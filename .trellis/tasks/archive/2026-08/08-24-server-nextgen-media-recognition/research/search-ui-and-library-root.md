# Research: 搜索结果交互、媒体库目录更新与识别一致性

- Query: 核对用户提出的站点搜索分页/渠道导航/卡片/多条件筛选，以及媒体库目录保存后显示为 `/`、修改目录后任务仍写旧位置、搜索预识别失败但下载后识别成功、Player 识别器同步等问题；给出可直接实施且不会误改已开始任务目标的方案。
- Scope: mixed（OhMyCine Server/WebUI/Player 内部实现 + MoviePilot Frontend v3 的公开交互参考）
- Date: 2026-08-25

## Findings

### 1. Files Found

| 文件 | 作用 |
| --- | --- |
| `server/webui/src/views/ExploreView.vue` | 当前种子搜索、按站点分组、追加下一页、快速识别、人工识别和创建下载任务界面。 |
| `server/webui/src/sites.ts` | 搜索结果/站点分组/识别 DTO、查询参数、分组追加与 sessionStorage 恢复逻辑。 |
| `server/internal/services/site.go` | 多站并行搜索、不透明结果 claim、快速识别、人工 TMDB 纠错及下载入队衔接。 |
| `server/pkg/site` | 站点 Adapter、分页 Page 和原始种子结果的统一边界。 |
| `server/webui/src/media-libraries.ts` | 媒体库编辑 draft 与 API payload 映射；当前把 `relative_root` 直接当成可见路径。 |
| `server/webui/src/views/MediaLibrariesView.vue` | 目录选择 token、媒体库保存和来源根展示。 |
| `server/internal/handlers/media_libraries.go` | 本地/115 目录 token 解析及修改时的目录身份保护。 |
| `server/internal/services/directory_browser.go` | 把本地绝对选择限制在 Storage 根内，并转换成 provider-relative 根。 |
| `server/internal/services/provider_directory.go` | 把 115 目录令牌解析成稳定 provider ID 与相对显示路径。 |
| `server/internal/services/media_library.go` | 媒体库更新、来源身份变化判断、旧扫描索引清理和重新首次扫描。 |
| `server/internal/services/download.go` | 创建任务时冻结目标媒体库、Storage、相对根、Profile 与命名模板；下载完成后用完整 manifest 识别。 |
| `server/internal/models/models.go` | `DownloadTask` 中不可变目标快照与识别 override 字段。 |
| `server/internal/services/transfer.go` | 本地入库用 `TargetStorageRoot + TargetRelativeRoot + 分类/命名模板` 规划最终路径。 |
| `server/internal/services/transfer_cloud.go` | 115 入库用冻结的 `TargetProviderRootID` 规划云端目标。 |
| `player/src/services/scraper/recognition.ts` | Player 独立识别候选/排名实现，当前实现版本 `player-nextgen-v2`。 |
| `player/scripts/verify-nextgen-recognition.ts` | Player 已读取 Server provider-neutral corpus，但目前只适配部分事实。 |
| `.trellis/spec/backend/pt-discovery.md` | PT/BT 搜索、不透明 claim、快速/人工识别和下载衔接的执行契约。 |
| `.trellis/spec/backend/media-library-foundation.md` | Storage-relative 媒体库身份和扫描生命周期规范。 |
| `.trellis/spec/backend/transfer-organization.md` | 下载目标快照、分类命名和写入安全边界。 |
| `.trellis/spec/frontend/server-admin-ui.md` | Server 管理台的传统浅/深色、紧凑信息层级和无玻璃拟态约束。 |

### 2. 搜索页当前实现与用户目标的差距

#### 2.1 当前是“所有站点纵向铺开 + 每站追加页”

- `ExploreView.vue:58-64` 的 JSON 搜索按返回 group 更新站点结果。
- `ExploreView.vue:79-98` 的 SSE 会按完成顺序把各站结果渐进插入；站点响应快慢会影响可见顺序。
- `ExploreView.vue:112-115` 的“下一页”传入 `append=true`。
- `sites.ts:208-216` 会把新页 items 追加到旧页 items，因此 UI 不是严格单页。
- `ExploreView.vue:265-289` 将所有站点 group 纵向平铺，只在每个 group 底部提供“加载此站下一页”，没有上一页和横向渠道导航。

用户提出的更合适信息架构是：

