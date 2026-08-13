# Design: Media Library Foundation

MediaLibrary 配置、FileTreeSnapshot、ReconciliationRun 和 MediaEntry 分表。扫描器依赖 Storage reader/event 接口，只接收 provider-relative paths；本地 adapter 在服务端安全解析绝对路径。扫描使用有界深度/目录/条目数，支持 partial 状态和逐项错误。

每个启用库由独立 `LibrarySupervisor` 常驻管理，生命周期为 Server start → load enabled libraries → one supervisor per library → ensure baseline → attach event source → catch-up reconciliation → steady listening → stop/reconfigure on library change。新建且启用的库自动全量初始化；只有基线事务提交成功后才挂接 watcher/event source，随后立即重新枚举/读取 cursor 做一次增量对账，消除 baseline 与监听挂接之间的变化窗口。失败时 supervisor 进入 `initialization_failed`，记录安全错误与下次有界指数退避时间，不启动监听；手动立即重试只唤醒该库。supervisor 不注册 Queue Job，也不占 downloader/transfer/scrape worker slot；同一库以 single-flight + monotonically increasing dirty generation 串行 reconciliation，不同库可同时工作。driver 负责选择本地 filesystem watcher、115 生活事件、通用 event/change cursor 或 polling，并输出统一 create/update/move/delete 事件。

`FileTreeSnapshot` 保存规范化相对路径、opaque provider identity、kind、size、mtime、可选 content signature 和 generation。首次/周期全量枚举新树并与旧树 diff；可靠增量事件直接 patch 当前树；cursor 失效、事件缺字段或顺序不可信时触发有界 reconciliation。文件树是 STRM projection 的唯一输入，不把绝对物理路径暴露给 UI。

MediaLibrary 的来源字段与 STRM 输出字段分离：`storage_id + relative_root` 永远描述用户选择的唯一文件源；仅当来源是符合 capability 的 cloud Storage 且 `strm_enabled=true` 时，`strm_local_root` 必填。它由本地目录选择器产生、经服务端绝对路径/Reparse Point/可写性检查后作为该 Library 的 managed output boundary 保存，不是第二个 Storage。local 来源以及 cloud + STRM 关闭时该字段为空。cloud mount 属于 Storage driver 的访问/传输能力，不映射成该字段，也不能隐式开启投影。

文件身份优先使用 Storage ID + normalized relative path + size + modified time；重命名/强身份策略留给后续增强。未匹配条目保留为可播放候选并显示 `未识别`。

## Initialization State Flow

```text
draft/disabled
  -> enabled
  -> initializing (automatic full baseline)
     -> initialization_failed -> retry_wait -> initializing
     -> attaching_listener
     -> catch_up_reconciliation
     -> listening
```

Disabling or deleting cancels initialization/retry/listening for that library. Server restart resumes enabled libraries from their persisted baseline state: a missing/invalid baseline re-enters automatic initialization; a valid baseline attaches the listener and runs catch-up reconciliation before reporting `listening`.
