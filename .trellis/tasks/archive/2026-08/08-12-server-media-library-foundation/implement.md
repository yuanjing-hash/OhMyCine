# Implementation Plan

1. 增加 MediaLibrary/ScanRun/MediaEntry 模型、迁移、permissions 和 service contracts。
2. 实现 Storage-relative 路径解析与有界只读本地扫描器。
3. 实现媒体文件过滤、基础电影/剧集/季集解析、scan status/log persistence，以及创建/启用后自动首次全量初始化状态机。
4. 实现每库 `LibrarySupervisor`、初始化失败退避/立即重试、filesystem watcher 和统一 provider event/change cursor/polling contract；基线成功后才挂监听并立即 catch-up reconciliation，监听不接入持久 Job 队列。
5. 实现 FileTreeSnapshot、cursor、generation、事件 patch、全量/增量 diff 与 single-flight reconciliation。
6. 接入 Profile matcher 和待重分类状态。
7. 实现媒体库 CRUD、条件校验、显式扫描、状态/日志/条目 API：cloud + STRM 必须有经安全校验的 `strm_local_root`；local source 和 STRM 关闭时不接受输出目录。
8. 实现媒体库配置与扫描管理 UI，仅在 cloud 来源勾选 STRM 后展示必填本地目录选择器；挂载配置不复用该字段。
9. 用隔离树和 fake 115 事件覆盖电影、剧集、混合根、重叠范围、Reparse Point、partial scan、事件丢失/cursor 失效、双库并行和 STRM 条件字段矩阵测试。
10. 对用户指定的本机运行时媒体根做只读 live acceptance，并核对 4 个 MP4 未变化。
