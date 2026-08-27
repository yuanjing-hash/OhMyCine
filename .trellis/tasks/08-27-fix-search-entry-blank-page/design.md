# 技术设计

## 1. 总体边界

本任务不新建旁路，统一复用既有流水线：

```text
TMDB 海报/推荐详情
  → 多语言资源搜索或电视剧订阅
  → 来源与 Downloader/Storage 兼容校验
  → DownloadTask
  → 清单复核与统一识别
  → Transfer / Import
  → STRM / Emby/Jellyfin / Player Notify
```

HTTP handler 保持薄层；搜索结果归一化、下载路线兼容、115 目录所有权、Follow runtime validation 均落在 Server service/domain helper。WebUI 只投影状态和提供即时过滤，不能成为安全权威。

## 2. 搜索和详情闭环

### 2.1 两种搜索

- “搜索”调用 TMDB media search，只返回电影/电视剧身份海报；海报导航携带显式 `identity provenance`。
- 推荐、搜索海报、相关/类似作品都进入同一 media detail route。
- 详情资源搜索根据可信 TMDB identity 建立有界、多语言、去重查询集合，再复用现有逐站并发/SSE、opaque result claim 和下载确认。
- “直接搜索”保留旧 keyword/title/tmdb_id 表单与 `/torrent-search*`。手工 TMDB ID 是查询参数，不自动获得详情 identity provenance。

### 2.2 `items` wire contract

- Go 创建、复制和完成 `SiteSearchGroup` 时使用非 nil slice；普通 JSON、SSE final/group、缓存/session 数据均输出 `items: []`。
- TypeScript 在 `upsertPTGroup` 或同等唯一 wire boundary 使用 `Array.isArray` 归一化；组件与 session restore 只消费归一 DTO。
- 单站失败只更新该 group 的状态/错误，不替换其它 group 或根视图。

### 2.3 coverage

- 详情页复用 `MediaCoverageService`，按 actor 可读 MediaLibrary 汇总可信 catalog。
- 电影投影 `present|missing|unknown`；电视剧投影逐季逐集 `present|missing|future|unknown`，并计算 present/missing 汇总。
- `missing` 仅代表已播且 catalog 明确缺失；其余不完整事实为 `unknown`。Follow 与详情 UI 消费同一合同。

## 3. 订阅配置与执行

- 详情页订阅表单采用分组抽屉/对话框：范围、下载路线、质量过滤、资源限制、调度和高级规则。
- 选择顺序为 `目标 MediaLibrary → Downloader → Site`；每次上游变化重新计算合法下游并清除失效草稿。
- Server defaults 从已启用记录中找第一个完整兼容元组；找不到时返回空字段和 machine-readable unavailable reason。
- 保存后继续使用版本化 Follow snapshot；Create/Update 重载权威对象并校验，Worker 在搜索前和下载交接前再次校验。
- Worker 只对 coverage 明确的已播缺集建立 episode claim；选择资源后调用既有 SiteService opaque claim 和 DownloadService，不直接操作 provider 或文件。

## 4. 共享下载路线兼容合同

兼容 helper 输入权威事实：

```text
Site.type/capabilities
resolved SourceKind
Downloader.type/storage_id/capabilities
Downloader Storage.connection_id
target MediaLibrary Storage.type/connection_id
```

核心矩阵：

| 来源 | Downloader | 目标 | 结果 |
| --- | --- | --- | --- |
| PT / private torrent | qBittorrent 或未来 Transmission | local/其支持目标 | 允许 |
| PT / torrent | 115 native offline | 任意 | 拒绝，不转换 |
| SiteType=bt → torrent | 115 native offline | 同 Connection 115 | 安全解析原始 info 后转 BTIH magnet |
| SiteType=bt → magnet/HTTP(S)/ed2k | 115 native offline | 同 Connection 115 | 允许 |
| 115 native offline | local | local | 拒绝 |
| 115 native offline | 另一 Connection 115 | 115 | 拒绝 |
| 115 share/provider item | 选中的 115 Downloader | 同 Connection 115 | 允许 |

配置阶段以 SiteType 粗校验；resolve 后以真实 SourceKind 最终校验。Create/Update、Follow Worker、Site handoff 与 Download submit 共用稳定错误码与原因。

取消是独立的流水线控制：`POST /downloads/:id/cancel` 可覆盖下载、识别、等待处理、Transfer/Import 与重试阶段，先调用 provider `Cancel(taskID, false)` 删除下载器任务并保留文件，成功或 task-not-found 后才终止 OhMyCine jobs、释放 Follow claim 并写 cancelled 历史；provider 失败保留原本地事实。`DELETE /downloads/:id` 默认同样以 `delete_data=false` 清理 provider 任务后删除本地记录，只有显式 `delete_data=true` 才删除源/临时文件。Submit 竞态返回的迟到 provider ID 会被持久化并立即以 `false` 清理，失败事实保留供重试。

## 5. 115 Downloader 一体模型

### 5.1 配置归属

```text
115 Downloader
  = 115 Storage / Connection
  + 下载目录
  + 自动监听生活事件开关
```

