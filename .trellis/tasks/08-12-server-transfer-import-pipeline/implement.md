# Implementation Plan

1. 定义 DownloadRoute/ImportPlan/TransferRun 状态机和迁移。
2. 实现五类 route resolver、空间/能力/权限预检和确认契约。
3. 实现本地 move/hardlink/copy 与 fake cloud upload/download/server-side-copy。
4. 实现识别、目标路径模板、ask 冲突预览/人工确认，以及 overwrite 的 trash-if-supported / permanent-replace 协议和审计。
5. 实现阶段 telemetry、幂等重试、WebSocket/REST 状态。
6. 通过 dirty generation 串联 MediaLibrary supervisor reconciliation、Profile、STRM 和通知；扫描/投影不创建 Queue Job，metadata scrape/refresh 仍按离散持久任务入队。
7. 做跨盘失败、进程重启、部分上传、目标冲突和取消测试。
