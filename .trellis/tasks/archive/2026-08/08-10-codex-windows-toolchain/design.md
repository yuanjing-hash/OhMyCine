# 设计：Codex Windows 工具链恢复

## 诊断边界

比较 Codex 当前进程、Windows 用户和 Windows 系统三层 PATH，并解析每个工具的实际可执行文件。使用实测耗时判断仓库或 Trellis hook 是否构成主要本地延迟。

## 配置策略

项目 `.codex/config.toml` 保持 `inherit = "core"`，因为当前 PATH 已完整继承。只有发现 `core` 在新会话中再次删除必要 PATH 时才改为更宽的策略；避免无必要地把更多环境变量传给子进程。

## 安装策略

通过 winget 安装官方 Rustup 与 Go 包。安装操作要求幂等：已安装时不重复覆盖。安装后重新读取注册表中的用户/系统 PATH 并更新当前 PowerShell 进程，仅用于即时验证；持久生效依赖安装器写入环境以及重启 Codex。

## 风险与回滚

- winget 网络/源失败：保留现状并报告，不改用来源不明的安装包。
- PATH 在当前 Codex 进程不刷新：手动重建当前 shell PATH，最终仍提示重启应用。
- Rust 无链接器：只承诺 Rustup/Rust/Cargo 安装；大型 MSVC/MinGW 工具链另行确认。
- 回滚：使用 winget uninstall 对应包；不删除用户目录或已有缓存。
