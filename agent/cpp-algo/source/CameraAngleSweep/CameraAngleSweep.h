#pragma once

#include "MaaFramework/MaaAPI.h"

namespace cameraanglesweep
{

// 相机角度环扫（CameraAngleSweep）：站在固定位置，按 8 个目标朝向（0°/45°/…/315° 各叠加
// ±22.5° 随机抖动）依次转向、前进、采样截图，产物落运行目录 CameraAngleData/。
// 采样计划由进程内单例持有，任务重启（InitAction）时重新洗牌。

MaaBool MAA_CALL CameraAngleSweepInitActionRun(
    MaaContext* context,
    MaaTaskId task_id,
    const char* node_name,
    const char* custom_action_name,
    const char* custom_action_param,
    MaaRecoId reco_id,
    const MaaRect* box,
    void* trans_arg);

MaaBool MAA_CALL CameraAngleSweepStepRecognitionRun(
    MaaContext* context,
    MaaTaskId task_id,
    const char* node_name,
    const char* custom_recognition_name,
    const char* custom_recognition_param,
    const MaaImageBuffer* image,
    const MaaRect* roi,
    void* trans_arg,
    /* out */ MaaRect* out_box,
    /* out */ MaaStringBuffer* out_detail);

MaaBool MAA_CALL CameraAngleSweepTowardActionRun(
    MaaContext* context,
    MaaTaskId task_id,
    const char* node_name,
    const char* custom_action_name,
    const char* custom_action_param,
    MaaRecoId reco_id,
    const MaaRect* box,
    void* trans_arg);

MaaBool MAA_CALL CameraAngleSweepSnapshotActionRun(
    MaaContext* context,
    MaaTaskId task_id,
    const char* node_name,
    const char* custom_action_name,
    const char* custom_action_param,
    MaaRecoId reco_id,
    const MaaRect* box,
    void* trans_arg);

// 采样计划与角度归一化的启动自检（移植自 Go 侧 sweep_test.go 的三个用例）。
// 在 main.cpp 的 Test() 中调用，失败时 LogError 并返回 false。
bool RunSelfCheck();

} // namespace cameraanglesweep
