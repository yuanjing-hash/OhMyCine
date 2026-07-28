# 修复 Player 播放上下文与媒体源清理

## Goal

修复 Player 数据源播放和删除生命周期中的跨层不一致：WebDAV 扫描缓存可以持久化；播放路由不再携带远程签名 URL、请求头或本地绝对路径；Emby 详情页选择的媒体版本真正参与流解析；删除媒体源时同步删除该来源的播放历史和扫描缓存。

## Requirements

- Rust raw scan cache 接受 TypeScript 已支持的 `webdav` 原始文件数据源类型。
- 数据源播放请求统一使用媒体身份和可选 `mediaSourceId`，由 PlayerView 在真正加载前即时解析 URL 与 headers。
- 数据源播放路由只携带 `sourceId`、`itemId`、`contextId` 和展示元数据，不携带播放 URL、本地绝对路径或认证 header。
- 本地文件路径仅保存在当前进程的短生命周期播放上下文中；文件拖放继续直接交给当前 PlayerView，不写入路由。
- Emby 必须使用用户选择的 `mediaSourceId` 解析对应版本，并让后续播放进度同步沿用同一媒体源会话。
- 删除媒体源后，删除该 `sourceId` 的本地播放历史，只影响目标来源。
- 删除原始文件媒体源后，清理该来源当前根目录的本地扫描缓存。
- 删除源配置是主操作；历史、扫描缓存和凭据清理失败不得把已删除的数据源重新显示出来。
- 增加跨层自动验证，防止 DataSource 类型、播放路由和 Tauri command 注册再次漂移。
- 完成 TypeScript、lint、Vite、Rust 与 Windows GNU release 构建验证，并生成最新 `ohmycine-player.exe`。

## Security Boundaries

- CloudDrive2/WebDAV token、Authorization header、临时直链和签名参数不得进入配置、localStorage、扫描缓存、日志、诊断、播放历史或路由状态。
- 本地绝对路径不得进入 Vue Router query/history。
- 播放请求只在即将调用 mpv 时解析，并只在内存中保留当前播放所需数据。
- 删除历史按经过校验的 `sourceId` 精确执行，不允许空值或控制字符扩大删除范围。

## Acceptance Criteria

- WebDAV 完成扫描后可从 SQLite raw scan cache 恢复。
- Home、媒体详情、数据源媒体库和队列切换生成的 Player 路由均不包含 `path`。
- 选中 Emby 非默认媒体版本后，实际播放请求使用该版本的 `mediaSourceId`。
- 删除媒体源 A 后，A 的本地继续观看记录消失，媒体源 B 的记录保留。
- 删除 raw source 后，其当前根目录扫描缓存被清除。
- 所有自动化检查和 Windows GNU release 构建通过。

## Out Of Scope

- 重写当前全量扫描与增量扫描算法。
- 将 Player 凭据存储迁移到系统 Keychain。
- 替换 Windows HWND/libmpv 渲染架构。
- GitHub push、tag 或发布。
