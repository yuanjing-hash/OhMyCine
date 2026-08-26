# 实现 Player 签名自动更新与发布渠道

## Goal

让 Player 使用 Tauri 官方签名更新机制从 OhMyCine GitHub Releases 检测、下载并安装新版本，支持 Beta 与正式渠道、启动自动检测、设置页手动检测和清晰的下载/安装反馈。

## Requirements

- 使用 `tauri-plugin-updater` 验证每个更新包的 minisign 签名；不得下载任意 GitHub EXE 后直接覆盖程序。
- 更新公钥进入应用配置；私钥只保存在仓库外的受限本地文件和 GitHub Actions Secret，不得提交、打印或进入构建产物。
- 设置页增加“更新”入口，允许用户启用/停用启动自动检测，并选择 `Beta` 或 `正式版` 渠道。
- 设置页提供“立即检测更新”按钮并明确显示检查中、已是最新、发现更新、失败等反馈。
- 应用启动时在启用自动检测的情况下执行一次 best-effort 检查；发现更新后显示全局确认弹窗，不静默关闭播放器或安装。
- Beta 渠道选择 GitHub Releases 中最新的非草稿发布，包括 prerelease 与正式发布；正式渠道只选择非草稿且非 prerelease 的发布。
- GitHub Release 必须包含 `latest.json` 和对应 NSIS `.sig`；缺少清单或签名时视为不可更新，不回退到未签名安装。
- 更新清单 URL 只允许固定仓库 `yuanjing-hash/OhMyCine` 的 HTTPS GitHub Release asset。
- 标准模式使用默认 NSIS 更新安装；便携模式向 NSIS 传入当前 EXE 目录作为安装目录并保留原有 `portable.flag`、`data`、`cache`、`logs`。
- 下载时展示字节进度；安装前再次确认，Windows 安装器启动后允许当前应用退出并由安装器重启。
- 普通本地 `tauri:build:windows` 不要求签名私钥；发布工作流使用独立 updater config 打开 `createUpdaterArtifacts`。
- 发布工作流支持 `beta` / `stable` 输入，Beta 创建 prerelease，Stable 创建普通 Release；tag push 保持 Beta 默认以兼容当前流程。
- 发布工作流生成并上传安装包签名和静态 `latest.json`，清单 URL 指向同一个 tag 的签名 NSIS 安装包。
- 更新 Player 设计、安全设计、路线图和 Trellis 可执行规范。

## Acceptance Criteria

- 设置页可以保存自动检测开关和 Beta/正式渠道，并在保存后明确提示成功。
- 启动自动检测和“立即检测更新”共用同一更新服务，不重复并发请求。
- Beta/正式渠道对同一 GitHub Releases 列表产生符合 prerelease 规则的不同选择。
- 无更新时显示当前已是最新版本；有更新时展示版本、发布日期和发布说明。
- 用户确认后显示下载进度，并调用 Tauri 签名验证与安装流程。
- 伪造清单、错误签名、非 GitHub URL、其他仓库 asset 或缺少 `.sig` 时不能安装。
- 便携模式更新参数指向当前 EXE 目录，不删除 `portable.flag` 或便携数据目录。
- 发布工作流在缺少签名 Secret 时明确失败，不生成未签名 updater 清单。
- TypeScript、lint、Vite、Rust 测试、Windows GNU check 和普通 release EXE 构建通过。

## Release Key Boundary

- 本地私钥路径：`~/.config/ohmycine/updater/ohmycine-updater.key`，权限必须为 `0600`。
- 仓库只提交公钥。
- GitHub Actions 需要配置 `TAURI_SIGNING_PRIVATE_KEY`；私钥丢失会导致已安装客户端无法继续更新，必须在正式发布前做离线备份。

## Out Of Scope

- 降级安装和版本回滚。
- 后台静默安装或绕过用户确认。
- macOS/Linux updater artifact 发布。
- 自动配置 GitHub Secrets 或推送 GitHub。
