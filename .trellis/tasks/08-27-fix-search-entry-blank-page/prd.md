# 统一搜索、订阅与 115 自动入库

## Goal

把“找片 → 看库存 → 搜资源 → 订阅缺失 → 自动下载 → 自动刮削入库”整理成一条与 MoviePilot 产品思路一致、但遵守 OhMyCine 现有安全和流水线边界的闭环；同时修复搜索白屏、TMDB ID 直接搜索不可达、Follow 把 PT 错交 115、115 运行状态误判失败，以及 115 离线监控与生活事件目录监听可能重复认领的问题。

## Background

- `server/internal/services/site_identity_search.go:148-153` 会把聚合分组的 `Items` 置为 `nil`；`server/webui/src/views/ExploreView.vue:74` 与 `server/webui/src/sites.ts:250-305` 对 `items:null` 调用 `.map()`，造成搜索页白屏。
- 手工“按 TMDB ID”直接搜索合同仍存在，但 WebUI 只凭 `mediaType + tmdbID` 就进入详情身份搜索，使旧 `/torrent-search?search_by=tmdb_id` 路径不可达。
- 详情/推荐已经具备 TMDB 身份、多语言标题资源聚合和媒体库 coverage 基础，需要把海报搜索结果统一接到同一详情闭环，并在详情清楚展示已入库/缺失状态。
- `FollowService.Defaults()` 当前独立选择全部站点、第一个 Downloader 和第一个 MediaLibrary；保存和 Worker 没有完整校验 Site、来源格式、Downloader、Storage/Connection 与 MediaLibrary 的兼容性。
- PT 适配器交付私有 `.torrent`；115 client 当前还会尝试 torrent→magnet，违反“PT 只能交给 qBittorrent/Transmission 等非网盘 BT 下载器”的来源边界。
- 115driver v1.3.5 的状态语义为 `0=等待、1=运行、2=完成、-1=失败`，当前实现错误地把 `1` 和 `-1` 都判失败。
- 115 Downloader 已经保存所属 115 Storage/Connection 和下载目录。用户确认离线下载、分享转存、115 App 手工转存应共用该目录，不新增侧栏、独立“中转目录”或手工转存默认媒体库设置。
- 现有目录扫描枚举目录直接子项，普通 115 离线也直接输出到 Downloader 目录；若原样共用，生活事件扫描可能在下载 Worker 完成前创建第二条接管任务。现有分享任务使用 `omc-*` 稳定子目录且扫描会跳过它，普通离线尚未使用相同隔离。

## Requirements

### R1. “搜索 / 直接搜索”产品入口

- 搜索页第一个标签叫“搜索”：关键词先查 TMDB 电影/电视剧海报；点海报进入与推荐海报完全相同的详情页。
- 第二个标签叫“直接搜索”：保留关键词、标题和 TMDB ID 等旧 PT/BT 资源搜索方式。
- “搜索海报”“多语言聚合搜索资源”“搜索种子资源”等静态按钮统一显示“搜索”；进行中可以显示动态状态。
- 推荐、海报搜索、相关作品和类似作品统一携带可信 identity provenance 进入详情；详情资源搜索按本地化名、中文别名、原名、英文名和其它翻译有序去重，并将同一资源聚合展示。
- 手工直接搜索选择 `search_by=tmdb_id` 时必须走旧 `/torrent-search` 合同；不能仅因表单里存在 TMDB ID 就劫持到详情身份 API。

### R2. 搜索稳定性与详情库存覆盖

- Server 的成功、空结果、失败站点分组在普通 JSON、SSE、缓存恢复中始终序列化 `items: []`，不得输出 `null`。
- WebUI 在唯一 wire/session boundary 将缺失、`null` 或非数组 `items` 归一为 `[]`；单站失败或空结果不能清空其它分组或导致主视图白屏。
- TMDB 海报详情展示当前用户可读媒体库中的覆盖状态：电影显示已入库/缺失/未知；电视剧按季/集显示已入库、已播缺失、未播或未知，并汇总已有集数与缺失集数。
- coverage 只能使用可信 catalog 事实；扫描或播出信息不完整时保守显示未知，不能误报缺失或触发下载。
- 详情页同时提供“搜索”和“订阅”；资源搜索结果仍走统一下载确认与入库流水线。

### R3. 订阅配置与自动闭环

- 电视剧详情可以按季或整剧创建订阅；界面参考 MoviePilot 的信息密度和分组方式，但不复制其代码或资源。
- 创建/编辑订阅至少可设置：订阅季、站点、Downloader、目标 MediaLibrary、检查周期、分辨率、编码、来源、发布组、包含/排除词、做种/年龄/大小限制、单次资源上限和下载优先级。
- 自动追更只搜索 coverage 明确证明“已播且缺失”的剧集；稳定排序选中资源后走正常 Download → Transfer → Import → Notify，不另建旁路。
- 订阅状态清楚展示 `active / paused / completed / blocked`、最近运行、下次运行、缺失/已认领集数和可操作错误。
- 识别、下载、整理或入库失败必须可见、可重试且不重复提交；只有最终媒体库对账完成才算入库成功。

