#include "CameraAngleSweep.h"

#include <array>
#include <chrono>
#include <cmath>
#include <filesystem>
#include <format>
#include <random>
#include <thread>

#include <meojson/json.hpp>

#include <MaaUtils/Logger.h>
#include <MaaUtils/NoWarningCV.hpp>

#include "../MapLocator/MapLocator.h"
#include "../MapLocator/MapLocateAction.h"
#include "../MapLocator/MapTypes.h"
#include "../MapNavigator/Backend/backend.h"
#include "../MapNavigator/action_wrapper.h"
#include "../MapNavigator/controller_info_utils.h"
#include "../MapNavigator/controller_type_utils.h"
#include "../MapNavigator/navi_math.h"
#include "../utils.h"

namespace fs = std::filesystem;

namespace cameraanglesweep
{

namespace
{

constexpr MaaBool kMaaTrue = 1;
constexpr MaaBool kMaaFalse = 0;

// —— 采样计划 ——

constexpr int kSweepStepCount = 12;         // 十二个基准朝向（0°/30°/…/330°）
constexpr double kSweepJitterRangeDeg = 15.0; // 每个基准朝向叠加的随机偏移范围（±15°）
constexpr double kSweepDefaultRotationThresholdDeg = 2.0; // 默认转向闭环容差；相邻基准相隔 30°，
    // 取 2° 保证实际朝向贴合目标角且不会追着推断噪声转圈。可被 Pipeline 参数覆写。
constexpr const char* kSweepOutputDirName = "CameraAngleData"; // 快照输出目录（相对运行目录）

// —— 转向闭环 ——

constexpr int kLoopIntervalMs = 100;     // 闭环节流，与 Go 侧 INFER_INTERVAL_MS 对齐
constexpr int kTowardTimeoutMs = 5000;   // 单步转向总预算；超时视为正常结束，不算失败
constexpr int kNudgeBackMs = 250;        // 转向后先后退再前进，让身体朝向贴合镜头且不漂移
constexpr int kNudgeForwardMs = 75;
// 转向后的姿态稳定等待，按步行转速 270°/s 转 180° 的耗时估算（Go 侧 EtaOfRotation(180) 同源）。
constexpr int kSettleWaitMs = 667;
constexpr int kTurnStepIntervalMs = 100; // 分批转向的步进间隔下限

double normalizeHeading(double angle)
{
    angle = std::fmod(angle, 360.0);
    if (angle < 0.0) {
        angle += 360.0;
    }
    return angle;
}

// SweepPlan 是环扫的采样计划：每步一个目标朝向，每完成一步推进 step，直到全部完成。
// 无需加锁：MaaFramework 保证任务回调单线程（与 Go 侧 mapDataSweepState 同约定）。
struct SweepPlan
{
    std::array<double, kSweepStepCount> targets {};
    int step = 0;
    bool initialized = false;

    void reset()
    {
        static std::mt19937 rng { std::random_device {}() };
        std::uniform_real_distribution<double> jitter(-kSweepJitterRangeDeg, kSweepJitterRangeDeg);
        for (int i = 0; i < kSweepStepCount; ++i) {
            targets[i] = normalizeHeading(i * 30.0 + jitter(rng));
        }
        step = 0;
        initialized = true;
    }

    bool done() const { return step >= kSweepStepCount; }
};

SweepPlan& getPlan()
{
    static SweepPlan plan;
    return plan;
}

// 识别器可能在 InitAction 之前被求值，兜底懒生成。
SweepPlan& ensurePlan()
{
    auto& plan = getPlan();
    if (!plan.initialized) {
        plan.reset();
    }
    return plan;
}

// —— 参数与输出 ——

struct StepRecognitionParam
{
    // "remaining"（默认，还有剩余步骤时命中）或 "done"（计划耗尽时命中）。
    std::string mode;

    MEO_JSONIZATION(MEO_OPT mode);
};

struct TowardActionParam
{
    double rotation_threshold = kSweepDefaultRotationThresholdDeg;

