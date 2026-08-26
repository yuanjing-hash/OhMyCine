# 115 云端自动整理入库

## Goal

打通 115 原生离线下载后的最后一段自动化链路：下载完成并取得可信文件清单后，按照下载时已快照的目标 MediaLibrary、分类结果、命名模板、转移方式和冲突策略，在同一 115 Connection 内完成云端建目录、移动或复制、改名，并让目标媒体库及时对账。用户不需要再手工整理网盘目录，也不新增重复的“下载规则”配置层。

## Background

- 当前 115 Connection、Storage、MediaLibrary 子目录、bulk scan、生活事件、原生离线下载和完成 manifest 已实现。
- DownloadTask 已快照目标 MediaLibrary、Storage、Profile、`move|copy`、`ask|overwrite|skip|rename` 和电影/剧集命名模板；TransferTask/Job、等待用户选择、失败重试、历史记录和媒体整理 UI 已存在。
- 当前 `DownloadService.snapshotDownloadTarget` 只接受 local Storage，`TransferWorker` 也只执行本地文件操作，因此 115 离线任务完成后无法进入 115 目标媒体库。
- 115 SDK 已提供 `Mkdir`、`Move`、`Copy`、`Rename` 和进入回收站的 `Delete`，但没有幂等请求键；服务端必须通过稳定 file ID、目标目录检查和持久化进度实现可恢复执行。

## Requirements

### R1. 目标选择与不可变快照

1. 下载创建时，已启用且可用的 115 MediaLibrary 可以作为显式目标，也可以参与按 `sort_order,id` 的自动目标选择。
2. 115 目标快照必须保存目标 Storage 类型、稳定 `provider_root_id` 和 Connection 身份；后续修改媒体库显示路径、模板或排序不得重定向在途任务。
3. 115 原生离线下载的源 Storage 与目标 Storage 必须属于同一 115 Connection。不同账号、115 与本地、115 与其他 provider 的传输在本任务中明确拒绝，不伪报成功。
4. 云端 MediaLibrary 只允许 `move|copy`；`symlink` 在 API 和 UI 中不可选。

### R2. 私有 provider manifest

1. 115 完成 manifest 的每个文件必须携带稳定 provider item ID、parent ID 和可验证的大小/校验摘要；这些字段只进入私有 TransferTask manifest，不进入 API、Job payload/checkpoint、审计、WebSocket 或普通日志。
2. manifest 相对路径继续用于分类和命名，但任何写操作以稳定 item ID 为准，不用可变路径重新猜源文件。
3. 执行前重新验证源 item 仍位于下载器所选 Storage 根内；越界、消失、类型变化或身份歧义均安全失败。

### R3. Provider-neutral 云端写入

1. `pkg/cloud` 增加最小的可选 mutation contract：创建目录、移动、复制、改名和送入回收站；只对真正实现且探测可用的 provider 声明 capability。
2. 115 mutation 使用独立的保守限速 lane，复用现有每 Connection 并发槽、风控退避、短路器、超时和安全错误映射。
3. 目录按命名模板逐级确保存在。只复用同名且确认为目录的子项；同名文件或多个同名目录视为冲突，不猜测。
4. 所有路径片段继续使用现有模板清洗和长度限制，目标 provider root 是硬边界；不得写到目标 MediaLibrary 之外。

### R4. 转移与冲突语义

1. TransferService 继续是唯一写入媒体库的业务入口，并按 Storage 类型选择 local 或 cloud executor；扫描器、生活事件和 catalog 保持只读。
2. `move` 在目标目录建立后移动源 item，再按计划改名；`copy` 保留源 item，识别并验证新副本后改名。每个文件完成后持久化稳定结果，进程重启不得重复制造副本。
3. `ask` 继续创建 ActionRequest 并释放 worker slot；用户可选择 `overwrite|skip|rename`，不会阻塞同队列后续任务。
4. `overwrite` 只把已确认位于目标 MediaLibrary 根内的冲突项送入 115 回收站，然后写入新项；不调用永久删除。若回收站操作失败，源文件保持不动并允许重试。
5. `skip` 保留目标和源；`rename` 为同一媒体组选择一个稳定的 ` (n)` 后缀，并保证视频及字幕/海报使用同一组名。
6. 对已经完成的 move，重试通过 source item 的当前 parent/name 和目标 item 身份确认成功；对 copy 结果无法唯一确认时停止并标为需要处理，不自动删除疑似重复项。

### R5. 完成、对账与可观测性

1. 全部操作成功或按策略跳过后，TransferTask 才进入 completed；逐文件进度和脱敏相对结果继续显示在媒体整理详情。
2. 成功后递增目标 MediaLibrary `dirty_generation`，并通过现有 supervisor/生活事件尽快对账；周期 reconciliation 继续补漏。
3. 115 原生离线下载不进入做种管理。`move` 整理成功后只清理离线任务记录而不再次删除已移动文件；`copy` 保留源文件，不自动清理源数据。
4. 新增独立的“115云端整理”日志 operation，记录任务、Connection、Storage、Library、动作类型、数量、耗时和稳定错误码；不得记录 Cookie、pickcode、临时 URL、provider 原始响应或完整云端路径。
5. 云端移动、复制、改名和覆盖回收均写入脱敏审计事件。

### R6. 下载包级媒体接管

