# Design: Downloader Management

统一 adapter 暴露 submit/get/list/pause/resume/cancel 和 telemetry capabilities。DownloadTask 是 Server 事实记录，provider task ID 是外部引用。scheduler 执行有界 reconciliation，WebSocket 只做增量传输，不替代 REST/DB。

