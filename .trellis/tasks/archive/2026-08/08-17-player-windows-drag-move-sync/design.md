# Windows 顶部拖拽与视频窗口移动同步设计

## 根因

`WindowChrome` 当前先记录鼠标坐标，等待移动超过 4px，再异步查询最大化状态、必要时还原窗口，最后调用 `startDragging()`。Windows 原生拖动循环要求仍处于有效的按下手势中；这段等待会偶发错过第一次手势。

Windows mpv 后端又把 `Moved` 与 `Resized` 都处理成 `waiting-webview-bounds`。纯移动不会改变 surface 在客户区中的相对矩形，通常不会触发 `ResizeObserver`，所以独立的 mpv HWND 会停留在旧屏幕坐标，直到焦点或其他几何事件再次同步。

## 设计

### 顶部命中

- 顶部空白拖动层继续位于 `WindowChrome` 内，并继续避让更高 z-index 的按钮。
- 左键 `mousedown` 立即调用 `appWindow.startDragging()`，不再使用移动阈值、`screenX/screenY` 状态或预先 `unmaximize()`。
- 由 Windows/winit 的系统移动循环处理恢复、跨显示器移动和 Snap 最大化。
- 双击仍调用现有最大化/还原逻辑。

### mpv HWND 移动

- `Moved` 事件调用专用即时位置同步，使用缓存的物理 surface bounds。
- 同步只重算 owner client origin 对应的屏幕坐标；surface 宽高不从 owner 原生 client rect 推导。
- 该路径直接在 Tauri 窗口事件线程执行 `SetWindowPos`，避免为每个 move event 再排队到下一轮 main-thread callback。
- `Resized` 和 `ScaleFactorChanged` 继续等待前端最终 bounds；现有 resize 防回归契约不变。
- 即时路径继续设置 owner 为 z-order 前驱、`SWP_NOACTIVATE | SWP_SHOWWINDOW`，并复用全屏 overscan、圆角和诊断字段。

## 风险与回退

- 若 Tauri 在非 UI 线程派发 `Moved`，Win32 跨线程 `SetWindowPos` 仍是合法调用，但需通过 Windows MSVC 与运行测试确认视觉时序。
- 若即时调用造成 resize 抖动，仅回退 `Moved` 专用路径；不得回退 resize 等待 WebView bounds 的既有修复。
