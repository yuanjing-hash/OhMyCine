# 修复 Codex Windows 工具链环境

## Goal

诊断 Codex 本地项目处理变慢和 PATH 继承问题，恢复 Node/Python/uv 可见性，安装并验证 Rust 与 Go。

## Requirements

- 仅调整 Codex/Trellis 与 Windows 本地工具链，不适配 Claude Code。
- 解释 Codex 曾无法发现 Node/Python/uv 的原因，并区分安装问题、PATH 继承问题和会话启动时机。
- 保留安全的 Codex 环境继承策略，不为解决已恢复的 PATH 问题扩大环境变量暴露范围。
- 使用 Windows 原生包管理方式安装 Rustup/Rust 与 Go。
- 安装后刷新当前 PowerShell 进程 PATH，并说明 Codex 应用需要重启才能稳定继承新 PATH。
- 不安装 Docker，不修改 Player 用户配置、凭据或缓存。

## Acceptance Criteria

- [x] 当前 Codex shell 可运行 Node.js 22、npm、Python 3.14 和 uv，并记录其实际路径。
- [x] 测量 Git 状态和 Trellis 每轮 hook 的耗时，排除仓库规模与 hook 为主要瓶颈。
- [x] `rustup --version`、`rustc --version` 和 `cargo --version` 成功。
- [x] `go version` 成功。
- [x] 新安装路径已进入 Windows 用户 PATH，刷新后的 shell 可直接调用。
- [x] `.codex/config.toml` 通过配置检查，且无需适配 `.claude/`。
- [x] 记录完整验证结果以及需要重启 Codex 的事项。

## Definition of Done

- Rust 和 Go 安装成功并能报告版本。
- Codex、Trellis hook、Node/Python/uv 路径诊断结论有证据支撑。
- 配置改动通过语法/行为检查，未覆盖用户已有工作区改动。

## Technical Approach

- 保留项目 `.codex/config.toml` 的 `shell_environment_policy.inherit = "core"`：官方配置参考确认该字段控制子进程基线环境继承；当前会话已经继承完整 Windows PATH，无需改成 `all`。
- 使用 `winget install --id Rustlang.Rustup --exact` 和 `winget install --id GoLang.Go --exact`。
- 安装后从 Windows 用户/系统环境重新构造当前 PowerShell PATH，再验证版本；最终要求重启 Codex。
- Rust 的完整 Tauri 链接环境（MSVC Build Tools 或 MinGW）单独评估，不在未确认的情况下自动安装大型 IDE 工具链。

## Decision (ADR-lite)

**Context**: 上一会话只发现 Git/PowerShell，但当前会话已能发现 Node/Python/uv；仓库仅 268 个可见文件，`git status` 约 95 ms，Trellis 每轮 hook 约 107 ms。

**Decision**: 将之前的工具不可见判定为 Codex GUI 进程继承了旧 PATH，而不是 Node/Python/uv 未安装；不扩大环境继承，先安装用户明确缺失的 Rust 和 Go。

**Consequences**: 重启 Codex 后工具路径最稳定；Rustup 安装完成不等于 Tauri 原生链接依赖全部完成。

## Out of Scope

- Claude Code 配置。
- Docker。
- 自动安装 Visual Studio/完整 Android SDK 等大型依赖。
- 更换用户的自定义模型提供商或服务套餐。

## Notes

- 官方 Codex 配置参考：https://developers.openai.com/codex/config-reference/
- 当前版本：Node.js `v22.22.3`、npm `10.9.8`、Python `3.14.6`、uv `0.11.7`。
- 安装结果：Rustup `1.29.0`、Rustc/Cargo `1.97.1`、Go `1.26.5`。
- Rust 已安装 `x86_64-pc-windows-msvc` 与 `x86_64-pc-windows-gnu` 标准库目标；MSVC Build Tools 与 MinGW 链接器仍未安装，WebView2 已安装。
- winget 的 Go MSI 安装挂起并占用 Windows Installer；最终使用 Go 官方 ZIP、官方 SHA-256 校验和用户级 `~/.local/go` 安装完成。
- 新增的 Cargo/Go PATH 对新启动的 Codex 生效；当前应用进程需要重启。

## Research References

- [`research/windows-toolchain.md`](research/windows-toolchain.md) — Codex 环境继承、Windows 工具链与安装决策依据。
