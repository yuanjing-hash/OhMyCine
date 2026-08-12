# 执行计划

1. 读取官方 Codex 配置参考以及项目 `.codex` 配置。
2. 记录三层 PATH、工具路径、版本、Git 与 Trellis hook 耗时。
3. 使用 winget 安装 `Rustlang.Rustup` 与 `GoLang.Go`。
4. 刷新当前 PowerShell PATH，运行 Rustup/Rust/Cargo/Go 版本验证。
5. 验证 Node/Python/uv 仍可用，运行 Codex/Trellis 配置检查。
6. 记录性能结论、安装结果、重启要求与未安装的 Tauri 链接依赖。
