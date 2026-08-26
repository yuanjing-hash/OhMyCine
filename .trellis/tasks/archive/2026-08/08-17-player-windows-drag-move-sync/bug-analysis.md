# Bug Analysis: Windows 标题栏首次拖动与 mpv underlay 移动滞后

## 1. Root Cause Category

- **Category**: B - Cross-Layer Contract / E - Implicit Assumption
- **Specific Cause**: Vue 标题栏把原生拖动延迟到 4px 阈值和异步最大化查询之后，错误假设 Windows 仍会接受该按下手势。Rust 又把 `Moved`、`Resized` 和 `ScaleFactorChanged` 统一当成等待 WebView bounds 的事件，忽略了纯移动不会触发新的布局尺寸。

## 2. Why Previous Coverage Failed

1. 旧修复聚焦 resize 时视频抢先缩放，正确禁止 owner resize 使用原生客户区尺寸，但把 `Moved` 一并放进等待分支，扩大了规则适用范围。
2. `VideoPlayer` 的前端 `onMoved(reportBounds)` 让静态设计看起来存在最终回报，却没有保证回报早于独立 HWND 的可见滞后，也不能替代原生 move event 的即时跟随。
3. 专项验证只断言 resize 等待 WebView bounds，没有断言首次拖动不能有异步前置、move 必须走独立即时路径。

## 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Architecture | 区分纯 move 的位置同步与 resize/DPI 的尺寸同步 | DONE |
| P0 | Interaction | 顶部左键按下立即进入唯一的 native `startDragging()` 路径 | DONE |
| P0 | Test Coverage | 专项断言无阈值/无异步 unmaximize、move 使用 cached bounds + `SWP_NOSIZE` | DONE |
| P1 | Documentation | 更新 Player component、Windows underlay 和跨层思考规范 | DONE |
| P1 | Runtime | Windows 实测首次拖动、Snap、连续移动和跨显示器 DPI | TODO（由产品测试完成） |

## 4. Systematic Expansion

- **Similar Issues**: 任何“透明 UI + 独立原生 surface”实现都需要分别定义 move、resize、DPI、minimize、restore 和 z-order 时序。
- **Design Improvement**: 原生事件只能改变自己拥有的维度；move 可改变屏幕坐标，WebView layout 才能确认尺寸。
- **Process Improvement**: 窗口专项回归必须同时包含首次输入时序和连续几何事件，不能只靠编译与最终状态断言。

## 5. Knowledge Capture

- [x] `.trellis/spec/frontend/component-guidelines.md`
- [x] `.trellis/spec/frontend/directory-structure.md`
- [x] `.trellis/spec/guides/cross-layer-thinking-guide.md`
- [x] `docs/architecture/03-player-design.md`
- [x] `docs/architecture/06-roadmap.md`
- [x] `player/scripts/verify-window-fullscreen-resize-sync.ts`