```text
搜索框
  ↓
站点类型/质量等多条件筛选 + 排序
  ↓
横向渠道：全部 | PTTime | SewerPT | Nyaa | ...
  ↓
当前渠道的一页卡片
  ↓
上一页 | 第 N 页 | 下一页
```

推荐保留 Server 当前“每站独立页码”的事实，不把不同站点强行伪装成一个全局游标：

- 每个站点保存自己的 `page/has_next/items/status`。
- 切换顶部站点 tab 只切可见 group，不丢其他站点结果。
- `上一页` 使用 `page > 1`，`下一页` 使用 `has_next`；翻页替换当前站当前页，不再追加。
- `全部` tab 可展示当前已取得的各站当前页聚合结果，但必须明确这是“各站当前页”，不能声称为跨站全局第 N 页。
- SSE group 必须按站点配置优先级/稳定 ID 排序，而不是按网络返回先后排列；可在 DTO 中加入稳定 `site_order`，或在发起搜索前读取已启用站点顺序。

#### 2.2 筛选、排序与卡片字段已有基础，但标准化不足

当前 `SiteSearchResult` 已有标题、描述、体积、时间、做种/下载/完成、优惠、单个 quality 和 tags（`site.go:129-141`），页面也显示这些字段（`ExploreView.vue:270-285`）。缺口是：

- 没有稳定的分辨率、来源、编码、HDR、发布组、季集等结构化字段；这些只有用户点击识别后才由解析器返回。
- 海报只有 TMDB 识别成功后才存在，不能在不调用 TMDB 的情况下为每个原始 torrent 保证真实海报。
- 站点类型 `pt|bt` 位于 group，不在 item；聚合视图筛选时必须携带所属 group 上下文。

建议在站点搜索结果生成阶段对标题执行一次纯本地、无网络的 `mediarecognition.Parse`，将安全且有界的 `specifications/episodes/release_group` 投影到 result DTO。这样：

- 分辨率、来源、视频编码、HDR、发布组、季集可立即作为卡片标签和筛选项；
- 不会为几十/几百条结果触发 N 倍 TMDB 请求；
- “检测”仍负责 TMDB 身份与海报；未检测卡片使用统一海报占位图，检测后替换为同源代理海报。

推荐首批筛选：

- 站点类型：PT、BT（多选）；
- 具体站点（多选）；
- 分辨率、来源、视频编码、HDR/杜比视界、优惠状态、发布组（多选）；
- 季/集、最小做种数、体积范围（可选高级筛选）。

组合语义应固定为“不同字段 AND，同一字段多个值 OR”。默认排序为 `seeders DESC`，空做种数永远排在有值之后；稳定 tie-breaker 建议为 `completed DESC → published_at DESC → site_order ASC → title ASC`。可选排序至少包含做种、发布时间、体积、站点。

注意：只在浏览器对“当前已取回的一页”排序/筛选，不等于全站全局排序。若产品文案要声称全局按做种排序，必须扩展 Adapter 查询参数并由每个上游真正支持；首版应明确为当前结果集排序。

#### 2.3 三个动作已有后端基础，但名称与证据层级需要澄清

现有页面已经有三个动作（`ExploreView.vue:285`）：识别、手动识别、选择下载器并入队。现有安全链也基本正确：

- 快速识别只解析 actor-bound claim 的标题/辅助描述，不消费 claim，也不下载 torrent（`site.go:697-739`）。
- 人工识别从 TMDB 候选中选择后，Server 用 `GetByID` 重新验证并绑定到 claim（`site.go:871-915`）。
- 创建下载任务时，如果 claim 有人工身份，就把它复制到 `DownloadTask.RecognitionOverride*`（`site.go:1056-1060`）；下载 Worker 会再次 `GetByID` 验证（`download.go:1504-1545`）。因此“手动检测结果用于后续入库”在身份层已经成立，不能退化为仅改变浏览器显示。

推荐按钮与语义：

- `检测`：非消费、非强制的快速预识别；显示“标题预识别”或“清单识别”证据等级。
- `手动检测`：用户显式选择 TMDB 身份；成功后显示“已人工确认”，后续下载必须沿用并再次验证该身份。
- `入库`：打开下载器/媒体库选项。若没有人工 override，可先调用快速检测用于即时反馈，但不得把低信息量的标题预识别强行固化成最终身份；最终仍由完整 manifest 识别。若已有人工 override，必须沿用。

按钮可以按用户要求简写，但弹窗/卡片需要说明“入库 = 下载 → 完整清单识别 → 分类命名 → 转移”，避免把搜索页的预识别误认为最终刮削。

