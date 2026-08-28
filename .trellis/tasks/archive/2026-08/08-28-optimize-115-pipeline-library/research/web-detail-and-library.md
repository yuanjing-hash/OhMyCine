# Research: Web detail white screen and Server media-library management

- Query: 定位推荐详情展开季度后的白屏根因；盘点现有媒体库海报墙/详情/元数据维护能力；定义本地与 115 源文件彻底删除的安全边界和最小实现文件。
- Scope: internal
- Date: 2026-08-28

## Findings

### 1. 白屏根因已定位：Go 的空切片被编码为 `null`，展开后模板读取 `null.length`

这不是路由跳转，也不是展开按钮本身的问题。详情页在未展开时不会读取每集的 `library_ids.length`；展开后才进入该表达式，所以整个 Vue 渲染 effect 抛出运行时异常，主内容区消失。

- `DiscoveryDetailView.vue` 的折叠状态仅更新本地数组，没有导航行为（`server/webui/src/views/DiscoveryDetailView.vue:55`）。
- 展开后的模板无防御地读取 `season.episodes.length` 和 `episode.library_ids.length`（`DiscoveryDetailView.vue:102`）。
- 前端类型把 `library_ids` 声明为必有的 `number[]`（`server/webui/src/discovery.ts:48-49`），因此 TypeScript 无法提示运行时 `null`。
- 后端对季级 `Episodes` 显式初始化为空切片，所以无集时会得到 `[]`（`server/internal/services/media_coverage.go:177`）。但每集的库 ID 使用 `append([]uint(nil), ...)` 构造；没有任何入库匹配时结果仍为 nil slice，JSON 编码后就是 `"library_ids": null`（`media_coverage.go:201-203`）。截图里的剧总集数 8、已入库 0，因此展开第一季时第一条 episode 即触发 `Cannot read properties of null (reading 'length')`。

修复必须双层收口：

1. 后端契约保证 collection 永远输出 `[]`，尤其 `MediaCoverageEpisode.LibraryIDs`、movie 的 `library_ids`、顶层 `libraries`、TV `seasons` 和季 `episodes`。
2. 前端 API 边界做轻量归一化，把旧版 Server 或异常缓存中的 `null`/缺失 collection 转成空数组；模板使用归一化 DTO，而不是在每个表达式散落可选链。
3. 季集块增加局部错误/空态；单个异常 season 不得击穿整个详情路由。
4. 回归测试必须直接使用截图对应形状：`episode.library_ids = null`、`season.episodes = null`、空库、重复展开/收起，而不能只测符合 TypeScript 声明的理想对象。

### 2. 现有媒体库 API 足以承担浏览骨架，不应新造第二套扫描索引

当前 Server 已有两套可复用读模型：管理端 catalog 与 Player catalog。此次管理端“发现 > 媒体库”应以管理端 catalog 为主，复用推荐详情视觉组件；不要直接复用 Player 路由，也不要重新扫描数据源。

- 媒体库列表：`GET /api/v1/media-libraries`（`server/internal/httpserver/router.go:118`），已有启用状态、顺序、Storage、扫描状态等配置读模型。
- 作品分页：`GET /api/v1/media-libraries/:id/catalog`（`router.go:132`），支持 `query`、`media_type=movie|series`、`match_status`、分页；前端已有 endpoint 和页数工具（`server/webui/src/media-catalog.ts:11-37`）。
- 作品详情：`GET /api/v1/media-libraries/:id/catalog/:work`（`router.go:133`），现有 DTO 已包含作品摘要、季、集/文件和可重新整理的受管 transfer（`server/internal/services/media_catalog.go:37-91`, `:158-215`）。季集来自 `MediaLibraryEntry`，因此显示的是该库真实入库覆盖，不是 TMDB 推测。
- catalog 当前按 `work_key` 聚合，统计文件数、季数、集数、大小、分类、匹配状态、TMDB ID 和年份（`media_catalog.go:93-107`, `:130-155`, `:333-334`）。
- Player 端同一路径族已经证明 catalog 能生成可播放详情，但管理端应保留管理权限和相对路径安全投影，不把 Player stream DTO 当管理 DTO（`router.go:44-48`）。

