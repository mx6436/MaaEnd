// 静态截图批量定位 / 流式追踪 CLI（本地工作台工具，随本仓库的 local/ 目录使用，不随 MaaEnd 发布）。
//
// 直连 MapLocator 核心：不依赖 MaaFramework，不经过识别流水线。批量模式每张图先
// resetTrackingState()，再以 force_global_search=true 最多调用 3 次 locate() 完成
// 冷启动共识（第 1 次调用后 buffer 建立，同一张图的后续调用不加 reset，共识才能在
// 3 次内攒满）。--stream 流式模式从 stdin 逐行读路径并立即处理（不等 EOF）：
// 不 resetTrackingState()、force_global_search=false、每帧一次 locate()，追踪状态
// 跨帧延续（冷启动共识也由连续帧攒满），供 live.py 之类的实时消费方使用。
//
// 用法:
//   map-locate --resource-dir <dir> [--output <jsonl>] [--max-attempts N] [image.png ...]
//   map-locate --resource-dir <dir> --stream   # 帧路径从 stdin 逐行读，一帧一行 JSON
//
// 资源目录布局:
//   <dir>/image/MapLocator/**.png
//   <dir>/model/map/cls.onnx (+ cls.json, tile_mapping.json)
//
// 无位置参数时从 stdin 逐行读取图片路径；每张图向输出写一行 JSON：
//   name, status, message, zone, x, y, rot, scale, locConf, isHeld, latencyMs, attempts, elapsedMs
// scale 为 zone 的 ZoneTemplateScale（底图与观测的像素尺度比，无缩放 zone 为 1.0），
// 供训练/实机侧消费定位记录，避免在消费方镜像 zone -> scale 表。定位失败时无 zone，
// scale 恒为 1.0（不参与消费）。
// status 与 LocateStatus 一致（0 Success / 1 TrackingLost / 2 ScreenBlocked / 3 Teleported /
// 4 YoloFailed / 5 NotInitialized）；CLI 级失败用负数（-1 读图失败，-2 小地图 ROI 越界）。

#include "MapLocator.h"
#include "MapTypes.h"

#include <meojson/json.hpp>

#include <MaaUtils/Logger.h>
#include <MaaUtils/NoWarningCV.hpp>

#include <chrono>
#include <cstdlib>
#include <cstring>
#include <algorithm>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

namespace fs = std::filesystem;
using namespace maplocator;

namespace
{

constexpr int kDefaultMaxAttempts = 3;
constexpr int kStatusReadFailed = -1;
constexpr int kStatusRoiFailed = -2;

struct ImageResult
{
    std::string name;
    int status = kStatusReadFailed;
    std::string message;
    std::string zone;
    double x = 0.0;
    double y = 0.0;
    double rot = 0.0;
    double scale = 1.0;
    double locConf = 0.0;
    bool isHeld = false;
    long long latencyMs = 0;
    int attempts = 0;
    long long elapsedMs = 0;

    MEO_JSONIZATION(name, status, message, zone, x, y, rot, scale, locConf, isHeld, latencyMs, attempts, elapsedMs)
};

void PrintUsage(const char* argv0)
{
    std::cerr << "usage: " << argv0
              << " --resource-dir <dir> [--output <jsonl>] [--max-attempts N] [--stream] [image.png ...]\n"
              << "       --stream: stdin 逐行读帧路径并立即处理，不重置追踪状态，每帧一次 locate\n"
              << "       (批量模式无位置参数时从 stdin 读全部路径后逐张处理)\n";
}

bool ParseArgs(
    int argc,
    char** argv,
    fs::path* resource_dir,
    fs::path* output,
    int* max_attempts,
    bool* stream_mode,
    std::vector<std::string>* inputs)
{
    for (int i = 1; i < argc; ++i) {
        const std::string arg = argv[i];
        if (arg == "--resource-dir" && i + 1 < argc) {
            *resource_dir = argv[++i];
        }
        else if (arg == "--output" && i + 1 < argc) {
            *output = argv[++i];
        }
        else if (arg == "--max-attempts" && i + 1 < argc) {
            *max_attempts = std::max(1, std::atoi(argv[++i]));
        }
        else if (arg == "--stream") {
            *stream_mode = true;
        }
        else if (arg.rfind("--", 0) == 0) {
            std::cerr << "unknown argument: " << arg << "\n";
            return false;
        }
        else {
            inputs->emplace_back(arg);
        }
    }
    return !resource_dir->empty();
}

} // namespace