### 3. 为什么搜索页没识别出来，下载完成后却正确入库

这是当前两次识别输入事实不同导致的合理结果，但 UI 把它们都叫“识别”，看起来像算法随机变化：

- 搜索页快速识别只传 `PackageName=claim.Title`、一个有界 subtitle 和弱媒体类型提示（`site.go:708-739`）。契约还明确禁止该入口下载 torrent（`.trellis/spec/backend/pt-discovery.md` 的 Quick recognition 条款）。
- 下载后识别会传 `manifest.Name`、每个文件的 provider-relative path 和 size、既有类型/年份提示、用户 Profile 的内置词包与自定义识别词（`download.go:1479-1556`）。
- 完整文件集合能提供剧集序列、父目录、单集文件名、光盘结构、真实主视频等强证据，因此标题预识别失败、manifest 识别成功并不矛盾。

改进方案不是强行让两次结果永远相等，而是显示证据等级并尽可能提前获得事实：

1. `title_preview`：只有站点标题/描述，快速且不接触下载源。
2. `torrent_manifest`：站点允许安全取得 `.torrent` 时，只解析 bencode 文件清单，不提交下载；识别明显更准。
3. `downloader_manifest`：磁力元数据或下载完成后由下载器提供的权威清单，作为最终自动决定输入。

对仅提供 magnet 的站点，DHT 元数据在下载器接管前可能不存在，因此搜索页必须允许“预识别未命中、入库后重新判定”。自动预识别不得成为不可撤销 override；人工选择才是 override。

### 4. 媒体库来源根保存后从绝对路径变成 `/` 的根因

截图中的两种值表达的是同一个位置：

- Storage 物理根为 `D:\Downloads\115\媒体`。
- 用户选择的媒体库目录正好是这个 Storage 根。
- `ResolveStorageRelativeSelection` 会把 Storage 根转换为相对根 `/`（`directory_browser.go:296-318`）。这是正确的持久化形式，保证跨 Windows/Linux 和 Storage 根迁移时不把绝对路径写进媒体库身份。
- 保存后 `draftFromLibrary` 把 `library.relative_root` 直接赋给 `source_path`（`media-libraries.ts:28-32`），所以原本选择器显示的绝对路径被 UI 重新显示成 `/`。

因此这是确定的**展示 bug**，不是数据库把目录改丢。修复应保持后端存储 `/`，但给管理员显示可理解的完整位置：

- 最稳妥：`MediaLibraryDetail` 增加 Server 计算的 `source_display_path`，本地为 `Storage.RootDisplayPath + RelativeRoot`，115 为 provider display root + relative root；不要在 Vue 中手写 Windows/POSIX 路径拼接。
- 或者短期由 WebUI 用已加载的 `StorageSummary.root_display_path/root_path` 映射，但要有跨平台 join 测试。
- 列表摘要和编辑输入统一显示 `source_display_path`，旁边可用次要文案显示“相对根 `/`”，不要用相对根替代用户选择位置。

`StorageSummary` 当前已经把 `root_path/root_display_path` 返回给有 `storages.read` 权限的管理员（`types/api.ts:117-120`、`storage.go:65-76`），所以这项显示修复不需要重新引入“隐藏绝对路径”的旧策略。

### 5. 修改媒体库目录后，为什么朋友的任务仍写到旧分类目录

#### 5.1 媒体库本身的修改与扫描索引已经正确换根

`MediaLibraryService.Update` 会比较 Storage ID、相对根和 provider root ID（`media_library.go:237-289`）。来源变化时，它在同一事务中清除 entries、recognitions、scan runs 和 source assets，然后保存新根；重新启用会执行新的 initial/catch-up 扫描（`media_library.go:250-283`）。对应回归测试验证旧 `/old` 索引被清空并只扫描 `/new`（`media_library_test.go:332-395`）。

所以“媒体库页面显示新目录”与“新扫描使用新目录”这一层已有保护。

#### 5.2 下载任务在创建时冻结目标快照，旧任务不会跟着媒体库修改

创建下载任务时，系统把当时的媒体库选择复制到任务：

- `TargetLibraryID/Name`
- `TargetStorageID/Type/Root`
- `TargetRelativeRoot` 或 115 `TargetProviderRootID`
- transfer mode/conflict policy
- Profile revision/rules 与命名模板