    MEO_JSONIZATION(MEO_OPT rotation_threshold);
};

template <typename T>
T parseParam(const char* param_string)
{
    if (param_string == nullptr || std::strlen(param_string) == 0) {
        return T {};
    }
    const auto parsed = json::parse(param_string);
    T value {};
    if (!parsed || !value.from_json(*parsed)) {
        LogWarn << "Invalid param, using defaults" << VAR(param_string);
        return T {};
    }
    return value;
}

void writeJsonDetail(MaaStringBuffer* out_detail, const json::value& payload)
{
    if (out_detail == nullptr) {
        return;
    }
    const std::string json_text = payload.dumps();
    MaaStringBufferSet(out_detail, json_text.c_str());
}

// —— 采集与定位 ——

// AgentServer 环境下 MaaTaskerGetController 每调用一次就会销毁上一个 RemoteController
// 并新建一个，返回的指针在下一次调用前即失效。因此每个动作生命周期内只允许调用一次，
// 之后所有用途都必须复用同一个指针。
MaaController* getController(MaaContext* context)
{
    return context == nullptr ? nullptr : MaaTaskerGetController(MaaContextGetTasker(context));
}

bool isTaskStopping(MaaContext* context)
{
    return context != nullptr && MaaTaskerStopping(MaaContextGetTasker(context));
}

// 截一帧全屏。失败路径 LogError。
bool captureFrame(MaaController* controller, cv::Mat* out_frame)
{
    if (controller == nullptr) {
        LogError << "CameraAngleSweep: controller is null";
        return false;
    }
    const MaaCtrlId screencap_id = MaaControllerPostScreencap(controller);
    MaaControllerWait(controller, screencap_id);

    ScopedImageBuffer captured;
    if (!MaaControllerCachedImage(controller, captured.Get()) || MaaImageBufferIsEmpty(captured.Get())) {
        LogError << "CameraAngleSweep: cached image is empty";
        return false;
    }
    *out_frame = to_mat(captured.Get()).clone(); // to_mat 是零拷贝的壳，必须 clone 出来，
                                                 // 否则缓冲区销毁后带出去的是悬垂内存
    return true;
}

// 在给定帧上定位（共享 MapLocator 单例），成功时输出位置。
bool locateOnFrame(MaaController* controller, const cv::Mat& frame, maplocator::MapPosition* out_position)
{
    auto locator = maplocator::getOrInitLocator();
    if (!locator) {
        LogError << "CameraAngleSweep: locator init failed";
        return false;
    }

    cv::Mat minimap;
    const bool adb_roi = mapnavigator::IsAdbLikeControllerType(mapnavigator::DetectControllerType(controller));
    if (!maplocator::TryExtractMinimap(frame, adb_roi, &minimap)) {
        LogError << "CameraAngleSweep: minimap ROI extraction failed";
        return false;
    }

    const maplocator::LocateResult result = locator->locate(minimap, maplocator::LocateOptions {});
    if (result.status != maplocator::LocateStatus::Success || !result.position.has_value()) {
        LogWarn << "CameraAngleSweep: locate failed" << VAR(result.debugMessage);
        return false;
    }
    *out_position = result.position.value();
    return true;
}

// —— 转向原语（复用 MapNavigator 的零件，不使用其 HEADING 节点语义：
// 该节点接受 ±40° 容差且带前进脉冲，对需要精确朝向的采样不可接受）——

// 分批发送视角增量，与 MapNavigator 的 TurnToHeadingOnce 同逻辑。
bool turnViewDelta(mapnavigator::ActionWrapper& wrapper, double heading_delta)
{
    if (std::abs(heading_delta) <= 1.0) {
        return true;
    }
    const mapnavigator::SteeringTransportProfile profile = wrapper.SteeringProfile();
    const int step_count = static_cast<int>(std::ceil(std::abs(heading_delta) / profile.max_batch_delta_deg));
    const double step_deg = heading_delta / step_count;
    const int step_interval_ms = std::max(kTurnStepIntervalMs, profile.min_send_interval_ms);
    for (int i = 0; i < step_count; ++i) {
        int units = static_cast<int>(std::lround(step_deg * wrapper.DefaultTurnUnitsPerDegree()));
        if (units == 0) {
            units = step_deg > 0.0 ? 1 : -1;
        }
        if (!wrapper.SendViewDeltaSync(units, 0)) {
            LogError << "CameraAngleSweep: failed to send view delta" << VAR(heading_delta);
            return false;
        }
        if (i + 1 < step_count) {
            mapnavigator::utils::SleepFor(step_interval_ms);
        }
    }
    return true;
}

// 停住玩家 → 闭环转向到 target_heading（推断 rot → 转向 → 前后轻推 → 等稳定 → 再推断）。
// 超时按正常结束处理（返回 true），与 Go 侧 MapTrackerToward 语义一致。
bool towardHeading(MaaContext* context, double target_heading, double threshold_deg)
{
    // ActionWrapper 构造时取的 RemoteController 是本动作内唯一一次 MaaTaskerGetController，
    // 之后 capture/locate 一律复用 wrapper.GetCtrl()，避免旧指针被下一次调用销毁。
    mapnavigator::ActionWrapper wrapper(context);
    if (!wrapper.is_supported()) {
        LogError << "CameraAngleSweep: input backend unsupported" << VAR(wrapper.unsupported_reason());
        return false;
    }
    MaaController* controller = wrapper.GetCtrl();

    auto stopMovement = [&wrapper]() {
        wrapper.SetMovementStateSync(false, false, false, false, 0);
    };

    // 首次测量前确保玩家静止。
    stopMovement();

    const auto deadline = std::chrono::steady_clock::now() + std::chrono::milliseconds(kTowardTimeoutMs);
    auto next_loop_at = std::chrono::steady_clock::now();

    while (true) {
        std::this_thread::sleep_until(next_loop_at);
        next_loop_at = std::chrono::steady_clock::now() + std::chrono::milliseconds(kLoopIntervalMs);

        if (isTaskStopping(context)) {
            LogWarn << "CameraAngleSweep: task stopping, abort toward";
            stopMovement();
            return false;
        }

        // 超时检查放在循环开头：capture/locate 持续失败时也要能退出，而不是无限重试。
        if (std::chrono::steady_clock::now() >= deadline) {
            LogWarn << "CameraAngleSweep: toward timeout, ending orientation adjustment" << VAR(kTowardTimeoutMs);
            break;
        }

        cv::Mat frame;
        if (!captureFrame(controller, &frame)) {
            continue;
        }
        maplocator::MapPosition position;
        if (!locateOnFrame(controller, frame, &position)) {
            continue;
        }

        const double current = mapnavigator::NaviMath::NormalizeAngle(position.angle);
        const double target = normalizeHeading(target_heading);
        const double delta = mapnavigator::NaviMath::CalcDeltaRotation(current, target);
        LogDebug << "CameraAngleSweep: adjusting" << VAR(current) << VAR(target) << VAR(delta);

        if (std::abs(delta) <= threshold_deg) {
            LogInfo << "CameraAngleSweep: reached target orientation" << VAR(current) << VAR(target);
            break;
        }

        if (!turnViewDelta(wrapper, delta)) {
            stopMovement();
            return false;
        }

        // 前后轻推：身体朝向吸附到镜头朝向，且不离开原地。
        wrapper.SetMovementStateSync(false, false, true, false, kNudgeBackMs);
        wrapper.SetMovementStateSync(false, false, false, false, 0);
        wrapper.SetMovementStateSync(true, false, false, false, kNudgeForwardMs);
        stopMovement();

        // 等待姿态稳定后再做下一次测量。
        mapnavigator::utils::SleepFor(kSettleWaitMs);
    }

    stopMovement();
    return true;
}

} // namespace

MaaBool MAA_CALL CameraAngleSweepInitActionRun(
    [[maybe_unused]] MaaContext* context,
    [[maybe_unused]] MaaTaskId task_id,
    [[maybe_unused]] const char* node_name,
    [[maybe_unused]] const char* custom_action_name,
    [[maybe_unused]] const char* custom_action_param,
    [[maybe_unused]] MaaRecoId reco_id,
    [[maybe_unused]] const MaaRect* box,
    [[maybe_unused]] void* trans_arg)
{
    getPlan().reset();

    std::error_code ec;
    fs::create_directories(kSweepOutputDirName, ec);
    if (ec) {
        LogError << "CameraAngleSweep: failed to create output dir" << VAR(kSweepOutputDirName) << VAR(ec.message());
        return kMaaFalse;
    }
    LogInfo << "CameraAngleSweep: plan reset, output dir ready" << VAR(kSweepStepCount) << VAR(kSweepOutputDirName);
    return kMaaTrue;
}

MaaBool MAA_CALL CameraAngleSweepStepRecognitionRun(
    [[maybe_unused]] MaaContext* context,
    [[maybe_unused]] MaaTaskId task_id,
    [[maybe_unused]] const char* node_name,
    [[maybe_unused]] const char* custom_recognition_name,
    const char* custom_recognition_param,
    [[maybe_unused]] const MaaImageBuffer* image,
    const MaaRect* roi,
    [[maybe_unused]] void* trans_arg,
    MaaRect* out_box,
    MaaStringBuffer* out_detail)
{
    const auto param = parseParam<StepRecognitionParam>(custom_recognition_param);
    auto& plan = ensurePlan();

    if (out_box != nullptr && roi != nullptr) {
        *out_box = *roi;
    }

    if (param.mode == "done") {
        if (!plan.done()) {
            return kMaaFalse;
        }
        writeJsonDetail(out_detail, json::object { { "done", true } });
        return kMaaTrue;
    }

    if (plan.done()) {
        return kMaaFalse;
    }
    writeJsonDetail(
        out_detail,
        json::object {
            { "step", plan.step },
            { "angle", plan.targets[plan.step] },
        });
    return kMaaTrue;
}

MaaBool MAA_CALL CameraAngleSweepTowardActionRun(
    MaaContext* context,
    [[maybe_unused]] MaaTaskId task_id,
    [[maybe_unused]] const char* node_name,
    [[maybe_unused]] const char* custom_action_name,
    const char* custom_action_param,
    [[maybe_unused]] MaaRecoId reco_id,
    [[maybe_unused]] const MaaRect* box,
    [[maybe_unused]] void* trans_arg)
{
    const auto param = parseParam<TowardActionParam>(custom_action_param);
    const double threshold = param.rotation_threshold > 0.0 ? param.rotation_threshold : kSweepDefaultRotationThresholdDeg;

    auto& plan = ensurePlan();
    if (plan.done()) {
        LogWarn << "CameraAngleSweep: toward called after plan exhausted, ignoring";
        return kMaaTrue;
    }
    const int step = plan.step;
    const double target = plan.targets[step];

    if (!towardHeading(context, target, threshold)) {
        LogError << "CameraAngleSweep: toward failed" << VAR(step) << VAR(target);
        return kMaaFalse;
    }
    plan.step = step + 1;
    LogInfo << "CameraAngleSweep: step toward done" << VAR(step) << VAR(target) << VAR(threshold);
    return kMaaTrue;
}

MaaBool MAA_CALL CameraAngleSweepSnapshotActionRun(
    MaaContext* context,
    [[maybe_unused]] MaaTaskId task_id,
    [[maybe_unused]] const char* node_name,
    [[maybe_unused]] const char* custom_action_name,
    [[maybe_unused]] const char* custom_action_param,
    [[maybe_unused]] MaaRecoId reco_id,
    [[maybe_unused]] const MaaRect* box,
    [[maybe_unused]] void* trans_arg)
{
    // 本动作内唯一一次 MaaTaskerGetController，capture 与 locate 复用同一指针；
    // 两者用同一帧，保证保存的截图就是被推断的那一帧。
    MaaController* controller = getController(context);
    cv::Mat frame;
    if (!captureFrame(controller, &frame)) {
        return kMaaFalse;
    }
    maplocator::MapPosition position;
    if (!locateOnFrame(controller, frame, &position)) {
        LogError << "CameraAngleSweep: inference failed, aborting sweep";
        return kMaaFalse;
    }

    const std::string name = std::format("{}_x{:.1f}_y{:.1f}_r{:.1f}.png", position.zoneId, position.x, position.y, position.angle);
    const fs::path path = fs::path(kSweepOutputDirName) / name;

    std::error_code ec;
    fs::create_directories(kSweepOutputDirName, ec);
    if (ec) {
        LogError << "CameraAngleSweep: failed to create output dir" << VAR(kSweepOutputDirName) << VAR(ec.message());
        return kMaaFalse;
    }
    if (!cv::imwrite(MAA_NS::path_to_utf8_string(fs::absolute(path)), frame)) {
        LogError << "CameraAngleSweep: failed to write snapshot" << VAR(name);
        return kMaaFalse;
    }
    LogInfo << "CameraAngleSweep: snapshot saved" << VAR(name) << VAR(position.score);
    return kMaaTrue;
}

} // namespace cameraanglesweep
