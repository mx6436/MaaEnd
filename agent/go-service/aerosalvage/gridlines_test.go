package aerosalvage

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MaaXYZ/MaaEnd/agent/go-service/pkg/minicv"
	maa "github.com/MaaXYZ/maa-framework-go/v4"
)

func TestAerosalvageSamples(t *testing.T) {
	t.Parallel()

	repoRoot := repositoryRoot(t)
	placementConfig := readPlacementSiteTestConfig(t, repoRoot)
	params := readRecognitionParam(t, repoRoot)
	lineConfig := gridLineConfig(rectangle(params.GridROI))
	gridConfig := gridPointConfig(rectangle(params.CenterROI))
	pattern := filepath.Join(repoRoot, "assets", "resource_wlroots", "image", "浮空回收_*.png")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob samples: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no samples matched %s", pattern)
	}

	for _, path := range paths {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		t.Run(name, func(t *testing.T) {
			src := decodePNG(t, path)
			placementROIs := recognizePlacementSitesForTest(t, repoRoot, src, placementConfig)
			detected, err := DetectGridLines(src, lineConfig)
			if err != nil {
				t.Fatalf("detect grid lines: %v", err)
			}
			cleaned, err := CleanseGridLines(detected, cleanseConfig())
			if err != nil {
				t.Fatalf("clean grid lines: %v", err)
			}
			outputDir := filepath.Join(repoRoot, "install", "debug", name)
			if err := os.RemoveAll(outputDir); err != nil {
				t.Fatalf("clear debug directory: %v", err)
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				t.Fatalf("create debug directory: %v", err)
			}
			writePNG(t, filepath.Join(outputDir, "cleaned_lines.png"), drawCleanseOverlayDebug(src, detected, cleaned))
			gridPoints, err := DetectGridPoints(cleaned, detected.ROI, gridConfig)
			if err != nil {
				t.Fatalf("detect grid points: %v", err)
			}
			if len(gridPoints.Points) != 25 {
				t.Fatalf("grid point count = %d, want 25", len(gridPoints.Points))
			}
			writePNG(t, filepath.Join(outputDir, "grid_points.png"), drawGridPointsDebug(src, detected.ROI, gridPoints))
			writePNG(t, filepath.Join(outputDir, "placement_sites.png"), drawPlacementSitesDebug(src, placementROIs))
		})
	}
}

func drawCleanseOverlayDebug(src image.Image, detected *Result, cleaned *CleanseResult) *image.RGBA {
	bounds := src.Bounds()
	overlay := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(overlay, overlay.Bounds(), src, bounds.Min, draw.Src)
	drawRectangleDebug(overlay, detected.ROI, color.RGBA{R: 255, G: 220, A: 255})
	for _, familyResult := range cleaned.Families {
		for _, line := range familyResult.Lines {
			drawInfiniteLineDebug(overlay, line, detected.ROI, color.RGBA{R: 40, G: 255, B: 100, A: 255})
		}
	}
	return overlay
}

func drawGridPointsDebug(src image.Image, roi image.Rectangle, result *GridPointResult) *image.RGBA {
	bounds := src.Bounds()
	overlay := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(overlay, overlay.Bounds(), src, bounds.Min, draw.Src)
	drawRectangleDebug(overlay, roi, color.RGBA{R: 255, G: 220, A: 255})
	for _, point := range result.Points {
		center := roundPoint(point.Center)
		drawCrossDebug(overlay, center, color.RGBA{R: 40, G: 255, B: 100, A: 255})
		drawCircleDebug(overlay, center, 5, color.RGBA{R: 40, G: 255, B: 100, A: 255})
	}
	return overlay
}

func drawInfiniteLineDebug(dst *image.RGBA, line Line, roi image.Rectangle, c color.RGBA) {
	if from, to, ok := clipLineToRect(line, roi); ok {
		drawLineDebug(dst, from, to, c)
	}
}

func drawCrossDebug(dst *image.RGBA, point image.Point, c color.RGBA) {
	const radius = 6
	drawLineDebug(dst, point.Sub(image.Pt(radius, 0)), point.Add(image.Pt(radius, 0)), c)
	drawLineDebug(dst, point.Sub(image.Pt(0, radius)), point.Add(image.Pt(0, radius)), c)
}

func drawCircleDebug(dst *image.RGBA, center image.Point, radius int, c color.RGBA) {
	x, y := radius, 0
	errorValue := 1 - x
	for x >= y {
		points := [...]image.Point{
			center.Add(image.Pt(x, y)), center.Add(image.Pt(y, x)), center.Add(image.Pt(-y, x)), center.Add(image.Pt(-x, y)),
			center.Add(image.Pt(-x, -y)), center.Add(image.Pt(-y, -x)), center.Add(image.Pt(y, -x)), center.Add(image.Pt(x, -y)),
		}
		for _, point := range points {
			if point.In(dst.Bounds()) {
				dst.SetRGBA(point.X, point.Y, c)
			}
		}
		y++
		if errorValue < 0 {
			errorValue += 2*y + 1
		} else {
			x--
			errorValue += 2*(y-x) + 1
		}
	}
}