### R4. 来源—Downloader—媒体库兼容合同

- PT 站点只允许 qBittorrent、Transmission 或未来明确支持私有 `.torrent` 的非网盘 BT Downloader。
- 115 原生离线不得接收 PT `.torrent`，不得 torrent→magnet 绕过；权威 `SiteType=bt` 的站点可直接提交 115 支持的 magnet/HTTP(S)/ed2k，返回 `.torrent` 时必须经有界 bencode 安全解析、按原始 `info` 字节计算 BTIH 后转为 magnet。未知来源 fail closed。
- 115 Downloader 只允许进入同一 115 Connection/Storage 下的 115 MediaLibrary；本地媒体库不得选择 115 原生离线，不同 115 账号不得交叉。
- Follow defaults 必须一次选择完整兼容元组“目标媒体库 → Downloader → 站点”，不得独立取三张表第一项或默认全选站点；无合法元组时保持空值、解释原因并禁用保存。
- UI 联动过滤、Create/Update 权威保存校验、Worker 搜索前校验和 Site resolve/Download submit 最终校验四层共用一个 Server 合同，全部 fail closed。
- 旧的不兼容订阅不静默迁移；运行前进入 blocked，不访问 PT 下载端点、不创建 DownloadTask；用户保存合法 revision 后恢复。

### R5. 115 新建下载统一入口

- 不新增“115 转存”侧栏或独立页面；入口保留在“下载管理 → 新建下载”。
- 选择 115 Downloader 后显示来源方式“离线下载 / 分享转存”；离线接受受支持的 magnet/HTTP(S)/ed2k，分享转存接受 115 分享链接和提取信息。
- 两种方式共用该 Downloader 已配置的“下载目录”，不再显示或保存媒体库级“自动摄取中转目录”，界面中不再使用“中转目录/自动摄取”命名。
- 显式离线/分享任务继续选择同一 115 Storage/Connection 下兼容的目标 MediaLibrary，并使用其 Profile、命名、移动/复制与通知配置。
- 分享链接与提取信息按 DownloadTask AES-GCM 加密；不得出现在 API 响应、WebSocket、日志、审计或浏览器持久化。非法或不完整链接在入队前拒绝。
- 页面与任务详情必须区分“已下载/已转存到下载目录”和“已刮削整理入库”；识别失败保留源内容并进入需要处理状态。

### R6. “自动监听生活事件”与所有权隔离

- 115 Downloader 设置增加开关“自动监听生活事件”；说明为“自动接管通过 115 App 放入该下载目录的内容，完成识别、刮削和入库”。它复用 Downloader 的下载目录，不再选择第二个目录。
- 一个 115 Downloader 与其所属 115 Storage/媒体库是一体路线。手工内容只能在该 Downloader 所属 115 Storage/Connection 内按现有分类/Profile/目标规则入库，不增加“手工转存默认媒体库”配置，也绝不路由到其它 Storage/账号。
- OMC 发起的原生离线和分享转存都必须先在下载目录下建立稳定的 `omc-<task-id>` 保留子目录，再把 provider 输出写入其中；对应 Download Worker 是唯一 owner。
- 生活事件只负责低延迟唤醒；Server 重新读取 115 目录清单作为权威事实。监听扫描必须跳过整个 `omc-*` 保留命名空间，只考虑普通直接子项，因此不会抢认领 OMC 下载任务。
- 普通直接子项必须经过稳定窗口/连续清单复核后，按 `Connection + Downloader + provider item ID` 建立数据库唯一 claim；重复事件、并发扫描、进程重启只产生一条接管任务。
- 用户手工创建 `omc-*` 子项时不自动接管，记录可诊断告警；该前缀为 OMC 保留命名空间。
- 同一 115 Connection 下启用生活事件监听的 Downloader 下载目录不得相互重叠，也不得与最终媒体库根重叠；保存时权威校验，旧冲突配置显示待修复而非自动改动真实目录。
- 手工接管任务继续走统一识别、命名、Transfer/Import、STRM/刷新/通知；只有入库对账完成后才能按既定策略处理已认领源内容。
- 生活事件漏报由有界周期扫描补偿；事件载荷不直接决定文件操作。

### R7. 115 状态、重试和订阅 claim