int main(int argc, char** argv)
{
    // JSON 走 stdout，日志必须闭嘴；错误信息由 CLI 自己处理。
    MAA_NS::LogNS::Logger::get_instance().set_stdout_level(MAA_NS::LogNS::level::off);

    fs::path resource_dir;
    fs::path output_path;
    int max_attempts = kDefaultMaxAttempts;
    bool stream_mode = false;
    std::vector<std::string> inputs;
    if (!ParseArgs(argc, argv, &resource_dir, &output_path, &max_attempts, &stream_mode, &inputs)) {
        PrintUsage(argv[0]);
        return 2;
    }
    if (stream_mode && !inputs.empty()) {
        std::cerr << "--stream reads image paths from stdin; positional arguments are not allowed\n";
        return 2;
    }

    MapLocatorConfig config;
    config.mapResourceDir = (resource_dir / "image" / "MapLocator").string();
    config.yoloModelPath = (resource_dir / "model" / "map" / "cls.onnx").string();
    config.yoloThreads = 4;

    MapLocator locator;
    if (!locator.initialize(config)) {
        std::cerr << "MapLocator initialize failed: mapResourceDir=" << config.mapResourceDir
                  << " yoloModelPath=" << config.yoloModelPath << "\n";
        return 1;
    }

    std::ofstream file_out;
    std::ostream* out = &std::cout;
    if (!output_path.empty()) {
        file_out.open(output_path, std::ios::out | std::ios::trunc);
        if (!file_out) {
            std::cerr << "cannot open output file: " << output_path << "\n";
            return 1;
        }
        out = &file_out;
    }

    LocateOptions options;
    options.force_global_search = true;

    auto apply_result = [](const LocateResult& res, ImageResult* result) {
        result->status = static_cast<int>(res.status);
        result->message = res.debugMessage;
        if (res.status == LocateStatus::Success && res.position.has_value()) {
            const MapPosition& pos = res.position.value();
            result->zone = pos.zoneId;
            result->x = pos.x;
            result->y = pos.y;
            result->rot = pos.angle;
            result->scale = ZoneTemplateScale(pos.zoneId);
            result->locConf = pos.score;
            result->isHeld = pos.isHeld;
            result->latencyMs = pos.latencyMs;
        }
    };

    auto process_image = [&](const std::string& path, bool streaming) {
        const auto start = std::chrono::steady_clock::now();

        ImageResult result;
        result.name = fs::path(path).filename().string();

        const cv::Mat frame = cv::imread(path, cv::IMREAD_COLOR);
        cv::Mat minimap;
        if (frame.empty()) {
            result.status = kStatusReadFailed;
            result.message = "imread failed";
        }
        else if (!TryExtractMinimap(frame, /*use_adb_minimap_roi=*/false, &minimap)) {
            result.status = kStatusRoiFailed;
            result.message = "minimap ROI out of bounds";
        }
        else if (streaming) {
            // 流式追踪：不重置跨帧状态、不强制全局搜索，冷启动共识与 tracking 都由连续帧推进
            LocateOptions stream_options;
            stream_options.force_global_search = false;
            result.attempts = 1;
            apply_result(locator.locate(minimap, stream_options), &result);
        }
        else {
            locator.resetTrackingState();
            for (int attempt = 1; attempt <= max_attempts; ++attempt) {
                result.attempts = attempt;
                const LocateResult res = locator.locate(minimap, options);
                apply_result(res, &result);
                if (res.status == LocateStatus::Success && res.position.has_value()) {
                    break;
                }
            }
        }

        result.elapsedMs =
            std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now() - start).count();
        *out << json::value(result).dumps() << '\n';
        out->flush();
    };

    if (stream_mode) {
        std::string line;
        while (std::getline(std::cin, line)) {
            if (!line.empty()) {
                process_image(line, true);
            }
        }
    }
    else {
        if (inputs.empty()) {
            std::string line;
            while (std::getline(std::cin, line)) {
                if (!line.empty()) {
                    inputs.push_back(line);
                }
            }
        }
        for (const std::string& path : inputs) {
            process_image(path, false);
        }
    }

    return 0;
}