证据见 `download.go:339-369` 和 `models.go:1084-1102`。本地转移随后只使用 `TargetStorageRoot + TargetRelativeRoot`（`transfer.go:519-525`）；115 转移只使用任务冻结的 `TargetProviderRootID`（`transfer_cloud.go:56-85`）。

因此最符合截图和描述的时间线是：

```text
任务创建（目标相对根仍是 /）
  → 用户把媒体库改成 /Media
  → 页面/媒体库记录显示 /Media
  → 旧任务完成下载
  → Transfer 使用旧快照 / + {category}/... 写入 Storage 根下分类目录
```

用户的电脑一直只配置一次，所以任务快照和媒体库当前值相同；朋友修改过目录，才暴露这个差异。

#### 5.3 推荐的行为合同

不能简单在每次 retry/transfer 时无条件读取“媒体库最新路径”，否则已经开始写入或部分写入的任务会在两个目录间分裂。建议：

1. 为媒体库增加可比较的 `configuration_revision`，DownloadTask 保存 `TargetLibraryRevision`。
2. 目标“媒体库 ID”始终不可变；具体目录/Profile/命名快照在**首次 Transfer 写入前**允许原子刷新到该媒体库的最新 revision。
3. 刷新条件必须同时满足：尚未创建任何有效 transfer plan，尚未发生目录创建/复制/移动/重命名/覆盖，且没有部分写入检查点。
4. 一旦进入 planning 后的首个写入，冻结快照；后续 retry 继续原目标，除非用户执行专门的“迁移/重新指定目标”恢复动作并通过部分写入检查。
5. 媒体库保存成功时显示提示：“新任务立即使用新目录；尚未开始入库的任务将在入库前刷新；已开始入库的任务保持原目标。”
6. 下载/整理任务详情显示“任务目标路径”和“当前媒体库路径”，若 revision 不同给出明确提示，避免用户只看媒体库设置误判。

仅凭 `processed_files=0` 不能证明云端/本地没有创建目录或发生部分写入；失败任务的重选目标首版只能对有确定 checkpoint 证明“尚未写入”的状态开放。已知 `transfer_route_unsupported` 且发生在规划前可以安全支持，其他错误需按 provider 操作日志/checkpoint 判定。

### 6. Player 识别器必须同步，但不能依赖 Server

Player 的 ServerDataSource 应直接消费 Server 已识别结果，不能二次识别；Player 自己添加的本地/OpenList 等原始数据源仍必须在没有 Server 时独立识别。这意味着不能把 Player 改成“统一请求 Server 识别”。

仓库已经有正确的同步起点：`player/scripts/verify-nextgen-recognition.ts:48-83` 直接读取 `server/internal/mediarecognition/testdata/corpus.v1.json`，验证 must-match/must-reject 和 provider 顺序稳定性。但当前 Player `PLAYER_RECOGNITION_ENGINE_VERSION='player-nextgen-v2'`，Server 为 `nextgen-domain-v8`，且 Player fixture adapter 只挑一个文件（`verify-nextgen-recognition.ts:245-260`），无法覆盖 Server 的完整多文件结构事实。

本任务实施时应同步：

- 将本轮新增失败样本放入同一个 provider-neutral corpus；Go 与 Player 都必须消费。
- Player fixture 不再任选一个文件，而要把完整文件集合、父目录、季集范围和规格输入自己的解析契约。
- Server/Player 各自保留实现版本，但共享 `contract_version` 和 `pack_bundle_version`；版本变化使各自识别缓存失效。
- 内置 `tv-v1/anime-v1` 使用同一固定快照、规则顺序和 digest；Player 端执行需要独立的正则兼容与超时保护，不能简单 `new RegExp` 执行 322 条上游规则。
- ServerDataSource 媒体继续只消费 Server DTO；同步只作用于 Player-owned 原始文件源。

### 7. 建议的实施拆分与测试门

#### Slice A：搜索展示与交互（不改下载安全语义）

- 新增顶部稳定站点 tab、PT/BT 和多选筛选、默认做种降序。
- 当前站点严格上一页/下一页替换，不追加。
- 把 result card 拆成组件；显示统一占位海报、标题、站点、体积、时间、做种/下载/完成、优惠和纯解析规格标签。
- 三个操作改为“检测 / 手动检测 / 入库”，保留现有权限、claim 和 CSRF 边界。
- sessionStorage 同步保存选中 tab、筛选、排序和各站当前页；仍遵守 30 分钟/300 条/512 KiB 上限。

