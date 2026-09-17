#pragma once

#include <chrono>
#include <optional>
#include <string>

namespace mapnavigator
{

struct NaviPosition
{
    double x = 0.0;
    double y = 0.0;
    // 角色朝向（小地图箭头）。
    double angle = 0.0;
    // 镜头朝向。与 angle 无固定关系, 定位成功帧且相机朝向模型可用时携带, 否则为空。
    std::optional<double> camera_angle;
    double score = 0.0;
    // 角色站在哪张可走面。实机定位给不出这个信息，只有预览端选了层才有值，不传就按区的主层走。
    std::optional<double> floor_y;
    bool valid = false;
    std::string zone_id;
    std::chrono::steady_clock::time_point timestamp;

    // 导航控制与朝向校验统一读镜头朝向: 转向只作用在镜头上, 角色朝向要迈一步才跟上, 所以操舵闭环、
    // HEADING 校验与滑索瞄准都该以镜头为准。镜头读数缺失时退回角色朝向 —— 没有它就没有朝向可用。
    double ControlHeading() const { return camera_angle.value_or(angle); }
};

// 上索要走到的一个站位。坐标记录的是随朝向变化的角格锚点, 设备模型占着锚点四周哪一格未知,
// 所以候选是各个可能的中心格; 末位那个从供电桩一侧让开, 让架子重新成为离身位最近的设备。
struct ZiplineMountSpot
{
    double x = 0.0;
    double y = 0.0;
};

} // namespace mapnavigator
