# Server 下载器管理与任务监控

## Goal

提供独立下载器管理页，统一 qBittorrent/Transmission 和网盘原生离线下载器，并持续展示可靠的任务进度、速度、ETA 与状态。

## Requirements

1. Downloader provider 独立于 Storage/MediaLibrary；保存加密凭据、健康状态、输出 Storage/根约束和 capability。
2. 本地 downloader 输出到本地 staging Storage；cloud native offline downloader 只能直接输出到所属 cloud Storage。
3. Downloader 只描述下载能力、输出约束与 telemetry，不直接保存最终目标 MediaLibrary；最终编排由 DownloadRule 引用。
4. 持久化任务 owner、provider id、phase/status、bytes total/completed、progress、download/upload speed、ETA、last sampled、error。
5. adapter 按 capability polling/event；WebSocket 推送实时变化，REST 可恢复事实。未知 telemetry 显示 unknown，不伪造 0。
6. 支持暂停/恢复/取消；删除下载数据是独立高风险操作，默认不随取消执行。

## Acceptance Criteria

- [ ] 可添加并测试 fake/qBit downloader，提交任务并持续看到百分比、速度和 ETA。
- [ ] cloud offline downloader 的 capability 明确限制其原生输出 Storage，DownloadRule 无法绕过。
- [ ] Server 重启后任务状态可从 DB + provider reconciliation 恢复。
