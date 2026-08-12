# 复检 Windows 本地调试环境

## Goal

验证重启后的 Node/Python/uv/Rust/Go、Player 前端构建、Tauri 原生前置条件以及 Server/CLI 调试能力。

## Requirements

- 验证 Node.js、npm、Python、uv、Rustup、Rustc、Cargo 和 Go 在新 Codex 会话中可直接调用。
- 检查 Windows Tauri 调试所需的 WebView2、Rust target、C/C++ 链接器和 libmpv 资源。
- 安装 Player 锁文件声明的依赖并运行 typecheck、lint 和 Web build。
- 对 Server 和 CLI 运行不依赖 Docker的 Go 测试/构建检查。
- 不启动或修改用户真实 Player 配置，不清理凭据、历史、缓存或 WebView 数据。

## Acceptance Criteria

- [x] 所有已安装语言工具报告版本及实际路径。
- [x] Player `npm ci`、typecheck、lint、Web build 有明确结果。
- [x] Rust/Tauri 可运行状态或缺失前置条件有明确证据。
- [x] Server/CLI Go 测试或构建有明确结果。
- [x] 给出当前可调试范围和下一步最小补齐项。

## Notes

- 此任务只做环境与构建复检，不修改产品功能。
- Player：`npm ci` 成功，审计 0 漏洞；typecheck、lint、Web build 全部通过；Vite 页面可从 `127.0.0.1:4173` 请求到。
- Windows libmpv wrapper、运行 DLL 与 GNU import library 已通过项目脚本下载成功。
- Rust/Tauri：Cargo 依赖可下载，但宿主构建脚本因缺少 Visual Studio C++ Build Tools 的 `link.exe` 失败；这是原生 Tauri 调试的当前硬阻塞。
- Server/CLI：目录存在但尚无 `go.mod` 或可执行项目，当前没有 Go 程序可测试。
- 当前 Codex 主进程仍是安装 Rust/Go 之前启动的旧进程；通过刷新 Windows 用户 PATH 可运行 Rust/Go，彻底退出并重启 Codex 后可直接识别。
