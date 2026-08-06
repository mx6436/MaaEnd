// Copyright (c) 2026 MaaEnd Contributors
package maptrackerdefault

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"

	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/rs/zerolog/log"
)

// —— MapDataSweep 地图数据环扫 ——

// sweepState 是环扫的采样计划：八个目标朝向（基准角 0°/45°/…/315° 各叠加一次 [-22.5°, 22.5°]
// 随机偏移）在任务启动时生成一次，每完成一步推进 step，直到 8 步全部完成。
//
// 无需加锁：MaaFramework 保证任务回调单线程（与 RoundState 同约定）。
type sweepState struct {
	targets     [8]float64 // 八个目标朝向（0–360，北为零向，顺时针）
	step        int        // 下一步下标；>= len(targets) 表示计划已耗尽
	initialized bool       // 计划是否已生成（InitAction 显式重置，识别器兜底懒生成）
}

// mapDataSweepState 是生产路径的单例：跨识别器/动作共享计划只能靠包级实例。
var mapDataSweepState = &sweepState{}

const (
	// sweepJitterRangeDeg 是每个目标朝向叠加的随机偏移范围（±22.5°）。
	sweepJitterRangeDeg = 22.5
	// sweepTowardRotationThresholdDeg 是默认的转向闭环容差（度）；相邻基准朝向相隔 45°，
	// 取 2° 保证实际朝向贴合目标角且不会追着推断噪声转圈。可在 Pipeline 的
	// MapDataSweepStep.custom_action_param 里覆写。
	sweepTowardRotationThresholdDeg = 2.0
	// sweepOutputDir 是截图输出目录（相对运行目录，与 debug/ 平级）。
	sweepOutputDir = "MapDataSweep"
)

// reset 重新生成一轮采样计划并回到第一步。
func (s *sweepState) reset() {
	for i := 0; i < len(s.targets); i++ {
		jitter := (rand.Float64()*2 - 1) * sweepJitterRangeDeg
		s.targets[i] = normalizeHeading(float64(i*45) + jitter)
	}
	s.step = 0
	s.initialized = true
}

// ensureInitialized 兜底：识别器可能在 InitAction 之前被求值，此时直接生成计划。
func (s *sweepState) ensureInitialized() {
	if !s.initialized {
		s.reset()
	}
}

// done 报告计划是否已耗尽。
func (s *sweepState) done() bool {
	return s.step >= len(s.targets)
}

// normalizeHeading 把角度归一化到 [0, 360)。
func normalizeHeading(v float64) float64 {
	v = math.Mod(v, 360)
	if v < 0 {
		v += 360
	}
	return v
}

var _ maa.CustomActionRunner = &MapDataSweepInitAction{}

// MapDataSweepInitAction 重置环扫采样计划（重新生成随机偏移）并创建输出目录。
// 任务入口节点调用一次；任务重启会重新洗牌，避免重复运行产出同批角度。
type MapDataSweepInitAction struct{}

func (a *MapDataSweepInitAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	mapDataSweepState.reset()
	if err := os.MkdirAll(sweepOutputDir, 0o755); err != nil {
		log.Error().
			Err(err).
			Str("component", "MapDataSweep").
			Str("dir", sweepOutputDir).
			Msg("failed to create output dir")
		return false
	}
	log.Info().
		Str("component", "MapDataSweep").
		Int("steps", len(mapDataSweepState.targets)).
		Msg("plan reset, output dir ready")
	return true
}

var _ maa.CustomRecognitionRunner = &MapDataSweepStepRecognition{}

type mapDataSweepStepRecognitionParam struct {
	// Mode 取值 "remaining"（默认，还有剩余步骤时命中）或 "done"（计划耗尽时命中）。
	Mode string `json:"mode,omitempty"`
}