- 115 状态映射修正为 `0→queued`、`1→downloading`、`2→completed`、`-1→failed`；未知状态不得直接判失败，应保留非终态或返回可重试 provider response error。
- 用户显式重试已有任务时先读取原 provider task；仍在运行或已完成则复用，避免重复提交。
- DownloadTask 进入失败、取消或完成终态时同步 Follow episode claim，避免订阅永久停留在 downloading。
- 下载器已完成但识别、整理、传输、入库失败或等待处理时仍可取消整个流水线；取消先调用 provider `Cancel(taskID, false)` 删除下载器任务但保留文件，成功或 task-not-found 后停止 OhMyCine 后续工作、释放 Follow claim 并保留 cancelled 历史。provider 暂时失败时保留原本地事实，不得伪取消。
- 删除终态 DownloadTask 默认调用 provider `Cancel(taskID, false)` 后删除 OMC 本地记录；只有用户显式勾选完全删除时才传 `delete_data=true` 删除源/临时文件。provider task-not-found 幂等成功，provider/config 缺失时保留本地记录；无 provider task ID 的完全删除必须证明 OMC-owned 安全边界，否则 fail closed。
- Cancel 与 provider Submit 并发时，若 Submit 在本地取消后才返回，Worker 必须持久化迟到 provider ID 并立即 `Cancel(..., false)`；失败留下可诊断、可通过默认删除重试的事实。
- 不自动重试、删除、迁移或重新提交用户现有真实 115/BT 任务。

## Acceptance Criteria

- [ ] 搜索页显示“搜索 / 直接搜索”，指定静态按钮统一为“搜索”；推荐和 TMDB 海报搜索进入同一详情闭环。
- [ ] 推荐/详情执行多语言身份聚合；手工 TMDB ID 直接搜索命中旧 `search_by=tmdb_id` 路径。
- [ ] 空结果、部分站点失败、SSE 和旧 session 中的 `items:null` 均不再导致白屏。
- [ ] 海报详情可见电影或逐季逐集库存覆盖，未知事实不会误报为缺失或触发订阅下载。
- [ ] 订阅 UI 可配置完整策略，且只为明确已播缺失集自动搜索、下载、整理并入库。
- [ ] PT + 115、未知来源 torrent + 115、115 + local、跨 115 Connection 等非法组合在默认值、UI、保存和 Worker/提交边界均被阻断；权威 BT torrent 可安全转 magnet。
- [ ] 新 Follow defaults 不产生不兼容组合；旧不兼容订阅在访问站点/创建任务前 blocked。
- [ ] 不存在独立“115 转存”侧栏；“新建下载”选择 115 Downloader 后可切换“离线下载 / 分享转存”，共用 Downloader 下载目录。
- [ ] MediaLibrary 设置不再出现“自动摄取中转目录/绑定 115 下载器”；115 Downloader 设置显示“自动监听生活事件”开关且不要求额外目录。
- [ ] OMC 离线和分享任务输出到 `omc-*` 保留子目录，目录监听不会创建第二条接管任务。
- [ ] 用户通过 115 App 放入同一下载目录的普通直接子项会在稳定后自动接管；重复事件、并发扫描和重启均只生成一条任务，且只在所属 115 内入库。
- [ ] 分享密钥不泄露；识别失败保留源内容；只有 Transfer/Import 与媒体库对账完成才显示最终成功。
- [ ] 115 status 1 始终保持下载中，只有 -1 失败、2 完成；显式重试不会重复提交运行中任务。
- [ ] 取消会删除 provider 任务但保留文件；默认删除同样保留文件，只有显式完全删除才删除源/临时文件，provider 失败或 Submit 竞态不会留下伪成功。
- [ ] 测试覆盖搜索双入口与空数组合同、coverage、订阅配置、完整兼容矩阵、统一目录所有权隔离、生活事件幂等、legacy 配置/任务兼容、115 状态和 claim 同步。
- [ ] Server `go test ./...`、`go vet ./...`，WebUI typecheck/test/build 与 `git diff --check` 全部通过；浏览器验证不操作真实下载任务。

## Out of Scope

- 不复制或引入 MoviePilot GPL 代码、选择器、密钥、签名或凭据；仅参考产品交互和行为。
- 不在本任务实现 Transmission，只保留其能力合同。
- 不将 PT torrent 转 magnet，不扩充 115 未声明的下载能力。
- 不重做推荐算法、TMDB 元数据模型或底层 Transfer/Import 架构。
- 不为 115 新增独立侧栏、第二套监听目录、媒体库级自动摄取配置或手工默认媒体库设置。
- 不自动操作用户当前真实下载任务或 provider 数据。
- 不推送分支、不发布版本。

## Technical Notes

- 长期兼容与所有权合同位于 `.trellis/spec/backend/download-route-selection.md`；新增来源、Downloader、Storage 或目标类型必须扩充同一矩阵与测试。
- “属于自己的 115”以 Downloader 绑定 Storage 的 `connection_id` 为权威边界；分类只在该边界内选择目标，不能根据名称或前端传值跨账号。
- `omc-*` 是下载目录内的 Server-owned 保留命名空间；provider item claim 的数据库唯一约束是并发权威，生活事件和内存锁都不能替代它。
- API/事件只返回兼容判断所需的非敏感类型、Storage/Connection 身份和安全阶段，不返回 Cookie、passkey、分享密钥、provider 完整路径或来源 URL。
- Follow snapshot schema 保持兼容；旧非法快照执行时 fail closed，通过显式编辑生成新 revision。