现有缺口：`MediaCatalogItem` 没有 poster/backdrop/overview，也没有直接可调用的 recognition token（`media_catalog.go:37-52`）；管理详情因此不能只靠当前 DTO 做推荐页样式，也无法从 work token 直接调用已有人工匹配接口。建议扩展 catalog projection：返回安全的同源海报/背景 URL、简介/原名，以及 work 对应的 recognition token/revision。海报优先复用 Server 已有 library artwork/TMDB image proxy，不把 TMDB 原始 URL或 115 路径透给浏览器。

### 3. “修改元数据 / 重新刮削”已有能力可组合，但需补作品级入口

- 单识别单元已有重试、候选搜索、人工 TMDB 覆盖、清除人工覆盖：`RetryRecognition`、`RecognitionCandidates`、`OverrideRecognition`、`ClearRecognitionOverride`（`server/internal/services/media_library_recognition_api.go:97-214`），对应路由在 `router.go:127-131`。
- 现有系统媒体库页已经调用这些能力（`server/webui/src/views/MediaLibrariesView.vue:194-208`, `:482`），其候选弹窗与状态反馈可抽成共享组件。
- 受管下载产生的媒体已有“修正识别并重新整理”预览/确认/持久任务链（`server/internal/services/media_reorganization.go:131-274`；路由 `router.go:164-166`），catalog detail 也已经暴露 `reorganizable_transfers`（`media_catalog.go:76-91`, `:218-242`）。

因此本轮不需要重写刮削器。最小做法是增加“作品级操作解析”：用 `(library_id, work_token)` 在服务端重新解析当前 entries 和 recognition revision，再委托现有 recognition/reorganization 服务。操作必须区分：

- 仅修改元数据匹配：人工指定 TMDB/清除覆盖，更新识别投影，不移动文件。
- 重新刮削：对当前 work 的 recognition 单元重新请求元数据，并触发 catalog/artifact revision；默认不移动源文件。
- 修正识别并重新整理：继续走现有 preview → confirm → durable job，不与普通重新刮削混为一键破坏性操作。

### 4. 媒体作品删除不能复用“删除媒体库配置”；可复用 transfer deletion 的安全模式

`DELETE /media-libraries/:id` 当前语义只是删除库配置（`router.go:123`），绝不能改成删除作品源文件。作品删除需要独立的 preview/confirm API 和独立权限，例如 `media_libraries.media_delete`，不可仅凭 `media_libraries.delete` 或前端按钮授权。

现有 `TransferService` 删除已经提供了正确的结构模板：五分钟短期不透明确认令牌、preview 保存不可变范围、confirm 再对账、审计成功/失败、区分只删记录/来源/媒体库（`server/internal/services/transfer_deletion.go:29`, `:44-78`, `:123-209`, `:777-815`）。作品删除应复用这些通用原语或抽取共享 destructive-preview 组件，但不能要求作品一定来自一个 transfer；手工放入库中的内容也必须可删。

建议作品删除契约：

```text
POST /api/v1/media-libraries/:id/catalog/:work/deletion-preview
POST /api/v1/media-libraries/:id/catalog/:work/deletion-confirm
```

Preview 返回：媒体库、Storage 类型、作品标题、当前 work revision、文件数量/总大小、安全的相对路径摘要、是否包含本地/115、STRM 影响数、阻断原因、短期 confirmation token；不返回本地绝对路径、115 provider ID/cookie 或签名直链。

Confirm 必须重新验证：