// MapDataSweepStepRecognition 报告环扫计划的状态：mode=remaining 时在还有剩余步骤时命中，
// mode=done 时在计划耗尽时命中。Pipeline 用它做循环分支（MapDataSweepStep ↔ MapDataSweepEnd）。
type MapDataSweepStepRecognition struct{}

func (r *MapDataSweepStepRecognition) Run(ctx *maa.Context, arg *maa.CustomRecognitionArg) (*maa.CustomRecognitionResult, bool) {
	var param mapDataSweepStepRecognitionParam
	if arg.CustomRecognitionParam != "" {
		if err := json.Unmarshal([]byte(arg.CustomRecognitionParam), &param); err != nil {
			log.Error().
				Err(err).
				Str("component", "MapDataSweep").
				Msg("failed to parse recognition params")
			return nil, false
		}
	}
	mapDataSweepState.ensureInitialized()

	if param.Mode == "done" {
		if !mapDataSweepState.done() {
			return &maa.CustomRecognitionResult{Box: arg.Roi}, false
		}
		return &maa.CustomRecognitionResult{Box: arg.Roi, Detail: `{"done":true}`}, true
	}

	if mapDataSweepState.done() {
		return &maa.CustomRecognitionResult{Box: arg.Roi}, false
	}
	k := mapDataSweepState.step
	detail := fmt.Sprintf(`{"step":%d,"angle":%.1f}`, k, mapDataSweepState.targets[k])
	return &maa.CustomRecognitionResult{Box: arg.Roi, Detail: detail}, true
}

var _ maa.CustomActionRunner = &MapDataSweepTowardAction{}

type mapDataSweepTowardActionParam struct {
	// RotationThreshold 是转向闭环容差（度），缺省用 sweepTowardRotationThresholdDeg。
	RotationThreshold float64 `json:"rotation_threshold,omitempty"`
}

// MapDataSweepTowardAction 执行当前步的转向：目标朝向来自采样计划，复用 MapTrackerToward 的
// 闭环旋转（推断 rot → 转向 → 再推断，直到进入容差）。转向成功后推进到下一步。
type MapDataSweepTowardAction struct{}

func (a *MapDataSweepTowardAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	var param mapDataSweepTowardActionParam
	if arg.CustomActionParam != "" {
		if err := json.Unmarshal([]byte(arg.CustomActionParam), &param); err != nil {
			log.Error().
				Err(err).
				Str("component", "MapDataSweep").
				Msg("failed to parse action params")
			return false
		}
	}
	rotationThreshold := param.RotationThreshold
	if rotationThreshold == 0 {
		rotationThreshold = sweepTowardRotationThresholdDeg
	}

	mapDataSweepState.ensureInitialized()
	if mapDataSweepState.done() {
		log.Warn().
			Str("component", "MapDataSweep").
			Msg("toward called after plan exhausted, ignoring")
		return true
	}
	k := mapDataSweepState.step
	target := mapDataSweepState.targets[k]
	towardParam, err := json.Marshal(map[string]any{
		"angle":              target,
		"rotation_threshold": rotationThreshold,
	})
	if err != nil {
		log.Error().
			Err(err).
			Str("component", "MapDataSweep").
			Msg("failed to marshal toward params")
		return false
	}
	// 直接调用同包的 MapTrackerToward：角度参数是动态的，无法写死在 Pipeline JSON 里，
	// 与 CharacterController 用 RunAction 覆写参数的思路等价，省去资源里的私有节点。
	ok := (&MapTrackerToward{}).Run(ctx, &maa.CustomActionArg{
		CurrentTaskName:   arg.CurrentTaskName,
		CustomActionName:  "MapTrackerToward",
		CustomActionParam: string(towardParam),
	})
	if !ok {
		log.Error().
			Int("step", k).
			Float64("angle", target).
			Str("component", "MapDataSweep").
			Msg("toward failed")
		return false
	}
	mapDataSweepState.step = k + 1
	log.Info().
		Int("step", k).
		Float64("angle", target).
		Float64("threshold", rotationThreshold).
		Str("component", "MapDataSweep").
		Msg("step toward done")
	return true
}