测试：分页替换、上一页边界、tab 稳定顺序、PT/BT 多选、AND/OR 组合、null seeders 排序、SSE/JSON parity、过期 token 清理、手动 override 随下载传播。

#### Slice B：来源路径显示与目标 revision

- API 返回 `source_display_path`；WebUI 不再把 `/` 当作用户可见完整路径。
- 新任务断言使用修改后的 relative/provider root。
- 增加 library revision 与首次写入前安全重绑定；增加 revision drift 提示和日志。
- 历史/已开始 transfer 保持旧快照，不静默搬迁。

测试：Windows Storage 根 `/` 显示完整路径、子目录显示、115 display path、修改后新任务目标、修改前但尚未写入任务的安全刷新、已开始/部分写入任务拒绝刷新。

#### Slice C：识别证据等级与 Player parity

- 快速结果 DTO 加 `evidence_level=title_preview|torrent_manifest|downloader_manifest`。
- 有 `.torrent` 能力的 adapter 可在不提交任务时做清单预识别；magnet-only 维持 title preview。
- UI 明确最终入库会重新使用完整清单。
- 更新共享 corpus/golden output 和 Player parity 脚本，确保本轮识别器规则同步。

测试：同一 title-preview 允许未识别，而 manifest 必须命中；人工 override 始终优先并再次 `GetByID`；自动预识别不强制覆盖更强 manifest 结果。

### 8. External References

- MoviePilot Frontend v3 `useTorrentFilter.ts`（研究提交 `d28fab0fe57ca0df049d19a308318306ba0e3e73`）：其多规则筛选覆盖站点、季集、发布组、编码、优惠、版本、分辨率，排序覆盖站点、体积、做种、发布时间。<https://github.com/jxxghp/MoviePilot-Frontend/blob/d28fab0fe57ca0df049d19a308318306ba0e3e73/src/composables/useTorrentFilter.ts>
- MoviePilot Frontend v3 `TorrentCard.vue`：卡片展示媒体/发行标题、站点、做种/下载、发布时间、分辨率、编码、版本、发布组、优惠、体积等信息。<https://github.com/jxxghp/MoviePilot-Frontend/blob/d28fab0fe57ca0df049d19a308318306ba0e3e73/src/components/cards/TorrentCard.vue>
- MoviePilot Frontend v3 `resource.vue`：资源页支持卡片/行视图、流式渐进搜索、筛选状态与上次搜索恢复。<https://github.com/jxxghp/MoviePilot-Frontend/blob/d28fab0fe57ca0df049d19a308318306ba0e3e73/src/pages/resource.vue>
- MoviePilot v3 媒体识别研究与许可证边界见本任务 `research/moviepilot-v3.md`；只借鉴公开行为与测试维度，不复制 GPLv3 实现。

### 9. Related Specs

- `.trellis/spec/backend/pt-discovery.md`
- `.trellis/spec/backend/media-library-foundation.md`
- `.trellis/spec/backend/transfer-organization.md`
- `.trellis/spec/backend/downloader-management.md`
- `.trellis/spec/frontend/server-admin-ui.md`
- `.trellis/spec/frontend/server-online-media.md`
- `.trellis/spec/guides/cross-layer-thinking-guide.md`

## Caveats / Not Found

- 没有读取用户朋友机器的数据库、任务时间线和运行日志，所以“旧目标快照”是由代码和现象支持的最高概率根因，不是对那台机器状态的直接取证。实施时应先用任务创建时间、媒体库更新时间、`TargetRelativeRoot/TargetProviderRootID` 的脱敏诊断证实。
- 当前 API 不公开 DownloadTask 的目标根快照，管理员页面也无法直接比较“任务目标 vs 当前媒体库目标”；需要新增安全的 display-only 诊断字段，不能暴露 115 provider ID、凭据或签名 URL。
- 各站上游是否支持真正的服务端排序不一致；首版浏览器排序只能保证当前已取回结果集，不能宣传为跨全部远端页的全局排序。
- 搜索卡片在未调用 TMDB 前无法保证真实海报。自动为所有结果调用 TMDB 会放大请求和触发限速，建议占位海报 + 用户检测后填充，或后续增加按规范身份去重的有界批量 enrich。
- 对磁力结果，下载器取得 metadata 前通常没有完整文件清单；预识别与最终识别允许不同，UI 必须解释证据等级。
- 本研究按 Trellis researcher 权限只写入本研究文件，没有修改代码、规范或 Git 状态。