func drawRectangleDebug(dst *image.RGBA, roi image.Rectangle, c color.RGBA) {
	drawLineDebug(dst, roi.Min, image.Pt(roi.Max.X-1, roi.Min.Y), c)
	drawLineDebug(dst, image.Pt(roi.Max.X-1, roi.Min.Y), roi.Max.Sub(image.Pt(1, 1)), c)
	drawLineDebug(dst, roi.Max.Sub(image.Pt(1, 1)), image.Pt(roi.Min.X, roi.Max.Y-1), c)
	drawLineDebug(dst, image.Pt(roi.Min.X, roi.Max.Y-1), roi.Min, c)
}

func drawLineDebug(dst *image.RGBA, from, to image.Point, c color.RGBA) {
	dx := int(math.Abs(float64(to.X - from.X)))
	dxSign := -1
	if from.X < to.X {
		dxSign = 1
	}
	dy := -int(math.Abs(float64(to.Y - from.Y)))
	dySign := -1
	if from.Y < to.Y {
		dySign = 1
	}
	err := dx + dy
	for {
		if from.In(dst.Bounds()) {
			dst.SetRGBA(from.X, from.Y, c)
		}
		if from == to {
			return
		}
		twiceError := 2 * err
		if twiceError >= dy {
			err += dy
			from.X += dxSign
		}
		if twiceError <= dx {
			err += dx
			from.Y += dySign
		}
	}
}

func drawPlacementSitesDebug(src image.Image, placementROIs []image.Rectangle) *image.RGBA {
	bounds := src.Bounds()
	overlay := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(overlay, overlay.Bounds(), src, bounds.Min, draw.Src)
	red := color.RGBA{R: 255, G: 60, B: 60, A: 255}
	for _, roi := range placementROIs {
		if roi.Empty() {
			continue
		}
		draw.Draw(overlay, image.Rect(roi.Min.X, roi.Min.Y, roi.Max.X, roi.Min.Y+1), &image.Uniform{C: red}, image.Point{}, draw.Over)
		draw.Draw(overlay, image.Rect(roi.Min.X, roi.Max.Y-1, roi.Max.X, roi.Max.Y), &image.Uniform{C: red}, image.Point{}, draw.Over)
		draw.Draw(overlay, image.Rect(roi.Min.X, roi.Min.Y, roi.Min.X+1, roi.Max.Y), &image.Uniform{C: red}, image.Point{}, draw.Over)
		draw.Draw(overlay, image.Rect(roi.Max.X-1, roi.Min.Y, roi.Max.X, roi.Max.Y), &image.Uniform{C: red}, image.Point{}, draw.Over)
	}
	return overlay
}

type placementSiteTestConfig struct {
	Templates []string `json:"template"`
	ROI       [4]int   `json:"roi"`
	Threshold float64  `json:"threshold"`
}

