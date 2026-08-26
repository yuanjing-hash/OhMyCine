# 实施计划

1. 简化 `WindowChrome.vue`：移除延迟拖动状态与 mousemove/mouseup/mouseleave 链路，左键按下立即启动原生拖动。
2. 在 Windows mpv surface 中增加使用缓存 bounds 的同步位置函数，`OwnerWindowEvent::Moved` 立即调用。
3. 保持 `Resized` / `ScaleFactorChanged` 等待 WebView bounds，并更新诊断文本与注释。
4. 扩展 `verify-window-fullscreen-resize-sync.ts`，断言拖动无阈值/异步前置，move 与 resize 使用不同同步策略。
5. 更新 Player Windows 窗口规范与架构说明。
6. 执行 typecheck、lint、build、专项验证、Rust fmt/check/clippy/test 和 Windows MSVC 检查。
