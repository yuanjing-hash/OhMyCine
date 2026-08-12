# Windows 工具链研究

## 结论

- Codex 官方配置参考将 `shell_environment_policy.inherit` 定义为子进程的基线环境继承策略，取值为 `all | core | none`。本项目使用 `core`，实测仍继承 Windows `PATH`，所以不需要为 Node/Python/uv 改成 `all`。
- Windows GUI 应用只在进程启动时获得环境快照。安装器或 `SetEnvironmentVariable` 更新用户 PATH 后，已经运行的 Codex 不会自动刷新；重启 Codex 是稳定生效方式。
- Rust 官方推荐 rustup 管理工具链。Rustup 安装了 stable MSVC 默认主机；本项目还需要 `x86_64-pc-windows-gnu` 目标标准库，但真正链接 Tauri 仍需 MSVC Build Tools 或 MinGW。
- Go 官方同时提供 MSI 与 ZIP。MSI 被挂起的 Windows Installer 锁阻塞时，校验官方发布清单 SHA-256 后进行用户级 ZIP 安装是可验证、可回滚的替代方案。
- uv 已在 `~/.local/bin`，Python 已在用户 PATH；Trellis hook 使用直接 `python`，实测单次约 100 ms，没必要改为 `uv run`。

## 官方参考

- OpenAI Codex configuration reference: https://developers.openai.com/codex/config-reference/
- Rust install: https://www.rust-lang.org/tools/install
- Go downloads/install: https://go.dev/dl/ and https://go.dev/doc/install
- uv installation: https://docs.astral.sh/uv/getting-started/installation/

## 性能判断

- 仓库可见文件约 268 个，`git status` 约 95 ms。
- Trellis `UserPromptSubmit` hook 平均约 107 ms。
- 因此本地仓库和 Trellis hook 不是秒级延迟来源。全局配置使用高推理强度、默认服务等级和自定义模型提供商，这些更可能主导响应延迟。
