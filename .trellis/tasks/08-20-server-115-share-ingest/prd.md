# 115 分享转存与中转目录自动摄取

## Goal

让 115 分享链接、后续站点提交的 115 分享资源，以及用户通过 115 App 手工转存到受控中转目录的内容，共用现有 Download → 统一识别/TMDB → Transfer → MediaLibrary reconciliation 管线。115 只负责分享转存、目录事实和生活事件，不能拥有私有标题识别、分类或命名逻辑。

## Background

- 现有 `pan115_offline` 下载器支持磁力、HTTP、ED2K 离线下载，并能在生活事件唤醒后重新读取权威任务状态及完成 manifest。
- 现有 115 cloud Transfer 已支持同 Connection 的移动/复制、冲突策略、稳定 provider item ID、重试和 `dirty_generation` 交接。
- `MediaLibrary` 尚无独立的自动摄取目录；115 分享链接也不是下载来源类型。
- MoviePilot 的成熟实现会解析分享码/提取码、枚举分享根、调用 `share/receive` 转存到待整理目录，再由生活事件命中待整理路径后复用核心整理链。本项目采用同一原则，但使用持久 Job、每媒体库路由、幂等任务目录和权威目录对账。
- 云盘无 STRM 的 NFO/JPG 上传策略已确定为媒体库级可选项、默认关闭；真实 NFO/JPG 生成与上传依赖尚未实现的元数据产物/STRM worker，不在本任务制造无效开关。

## Requirements

### R1. 媒体库中转目录

1. 115 媒体库可启用“自动摄取与转存接管”，选择同一 Storage 范围内的中转目录，并绑定一个同 Connection 的已启用 `pan115_offline` 下载器。
2. 中转目录通过现有 115 目录选择器的 Storage 令牌解析，持久化安全显示路径和私有稳定 provider ID；保存时及运行时重新验证 ancestry。
3. 中转目录不能等于、位于最终媒体库根之下或包含最终媒体库根；同一 Connection 上已启用的中转目录不能相同或相互嵌套。
4. 配置记录接管任务 owner。关闭自动摄取停止创建新接管任务，但不取消已入队任务。

### R2. 115 分享来源

1. 下载创建 API/UI 增加 `115_share` 来源，仅对 `pan115_offline` 下载器显示和接受；支持常见 `115.com/s/...`、`115cdn.com/s/...` 链接与 URL/query 中的提取码。
2. 分享链接及提取码作为敏感 DownloadTask source 使用 AES-GCM 保存，不进入 Job payload、API、WebSocket、audit 或日志。
3. 分享提交选择目标媒体库；`media_library_id=0` 按 `sort_order,id` 选择第一个启用了自动摄取且与下载器同 Connection 的可用 115 媒体库。
4. 每个系统分享任务在所选媒体库中转目录下创建/复用稳定 `omc-<DownloadTask ID>` 任务目录，再调用 115 `share/snap` 枚举受限顶层项并调用 `share/receive`。任务目录 ID 是完成 manifest 的权威根。
5. 成功响应、歧义响应或进程重启都先按稳定任务目录重新对账，避免重复转存；分享失效、密码错误、空分享、超大顶层清单和风险控制返回稳定安全错误。

### R3. 生活事件自动接管

1. 115 生活事件仍只做低延迟唤醒。收到事件后按 Connection 合并并延迟到安静窗口，再列举每个启用中转目录的直接子项；事件本身不能直接证明转存完成。
2. 对不属于 `omc-` 系统任务、且未被已有摄取记录占用的直接子项，创建内部 adopted DownloadTask/Job。私有来源只包含 provider item ID；任务 owner、目标媒体库、Profile revision、分类/命名、Transfer/Conflict 策略全部在创建时快照。
3. adopted worker 重新验证子项仍在中转根内，读取 bounded manifest，然后走现有可信主媒体筛选、统一 TMDB/Profile 识别和 Transfer enqueue；不识别时保持源文件不动并以 `transfer_media_unrecognized` 失败。
4. 重复/重放生活事件、Server 重启和定时 reconciliation 不得创建重复 adopted 任务。系统分享的 `omc-` 目录不得被生活事件再次接管。
5. 周期增量/全量媒体库 reconciliation 继续作为最终一致性补漏；自动摄取扫描不占用媒体库 supervisor，不把 watcher 本身放进持久队列。