1. qBittorrent、Transmission 和原生网盘离线下载共用同一套完成清单接管逻辑；provider adapter 不得自行决定哪些视频可以入库。
2. 完成清单先选择可信主媒体，再交给 TransferService。电影包只接管主片；剧集包保留体积合理且带可信季集号的正片；几百 KB 的广告、水印、样片等小视频不得成为独立媒体。
3. `srt|ssa|ass|jpg` 仅在与已接管主媒体同目录且文件 stem/语言后缀可关联时跟随；无关附件不得进入私有 Transfer manifest。
4. TMDB 未匹配、认证/网络失败、低置信度、分类结果不完整或找不到可信主媒体时，自动入库必须 fail closed：源文件留在下载目录，不创建“未分类”目标目录，不执行任何本地或 provider mutation，并显示稳定的“未识别，未自动入库”失败状态供用户重试。
5. TransferService 和 local/cloud planner 必须再次验证任务级 `completed_verified` 元数据快照，不允许回退成“逐个视频自行解析并分别建目录”。

### R7. Provider-neutral 识别与命名 Profile

1. qBittorrent、Transmission、本地目录、115 离线下载及未来 provider 必须共用同一套识别预处理、TMDB 匹配、分类、命名和安全清单选择；provider adapter 只负责最后的文件操作。
2. 每个 MediaClassificationProfile 除分类条件外，还拥有有序的正则识别预处理规则，以及电影/剧集各自的目录和文件名模板；复制 Profile 必须完整复制这些设置。
3. 识别预处理规则按顺序执行，可启停并限定 `all|movie|tv`，支持正则删除或替换；无效或过大的规则在保存时拒绝，不在运行时静默忽略。
4. 下载创建时快照 Profile revision、分类 JSON、识别规则和命名模板；后续 Profile 修改不得改变在途任务。
5. 媒体库继续拥有目标目录、转移方式和冲突策略，不再成为新任务的命名模板事实源；旧媒体库配置保持可读兼容。
6. 完整真实发行名、混合点号/空格/连字符和网站前缀必须进入回归测试，不能以简化标题样例替代。
7. v14-v23 媒体库已经保存的自定义命名模板在升级时必须迁入 Profile；同一旧 Profile 被不同模板组合引用时需拆分为独立 Profile，不得静默回退默认模板。
8. 公共识别候选同时考虑主文件名、有意义的父目录和 provider 包名；光盘结构中的 `BDMV/STREAM/00000.m2ts` 不得遮蔽外层真实发行名。

## Acceptance Criteria

- [x] 115 离线任务可以选择同 Connection 的 115 MediaLibrary；下载完成后自动创建目标目录并按电影/剧集模板整理视频和 `srt|ssa|ass|jpg`。
- [x] `move` 后源 item 不再位于离线目录，`copy` 后源 item 保留；115 目标不提供 `symlink`。
- [x] `ask` 释放 worker slot；`overwrite|skip|rename` 与本地语义一致，overwrite 只进入 115 回收站。
- [x] 任务在 mkdir、move/copy、rename 或进度持久化前后重启，重试可收敛且不会重复移动或静默制造副本；无法证明唯一结果时安全失败。
- [x] 源或目标 Storage 换 Connection、目标根移动、item 越界、同名目录歧义、限流和鉴权过期均返回稳定脱敏错误，不修改边界外文件。
- [x] 整理成功后 TransferTask 进入历史、目标媒体库被标脏并完成增量对账，115 任务不进入做种管理。
- [x] API、日志、审计、Job payload/checkpoint 和 UI 均不泄露 Cookie、pickcode、临时 URL、provider item ID 或完整云端路径。
- [x] fake provider 覆盖 move/copy、四种冲突策略、伴随文件、重启幂等、部分失败、越界和风控重试；115 adapter 覆盖 SDK 请求映射和错误分类。
- [x] 含一部 28.5 GiB 主片和两个几百 KiB 广告视频的完成清单只接管主片及关联字幕；标题候选能从 `Seven Samurai CC MA 2 0 SONYHD` 提取 `Seven Samurai`。
- [x] 未识别下载包不会创建 TransferTask、目标目录或 provider mutation；本地和所有云端下载器遵循同一入口。
- [x] 规则管理可为每个 Profile 编辑有序识别预处理规则和电影/剧集命名模板，复制后保持独立副本。
- [x] 下载任务快照并统一应用 Profile 识别/命名配置，真实七武士发行名以 `Seven Samurai` + `1954` 查询 TMDB。
- [x] 失败重试先清空旧计划投影；再次未识别时不显示旧广告文件，识别成功时仅主片进入计划。
- [x] v24 保留旧媒体库自定义命名模板；同一旧 Profile 的不同模板组合迁为独立 Profile 并重新绑定。
- [x] qBittorrent、115 与任意未来 `ManifestClient` 使用同一完成识别入口；本地与云端执行器使用同一筛选后命名计划，provider adapter 不做私有识别。

## Out of Scope

- 跨 115 账号、115 与本地、115 与其他网盘之间的数据传输或上传。
- 用户主动删除云端文件、永久删除、清空回收站和批量文件管理。
- STRM 投影、signed 302、Emby/Jellyfin/Player 通知；这些在整理入库闭环后单独实现。
- 对既有网盘文件进行手动整理；手动整理仍属于未来文件管理页面。
- 真实账号写入测试进入默认 CI；live smoke 必须显式配置隔离测试目录和环境凭据。

## Technical Notes

- 复用现有 `TransferTask`，通过加法迁移补充必要的 provider 快照/逐项执行状态，不建立第二套 cloud transfer 队列。
- 参考 `github.com/SheltonZhu/115driver` 的操作接口，但风控、幂等、边界、日志和任务模型由 OhMyCine 自己实现。