- token 未过期、未使用，actor 与 preview 相同且仍有专用删除权限；
- library、Storage、work revision、entry 集合及每条 entry 的 ID/relative path/size/provider identity 与 preview 一致；变化即令 preview 失效；
- 本地：每个目标由 `Storage.RootPath + library.RelativeRoot + entry.RelativePath` 重算和规范化，必须仍在库根内；拒绝 `..`、UNC/卷切换、符号链接/reparse escape；只删除 preview 中的文件，目录仅在变空且仍处于库根内时清理；
- 115：先用 `providerItemWithinRoot` 证明 library provider root 位于 Storage root，再逐项/批量证明 item 仍在 library root 内，且 ID、parent、name、size（有 SHA1 时也比较）未漂移；只调用对应连接的 `Recycle`。普通删除默认进入 115 回收站；“彻底清空回收站”不在本轮，避免把作品删除等同不可恢复 purge；
- 任意目标缺失视为可收敛的 missing，但不得扩大删除范围；任意目标歧义/越界时该确认失败关闭，不按文件名猜测；
- 删除文件成功后，在短事务中停用/移除 catalog entries、managed items 和 recognition 投影，增加 library dirty generation，排队 STRM reconcile/cleanup 和 Emby/Jellyfin refresh；不能先删数据库再删源文件；部分 provider 成功需要持久化逐项状态并可幂等续跑；
- 审计只记录 library/work token hash、数量、Storage/provider 类型、结果和安全错误码，不记录绝对路径、provider ID、Cookie 或 URL。

现有可复用边界代码包括 `providerItemWithinRoot`（`server/internal/services/cloud_boundary.go:13-44`）以及 transfer deletion 对 provider root/managed item 的再校验、缓存祖先验证和 115 Recycle（`transfer_deletion.go:463-596`, `:719-753`）。不要直接调用其 transfer-only public method；应抽取共享 validator/executor，避免媒体作品删除和 transfer 删除以后安全规则漂移。

### 5. 导航与页面最小实现形状

- 保留 `/system/media-libraries` 和 `MediaLibrariesView.vue` 全部能力，只把 `navigation.ts:79` 的显示名改为“媒体库管理”。
- 在发现组新增“媒体库”，权限为 `media_libraries.read`，新路由建议 `/discovery/library`；不要复用 `navigationMeta('recommendations')`，应有独立 navigation ID（现有发现组定义在 `server/webui/src/navigation.ts:44-51`，路由在 `server/webui/src/router/index.ts:56-58`）。
- 页面顶部显示“全部库”及已启用库切换；“全部库”需要后端聚合 endpoint 或前端并发分页会造成重复作品、错误总数和 N+1。首版可先只支持单库切换，并把“全部库”作为后端按 TMDB/work identity 去重的聚合查询实现；不能简单拼接各库第一页。
- 分类标签使用 catalog 的 `kind/category_name`，海报卡进入管理端 library detail。详情可复用推荐页 hero/season UI 的共享展示组件，但数据源分别适配 discovery detail 与 catalog detail，禁止直接复制整页模板。

### 6. 最小实现文件清单

白屏 P0：

- `server/internal/services/media_coverage.go` — 所有 collection 非 nil；修正 `library_ids:null`。
- `server/internal/services/media_coverage_test.go` — JSON 契约和无入库集 fixture。
- `server/webui/src/discovery.ts` — coverage DTO 运行时归一化 helper。
- `server/webui/src/discovery.test.ts` — null/缺失数组、重复展开数据 fixture。
- `server/webui/src/views/DiscoveryDetailView.vue` — 使用归一化数据与季集局部降级。

媒体库浏览 P0/P1：

- `server/internal/services/media_catalog.go`、对应测试 — 海报/背景/简介/recognition token/revision 与可选跨库聚合投影。
- `server/internal/handlers/media_libraries.go`、`server/internal/httpserver/router.go` — 扩展 catalog/API。
- `server/webui/src/media-catalog.ts`、测试 — 完整 DTO、分页和安全 URL 归一化。
- 新建 `server/webui/src/views/LibraryCatalogView.vue`、`LibraryCatalogDetailView.vue`（或一个列表页加共享详情组件）。
- 抽取推荐/catalog 共用的详情 hero、季集覆盖展示组件，避免两份模板再次出现 null contract drift。
- `server/webui/src/navigation.ts`、`router/index.ts`、导航/路由测试 — 新入口与“媒体库管理”文案。

元数据与删除 P1：