### R4. Provider 与下载契约

1. `cloud` 增加窄的 115 分享转存能力，不把分享 API 细节散入 service；115 adapter 负责受限解析/枚举/接收、限速、超时和 provider 错误映射。
2. downloader source 增加私有 adopted-provider-item 类型，只能由 Server 内部创建，HTTP API必须拒绝客户端提交。
3. `SubmitRequest` 可携带私有 provider staging directory override；115 client 在每次提交/接管时验证该目录仍位于下载器 Storage 根内。
4. 分享和 adopted item 都不支持暂停、恢复或做种。取消遵循现有强确认语义，回收任务中转根及其数据；provider task not found 视为幂等成功。

### R5. API、UI 与可观测性

1. 媒体库设置仅在 115 Storage 下显示自动摄取配置、下载器选择和中转目录浏览；编辑时显示已保存的 Storage 相对路径。
2. 下载页面为 115 下载器增加“115 分享链接”来源入口，提交成功后正常出现在下载任务和媒体整理页面。
3. 新增集中日志 operation `pan115_share_ingest` / `115分享摄取`。分享提交、接管扫描和 adopted 创建记录开始与唯一终态，只包含任务/媒体库/Connection ID、安全计数、耗时和稳定错误码。
4. API、日志、audit 和 UI 不暴露分享链接、提取码、provider item ID、provider 完整路径、Cookie 或原始上游响应。

## Acceptance Criteria

- [ ] 管理员可为 115 媒体库选择独立中转目录和同 Connection 下载器；本地库、跨 Connection、最终根重叠及中转根重叠均被拒绝。
- [ ] 下载页面可提交带提取码的 115 分享链接；链接密文落库，Job payload/DTO/log/audit 不含明文。
- [ ] 分享接收到稳定 `omc-<task>` 目录，完成后由现有统一识别和 115 cloud Transfer 整理到目标媒体库。
- [ ] 分享 API 成功但本地状态保存失败后重试只接管稳定任务目录，不再次调用分享接收。
- [ ] 用户通过 115 App 把目录放进中转根后，生活事件只唤醒权威目录扫描，并自动创建一条 adopted 下载任务；重复事件和重启不会重复创建。
- [ ] 广告/样片过滤、TMDB 未识别禁止写目标目录、分类/命名和冲突处理与 qBittorrent/115 离线下载使用同一 verifier/Transfer。
- [ ] 系统管理的 `omc-` 任务目录不会被自动接管器重复创建任务。
- [ ] 生活事件延迟或丢失时，定时中转目录 sweep 能补齐；所有中转目录独立处理，不占媒体库 watcher 队列。
- [ ] Go/Web UI focused tests、全量 Go test/vet/build、Web UI test/typecheck/lint/build、Windows 隔离脚本及 `git diff --check` 通过，测试后无 Server 进程残留。

## Out of Scope

- 115 分享资源站点搜索/订阅抓取；后续只需提交标准 `115_share` source。
- 直接从分享链接生成分享型 STRM，而不转存到用户网盘。
- 真实 NFO/JPG/海报生成与上传。云盘无 STRM 的“数据库 only / 上传旁挂文件”策略将和 STRM/NFO 产物 worker 在 `.trellis/tasks/08-12-server-library-strm-proxy` 中实现，默认仍为 database-only。
- 非 115 网盘的分享转存。

## Technical Notes

- 115 endpoints: `GET https://115cdn.com/webapi/share/snap` 与 `POST https://webapi.115.com/share/receive`。请求必须复用受控 SDK client、115 Browser UA、统一限速/风险熔断，响应体不得记录。
- provider task identity 使用带类型前缀的稳定中转根 ID；`Get`/`Manifest` 按根目录事实判断，不把 `share_code` 或 `receive_code` 放入 identity。
- 自动接管可由 `MediaLibraryService.ProviderEventsChanged` 合并唤醒后调用 DownloadService 的内部 adopted enqueue 边界；二者通过接口组合，避免 provider event service 反向依赖 handler。