var _ maa.CustomActionRunner = &MapDataSweepSnapshotAction{}

// MapDataSweepSnapshotAction 推断当前步的位置与角色朝向，并把推断所用的同一帧截图保存为
// 无损 PNG，文件名为 {mapName}_x{X}_y{Y}_r{R}.png（坐标保留一位小数；角度取推断整数值，
// MapTrackerInfer 的 Rot 为整数，不带小数点），落在运行目录 MapDataSweep/ 下。
// 推断失败或写入失败时返回 false，让任务中止。
type MapDataSweepSnapshotAction struct{}

func (a *MapDataSweepSnapshotAction) Run(ctx *maa.Context, arg *maa.CustomActionArg) bool {
	ctrl := ctx.GetTasker().GetController()
	img, err := captureFullScreen(ctrl)
	if err != nil {
		log.Error().
			Err(err).
			Str("component", "MapDataSweep").
			Msg("failed to capture screen")
		return false
	}
	result, err := inferOnImage(ctx, img)
	if err != nil {
		log.Error().
			Err(err).
			Str("component", "MapDataSweep").
			Msg("inference failed, aborting sweep")
		return false
	}
	name := fmt.Sprintf("%s_x%.1f_y%.1f_r%d.png", result.MapName, result.X, result.Y, result.Rot)
	path := filepath.Join(sweepOutputDir, name)
	if err := os.MkdirAll(sweepOutputDir, 0o755); err != nil {
		log.Error().
			Err(err).
			Str("component", "MapDataSweep").
			Str("dir", sweepOutputDir).
			Msg("failed to create output dir")
		return false
	}
	f, err := os.Create(path)
	if err != nil {
		log.Error().
			Err(err).
			Str("component", "MapDataSweep").
			Str("path", path).
			Msg("failed to create snapshot file")
		return false
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Error().
			Err(err).
			Str("component", "MapDataSweep").
			Str("path", path).
			Msg("failed to encode snapshot png")
		return false
	}
	log.Info().
		Str("path", path).
		Float64("locConf", result.LocConf).
		Float64("rotConf", result.RotConf).
		Str("component", "MapDataSweep").
		Msg("snapshot saved")
	return true
}

// inferOnImage 在给定帧上执行 MapTrackerInfer（与 doInfer 同路径、同参数），
// 保证保存的截图就是被推断的那一帧。
func inferOnImage(ctx *maa.Context, img image.Image) (*MapTrackerInferResult, error) {
	mapNameRegex := buildMapNameRegex("", "")
	inferConfig, err := json.Marshal(map[string]any{
		"map_name_regex": mapNameRegex,
		"precision":      mapTrackerInferDefaultParam.Precision,
		"threshold":      mapTrackerInferDefaultParam.Threshold,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inference config: %w", err)
	}
	taskDetail, err := ctx.GetTaskJob().GetDetail()
	if err != nil {
		return nil, fmt.Errorf("failed to get task detail: %w", err)
	}
	resultWrapper, hit := MapTrackerInferRunner.Run(ctx, &maa.CustomRecognitionArg{
		TaskID:                 taskDetail.ID,
		CurrentTaskName:        taskDetail.Entry,
		CustomRecognitionName:  "MapTrackerInfer",
		CustomRecognitionParam: string(inferConfig),
		Img:                    img,
		Roi:                    maa.Rect{0, 0, img.Bounds().Dx(), img.Bounds().Dy()},
	})
	if !hit {
		return nil, fmt.Errorf("map tracking inference did not hit")
	}
	if resultWrapper == nil || resultWrapper.Detail == "" {
		return nil, fmt.Errorf("map tracking inference result is empty")
	}
	var result MapTrackerInferResult
	if err := json.Unmarshal([]byte(resultWrapper.Detail), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inference result: %w", err)
	}
	return &result, nil
}