- 下载目录同时承载 OMC 离线、OMC 分享转存和 115 App 手工转存。
- MediaLibrary 不再拥有 `ingest_downloader_id`、`ingest_provider_root_id`、`ingest_relative_root` 的新配置入口；WebUI 删除“自动摄取/中转目录”区域。
- 显式新建任务仍选目标 MediaLibrary；后端限制为 Downloader 所属 Connection 下的兼容 115 库。
- App 手工转存不增加默认库字段。扫描任务固定在 Downloader 所属 Storage/Connection 内，复用分类/Profile/目标规则决定同一 115 内的媒体归属；若现有事实无法唯一决定具体分类，进入 needs-action，不能跨 Storage 猜测。

### 5.2 新建下载 UX

- 先选 Downloader；选择 `pan115_offline` 后显示“离线下载 / 分享转存”。
- 离线表单接受 URL/magnet/ed2k；分享表单接受 115 分享链接/提取信息。
- 同一表单选择兼容媒体库、显示下载目录的安全显示名与最终入库摘要；不显示第二个目录选择器。
- 分享来源只进入 Server 加密字段，提交后清空输入；进度统一进入下载任务详情。

## 6. 同目录冲突解决

### 6.1 所有权命名空间

所有 OMC 主动创建的 115 工作都使用固定任务子目录：

```text
Downloader 下载目录/
  omc-<download-task-id>/      # OMC 离线或分享；Download Worker 独占
  用户手工转存目录或文件       # 生活事件监听候选
```

- 创建 DownloadTask 后先通过 MutationDriver 幂等创建 `omc-<task-id>`，把该 provider directory ID 冻结进任务快照，再向 `SubmitOffline`/`ReceiveShare` 提交。
- 任务重试先复核该固定目录和原 provider task；不能创建第二个目录或重复提交。
- 生活事件扫描永远跳过名称以 `omc-` 开头的直接子项和其后代，因此下载状态监控与目录接管处理互斥集合。

### 6.2 手工转存接管

- Connection 生活事件同时唤醒相关 Downloader worker 和启用监听的目录 supervisor；事件只作为 wake signal。
- supervisor 权威 `List` 下载目录直接子项，过滤 `omc-*`，对候选记录 size/item identity/mtime 或可获得的目录清单摘要。
- 候选在静默窗口内无新事件且连续两次清单一致后才可 claim，避免转存尚未完成时抢跑。
- claim key 使用 `pan115:<connection-id>:<downloader-id>:<provider-item-id>` 的稳定摘要，并由数据库部分唯一索引作为并发权威。
- claim 成功创建普通 durable DownloadTask，来源为 `provider_ingest`，引用该 Downloader 和冻结 provider item；后续复用 manifest、识别、Transfer 和 Notify。
- claim 已存在时扫描幂等跳过；任务失败/needs-action 不释放 identity 以免重复建单，显式操作在原任务上重试。

### 6.3 路径安全

- 保存/启用监听时验证 Downloader 目录位于其 Storage root 内，与同 Connection 其它启用监听目录及最终 MediaLibrary 根不重叠。
- `omc-*` 为保留前缀；用户同名内容跳过并写安全告警，不自动移动或删除。
- 生活事件漏报由周期 sweep 补偿；分页、最大子项数、parent identity 和 provider root 边界继续 fail closed。

## 7. 状态、终态与清理

- pan115 adapter 映射 `0 queued / 1 downloading / 2 completed / -1 failed`；未知状态保留可重试，不伪造失败。
- provider `completed` 后先取清单并验证仍位于冻结 `omc-*` 或手工 provider item 边界，再进入识别和 Transfer。
- DownloadTask 的下载阶段完成不等于入库完成；UI 通过 Download/Transfer/Import 阶段展示真实状态。
- Transfer/Import 对账成功后才执行既定源清理；识别失败或安全校验失败保留源内容并进入 needs-action。
- DownloadTask 终态同步 Follow episode claim；重试先 reconcile 原 provider task/output。

## 8. 兼容与迁移

- 数据模型新增 Downloader 级生活事件监听配置和必要的稳定候选/claim 状态；沿用显式版本迁移。
- 旧 MediaLibrary intake 字段保留读取兼容，不再由新 UI/API 写入。已有 intake DownloadTask 按冻结快照完成或显式重试，不迁移真实 provider 工作。
- 旧配置若目录重叠或无法归属，不自动启用新监听，标记“需要修复”；不移动、删除或重新提交任何 115 内容。
- Follow snapshot schema 保持兼容；旧非法路线执行时 blocked，用户编辑生成新 revision。

## 9. 安全与可观测性

- 分享 URL、提取信息、PT passkey、Cookie、provider ID/完整路径不进入公开 DTO、WebSocket、日志或审计。
- 日志仅记录 task ID、非敏感 Downloader/Library ID、阶段、稳定错误码和计数。
- 权限仍由 API 独立校验：创建任务要求 `downloads.create`，读取候选媒体库要求相应 read 权限；导航可见性不代替授权。
- 浏览器验收只创建隔离测试数据或使用 mock；不操作用户真实 provider task。

## 10. 回滚边界

实现分为搜索、兼容合同/Follow、115 统一目录与监听、115 状态四个可独立验证单元。任何回滚都不得放宽 PT→115 和跨 Connection 最终提交校验；涉及 schema 的回滚保留新增列/索引，避免破坏已产生任务事实。
