# Implementation Plan

1. [x] 更新 v14 migration、MediaLibrary/DownloadTask/TransferTask 模型及迁移测试。
2. [x] 实现媒体库入库策略校验、排序服务/API 和任务目标库解析/快照。
3. [x] 将下载提交 API/UI 改为媒体库选择并展示路线摘要，补齐拖动排序与入库设置 UI。
4. [x] 实现本地 TransferService/worker：模板、边界、move/copy/symlink、伴随文件、冲突和幂等。
5. [x] 下载完成后幂等创建 transfer Job，完成后推进媒体库 dirty generation/reconciliation。
6. [x] 更新任务中心/下载摘要、架构文档和 Trellis specs。
7. [x] 运行 Go/Web UI/embedded/Windows 隔离全量验证，不连接真实 qBittorrent 或用户 Server。
8. [x] 修复 qBit 文件留在 staging root 时的 source resolution 回归，补分类路径优先、root fallback 和 reparse-point 安全测试；新任务额外显式调用 qBit `setLocation`。
9. [x] 新增 v15 migration、SeedingSettings/SeedingTask 及 DownloadTask 做种策略快照。
10. [x] 扩展 provider-neutral downloader telemetry 与 qBittorrent ratio/seeding_time/uploaded/state 映射。
11. [x] 实现 SeedingService 和短任务/定时调度；transfer 成功后按 move/copy/symlink 分流，清理保持幂等且可重试。
12. [x] 增加做种默认设置 API、任务 API、权限、系统设置 UI 和做种管理 UI，明示 copy 会删除暂存源文件、symlink 必须保源。
13. [x] 更新架构文档/Trellis specs（仓库尚无 OpenAPI 文件），运行定向与全量 Go/Web/embedded/Windows 隔离验证。
14. [x] 下载摘要返回 transfer Job 的安全 ID/状态，下载页为失败 download/transfer/seeding 分别提供重试按钮并补回归测试。