- 在 `media_catalog.go` 或独立 `media_catalog_actions.go` 增加 work-token 到 recognition/reorganization 的服务端解析；handlers/routes 与 WebUI client/dialog 配套。
- 新建 `media_catalog_deletion.go` 及测试；抽取 `transfer_deletion.go` 的确认令牌、provider/root validator 和逐项 checkpoint 为共享内部组件。
- `server/internal/authz/catalog.go`、`catalog.json` 及 generated frontend permissions — 新的作品源文件删除权限。
- `server/pkg/cloud/client.go` / 115 adapter 仅在需要批量 recycle 接口时扩展；首版仍须维持严格逐项身份对账。
- STRM/refresh 服务只通过既有调度边界触发，不在 handler 内直接删 `.strm` 或调用媒体服务器。

## Files Found

- `server/webui/src/views/DiscoveryDetailView.vue` — 白屏触发表达式与当前推荐详情 UI。
- `server/webui/src/discovery.ts` — 理想化的 coverage TypeScript 契约。
- `server/internal/services/media_coverage.go` — 实际产生 `library_ids:null` 的 Go JSON 投影。
- `server/internal/services/media_catalog.go` — 已有作品分页、详情、季集和受管 transfer 读模型。
- `server/internal/services/media_library_recognition_api.go` — 已有重试、候选、人工覆盖与清除覆盖能力。
- `server/internal/services/media_reorganization.go` — 已有安全预览/确认和持久化重新整理任务。
- `server/internal/services/transfer_deletion.go` — 可抽取的破坏性预览、边界再验证、审计和 115 回收站删除模式。
- `server/internal/services/cloud_boundary.go` — provider item/root 祖先验证。
- `server/internal/httpserver/router.go` — 当前管理端/Player catalog、recognition、reorganization 路由。
- `server/webui/src/views/MediaLibrariesView.vue` — 现有配置页及识别维护 UI。
- `server/webui/src/navigation.ts`、`server/webui/src/router/index.ts` — 侧栏与路由注册。

## Code Patterns

- Collection DTO 必须在服务层构造为空切片，API 边界再做旧版兼容归一化；模板不信任静态类型代表的运行时 JSON。
- catalog 使用 opaque work token，recognition 使用 opaque recognition token；作品级 action 必须在服务端解析关联，不能让前端拼数据库 ID。
- 破坏性操作使用 `preview -> short-lived opaque token -> revalidate -> durable/idempotent execution -> reconcile -> audit`。
- provider 删除只接受数据库当前 entry/managed identity 和配置根；不接受浏览器提交绝对路径/provider ID 作为删除授权。

## External References

- 本主题无需外部实现即可定位；产品视觉可参考 MoviePilot 行为，但 API 与删除边界应以 OhMyCine 现有 catalog、recognition、transfer deletion 契约为准。

## Related Specs

- `.trellis/spec/backend/media-library-foundation.md` — Storage-relative 库根、扫描事实与路径安全。
- `.trellis/spec/backend/transfer-organization.md` — managed item、115 边界和破坏性对账。
- `.trellis/spec/backend/security-guidelines.md` — 文件删除、权限、审计与秘密脱敏。
- `.trellis/spec/backend/api-guidelines.md` — `/api/v1`、服务层授权和统一响应。
- `.trellis/spec/frontend/server-admin-ui.md` — Server 管理 UI 与局部错误态。
- `.trellis/spec/guides/cross-layer-thinking-guide.md` — DTO/JSON/模板跨层契约与 destructive preview。

## Caveats / Not Found

- 当前浏览器会话没有已登录的本地 Server Cookie，因此无法实时读取控制台；白屏根因由真实截图输入形状、模板惰性访问点与后端 nil slice JSON 路径交叉确认。实现后仍应在登录态浏览器做一次点击回归。
- catalog 当前没有现成的“全部媒体库”去重分页 endpoint；若本轮时间紧，应优先交付单库切换，不应以前端拼页伪装全库分页。
- 现有 transfer deletion 只能覆盖明确受某 transfer 管理的内容；手工导入作品需要新的 catalog-entry deletion boundary，不能强行伪造 transfer。
- 115 `Recycle` 是进入回收站，不等同永久清空；若产品文案坚持“完全删除”，必须明确当前定义是“从媒体库数据源移除（115 可在回收站恢复）”，永久 purge 另行设计并二次确认。