func readPlacementSiteTestConfig(t *testing.T, repoRoot string) placementSiteTestConfig {
	t.Helper()
	path := aeroSalvagePipelinePath(repoRoot)
	data, err := readPipelineJSON(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var pipeline map[string]json.RawMessage
	if err := json.Unmarshal(data, &pipeline); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	rawNode, ok := pipeline[placementSiteTemplateNode]
	if !ok {
		t.Fatalf("%s missing from %s", placementSiteTemplateNode, path)
	}
	var config placementSiteTestConfig
	if err := json.Unmarshal(rawNode, &config); err != nil {
		t.Fatalf("unmarshal %s from %s: %v", placementSiteTemplateNode, path, err)
	}
	if config.Threshold == 0 {
		config.Threshold = 0.7
	}
	return config
}

func recognizePlacementSitesForTest(t *testing.T, repoRoot string, src image.Image, config placementSiteTestConfig) []image.Rectangle {
	t.Helper()
	img := minicv.ImageConvertRGBA(src)
	search := minicv.ImageCropRect(img, rectangle(config.ROI))
	integral := minicv.GetIntegralArray(search)
	rois := make([]image.Rectangle, 0)
	seen := make(map[image.Rectangle]struct{})
	for _, template := range config.Templates {
		tpl := minicv.ImageConvertRGBA(decodePNG(t, filepath.Join(repoRoot, "assets", "resource", "image", template)))
		hits := minicv.MatchTemplateMultiHit(search, integral, tpl, minicv.GetImageStats(tpl), config.Threshold, 25)
		for _, hit := range hits {
			roi := image.Rect(
				config.ROI[0]+int(math.Round(hit.X)),
				config.ROI[1]+int(math.Round(hit.Y)),
				config.ROI[0]+int(math.Round(hit.X))+tpl.Bounds().Dx(),
				config.ROI[1]+int(math.Round(hit.Y))+tpl.Bounds().Dy(),
			)
			if _, ok := seen[roi]; ok {
				continue
			}
			seen[roi] = struct{}{}
			rois = append(rois, roi)
		}
	}
	return rois
}

func TestRecognitionResult(t *testing.T) {
	repoRoot := repositoryRoot(t)
	src := decodePNG(t, filepath.Join(repoRoot, "assets", "resource_wlroots", "image", "浮空回收_平衡状态.png"))
	result, ok := (&GridRecognition{}).Run(nil, &maa.CustomRecognitionArg{
		Img:                    src,
		CustomRecognitionParam: readRecognitionParamJSON(t, repoRoot),
	})
	if !ok || result == nil {
		t.Fatal("recognition missed")
	}
	var detail recognitionDetail
	if err := json.Unmarshal([]byte(result.Detail), &detail); err != nil {
		t.Fatalf("unmarshal recognition detail: %v", err)
	}
	if len(detail.GridPoints) != 25 {
		t.Fatalf("grid point count = %d, want 25", len(detail.GridPoints))
	}
	for index, point := range detail.GridPoints {
		if point.Row != index/5 || point.Column != index%5 {
			t.Fatalf("grid point %d has row-column (%d,%d)", index, point.Row, point.Column)
		}
	}
}

func TestInitialStateRecognitionClearsCacheBeforeFailure(t *testing.T) {
	placementSitePositions = []gridPosition{{X: 1, Y: -1}}

	result, ok := (&InitialStateRecognition{}).Run(nil, nil)
	if ok || result != nil {
		t.Fatal("initial-state recognition succeeded with nil arguments")
	}
	if placementSitePositions != nil {
		t.Fatalf("placement-site cache = %+v, want nil", placementSitePositions)
	}
}

func TestNearestGridPositions(t *testing.T) {
	points := make([]GridPoint, 0, 25)
	for row := range 5 {
		for column := range 5 {
			points = append(points, GridPoint{
				Row: row, Column: column, Center: Point{X: float64(100 + column*20), Y: float64(200 + row*20)},
			})
		}
	}

	positions := nearestGridPositions([]image.Rectangle{
		image.Rect(136, 176, 146, 186),
		image.Rect(96, 196, 106, 206),
		image.Rect(136, 176, 146, 186),
	}, points)
	want := []gridPosition{{X: 0, Y: -2}, {X: -2, Y: -2}}
	if len(positions) != len(want) {
		t.Fatalf("placement-site count = %d, want %d", len(positions), len(want))
	}
	for index := range want {
		if positions[index] != want[index] {
			t.Fatalf("placement site %d = %+v, want %+v", index, positions[index], want[index])
		}
	}
}

func readRecognitionParam(t *testing.T, repoRoot string) recognitionParam {
	t.Helper()
	params, err := parseRecognitionParam(readRecognitionParamJSON(t, repoRoot))
	if err != nil {
		t.Fatalf("parse recognition parameters: %v", err)
	}
	return params
}

func readRecognitionParamJSON(t *testing.T, repoRoot string) string {
	t.Helper()
	path := aeroSalvagePipelinePath(repoRoot)
	data, err := readPipelineJSON(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var pipeline map[string]json.RawMessage
	if err := json.Unmarshal(data, &pipeline); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	rawNode, ok := pipeline["AeroSalvageGridRecognition"]
	if !ok {
		t.Fatalf("AeroSalvageGridRecognition missing from %s", path)
	}
	var node struct {
		CustomRecognitionParam json.RawMessage `json:"custom_recognition_param"`
	}
	if err := json.Unmarshal(rawNode, &node); err != nil {
		t.Fatalf("unmarshal AeroSalvageGridRecognition from %s: %v", path, err)
	}
	if len(node.CustomRecognitionParam) == 0 {
		t.Fatalf("AeroSalvageGridRecognition parameters missing from %s", path)
	}
	return string(node.CustomRecognitionParam)
}

func aeroSalvagePipelinePath(repoRoot string) string {
	return filepath.Join(repoRoot, "assets", "resource", "pipeline", "AeroSalvage.json")
}

func readPipelineJSON(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return stripJSONComments(data), nil
}

func stripJSONComments(data []byte) []byte {
	cleaned := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for index := 0; index < len(data); index++ {
		current := data[index]
		if inString {
			cleaned = append(cleaned, current)
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			cleaned = append(cleaned, current)
			continue
		}
		if current == '/' && index+1 < len(data) {
			switch data[index+1] {
			case '/':
				index += 2
				for index < len(data) && data[index] != '\n' {
					index++
				}
				if index < len(data) {
					cleaned = append(cleaned, data[index])
				}
			case '*':
				index += 2
				for index+1 < len(data) && (data[index] != '*' || data[index+1] != '/') {
					if data[index] == '\n' {
						cleaned = append(cleaned, '\n')
					}
					index++
				}
				index++
			default:
				cleaned = append(cleaned, current)
			}
			continue
		}
		cleaned = append(cleaned, current)
	}
	return cleaned
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}
