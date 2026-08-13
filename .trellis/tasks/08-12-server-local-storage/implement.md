# Parent Implementation Plan

1. 完成并验收子任务 `08-12-server-local-storage-foundation`。
2. 完成并验收子任务 `08-12-server-media-classification-profiles`。
3. 完成并验收子任务 `08-12-server-media-library-foundation`（媒体库监听/文件树 reconciliation 常驻并行，不承载到持久队列）。
4. 完成并验收子任务 `08-12-server-persistent-task-queue`（服务下载、上传、刮削和传输等有界任务）。
5. 完成并验收子任务 `08-12-server-downloader-management`。
6. 完成并验收子任务 `08-12-server-download-rules`。
7. 完成并验收子任务 `08-12-server-transfer-import-pipeline`。
8. 完成并验收子任务 `08-12-server-library-strm-proxy`（先用 fake cloud capability/文件树验收，首个 cloud Storage driver 后补 live test）。
9. 做父任务集成复核：Storage → MediaLibrary → Profile → Queue → Downloader → DownloadRule → Transfer/Import → STRM/Notify 引用、权限、审计和状态一致。
10. 更新 Server 架构、Web UI 设计和路线图；保留后续 Connection、Destination、CategoryRule、PT discovery、follow 等范围。
11. 使用用户指定、仅存在于本机运行时配置的媒体根做只读人工验收：创建 Storage、创建 MediaLibrary、选择默认/复制 Profile、运行扫描；确认 4 个现有 MP4 未被修改。
12. 用两个并行 fake MediaLibrary 验证常驻监听不进入全局队列；用 cloud 文件树快照验证 STRM/伴随文件全量与增量同步，以及关闭 STRM 后零本地投影。

## Cross-child Acceptance

- 一个 MediaLibrary 只能引用一个 Storage 和一个 Profile。
- 一个 Storage 可被多个 MediaLibrary 或未来 Destination 引用，但相对根必须在 Storage 边界内。
- 共享 Profile 编辑后影响所有引用库的后续扫描，并在 UI 保存前展示影响范围。
- Storage 删除、Profile 删除均遵守引用约束。
- 所有自动化检查由 `server/test.ps1` 统一通过。
- 规则显式 `overwrite` 时直接替换：本地永久删除旧目标，cloud 原生支持回收站则旧目标默认入云端回收站，否则永久替换；均不等待用户。手工真实数据删除仍反复确认。
