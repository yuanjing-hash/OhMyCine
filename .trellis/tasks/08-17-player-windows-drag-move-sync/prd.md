# 修复 Windows 顶部拖拽与视频窗口移动同步

## Goal

让 Windows Player 的自定义顶部标题栏始终可靠进入系统窗口拖动，并让播放中的独立 mpv 视频底层 HWND 与透明 Tauri/WebView 主窗口同步移动。

## Requirements

- 桌面端顶部空白标题栏区域的左键按下必须立即交给 Windows 原生窗口拖动，不得等待移动阈值、异步窗口状态查询或播放器手势判断。
- 系统拖动必须保留 Windows Snap 行为，包括拖到显示器顶部最大化、拖离最大化位置还原并继续移动。
- 返回、导航、搜索和最小化/最大化/关闭按钮仍可正常点击，不得被拖动区域覆盖。
- Player 播放手势、控制栏自动隐藏和弹幕层不得阻断顶部标题栏拖动。
- owner 窗口纯移动时，mpv HWND 必须使用已经确认的 surface bounds 与 owner 当前客户区屏幕原点立即同步位置。
- owner resize 与 DPI 变化仍等待 WebView `ResizeObserver` 上报最终 bounds，防止视频尺寸领先透明控制层。
- 最小化、恢复、最大化、全屏、圆角和 z-order 行为保持现有契约。
- 补充专项验证，覆盖即时拖动和 move/resize 两条不同几何时序。

## Acceptance Criteria

- [ ] 顶部空白区域第一次按下拖动即可移动窗口，不需要重复拖动。
- [ ] 拖到显示器顶部由 Windows 原生 Snap 最大化。
- [ ] 顶部交互按钮仍可点击，播放区域手势不影响标题栏拖动。
- [ ] 播放中连续移动窗口时，mpv 视频底层窗口与透明 UI 同步跟随，不再停留在旧位置等待后续事件。
- [ ] resize/DPI 路径没有恢复为使用原生客户区尺寸抢先缩放视频。
- [ ] Player typecheck、lint、Vite build、专项验证、Rust fmt/check/clippy/test 与 Windows MSVC 编译通过。

## Out of Scope

- 将 Windows 视频与 WebView 改造成同一个 DirectComposition/D3D 合成树。
- 调整播放器顶部按钮布局或移动端触摸手势。
- 修改 macOS、Linux、Android 的原生窗口实现。
