# OhMyCine 当前实现契约摘录

## Evidence scope

本记录用于给两个实施子任务提供小而完整的当前代码/规范边界，避免大型 spec 在子任务上下文注入时被截断。代码状态检查于 2026-08-27，实施前应重新核对发生过改动的行。

## 1. Discovery / TMDB

- `server/internal/services/discovery.go:37-46` 的 `DiscoveryService` 已持有 provider、TMDB client 和受控图片 HTTP client；媒体海报搜索和统一详情应扩展这个 service，而不是新建旁路 client。
- `server/internal/services/discovery.go:67` 已有 `DiscoveryDetail`，`server/internal/services/discovery.go:215` 已按 actor + provider/media type/provider ID 读取详情，`server/internal/services/discovery.go:294` 已把 TMDB match 投影为 `DiscoveryWork`。
- `server/internal/handlers/discovery.go:29` 的 detail handler 已保持薄层，新增 media-search/coverage/identity-search 应遵循相同模式。
- `server/pkg/metadata/tmdb/client.go:588` 有标题 `Search`，`:687` 有直接身份 `GetByID`，`:1063` 有分页候选 `SearchCandidates`。
- `server/pkg/metadata/tmdb/candidate_enrichment.go:16` 把候选别名上限设为 32；`:113-149` 已聚合/去重 alternative titles 和 translations。身份搜索应复用这些安全字段，但另设更低的站点查询预算。
- `server/pkg/metadata/tmdb/episodes.go:26` 已有受限 `GetTVSeasonEpisodes`；episode DTO 不含凭据或上游正文。

## 2. Site search / opaque result

- `server/internal/services/site.go:151` 定义安全 `SiteSearchResult`。
- `server/internal/services/site.go:640` 的 `Search` 和 `:647` 的 `SearchEach` 已实现 JSON/SSE 共用的站点搜索入口；媒体身份多名称搜索应复用内部单站逻辑、限速、并发和取消。
- `server/internal/services/site.go:721` 的 `searchSite` 是单站边界。
- `server/internal/services/site.go:1229` 的 `resolveClaim` 按 token + actor 读取服务端私有结果；公开 DTO 只能保留短期 `result_token`。
- `server/internal/services/site.go:1090` 的 `Download` 是当前资源到下载流水线的安全交接。follow Worker 可以新增服务端内部交接面，但不得把真实 torrent/magnet URL 持久化或返回前端。
- PT discovery 长期规范要求：多站有界并发、每站限速、部分失败保留成功结果、opaque actor-bound claim、日志/审计/DTO 不含 Cookie/passkey/真实下载地址。

## 3. Media library facts

- `server/internal/services/media_catalog.go:37-52` 的 `MediaCatalogItem` 已包含 `TMDBID`、match status 和安全聚合计数。
- `server/internal/services/media_catalog.go:61-79` 的 episode/season/detail DTO 已包含 season、episode 和相对条目，但新的跨库 coverage DTO 不得返回相对路径。
- `server/internal/services/media_catalog.go:130-149` 当前 catalog 是按单一 library 授权和聚合；新 coverage 必须在 service 层枚举 actor 可读且 enabled 的库后再聚合。
- 只有 verified `tmdb_id + media_type + season + episode` 可作为确定存在事实。未扫描、partial enumeration、provisional/unrecognized 或缺少 air date 必须产生 unknown/future，而不是自动下载目标。
- 大型 media-library spec 的关键约束：derived read model 不授权文件操作；partial scan 不删除/推断 unseen 条目；API 不暴露物理 root/provider ID；网络识别不持有 SQLite 写事务。

## 4. Persistent queue

- `server/internal/database/migrations.go:1361-1373` 的 jobs 已提供 owner、job type、priority、resource key、coalescing key、generation、payload/checkpoint、lease、retry 和安全错误字段。
- `idx_jobs_active_coalescing` 对 active job 的 `(job_type, resource_key, coalescing_key)` 做唯一约束，可作为同订阅运行幂等基础。
- follow-search 必须注册独立 queue policy/worker，HTTP 只入队；外部调用不在数据库事务中执行；checkpoint 只保存稳定 ID、revision、阶段和安全集坐标。

## 5. Download / Transfer handoff

- `server/internal/services/download.go:167-183` 的 `SubmitDownloadInput` 已包含 downloader、target media library、priority、source 和 internal-only `RecognitionOverride`。
- `server/internal/services/download.go:270-277` 的公开 `Submit` 检查 `downloads.create` 并转入内部 submit；follow 需要以订阅 owner 重验权限，不能冒充系统绕过 owner。
- `server/internal/services/download.go:309-365` 已验证目标媒体库、下载器和受信 identity override；`:387-438` 持久化 target/profile/identity snapshot 和安全审计。
- 自动订阅只负责编排：受控站点结果 → DownloadService → existing recognition → Transfer → media library import → refresh/notify。不得自行调用 downloader client、移动文件或生成 STRM。
- 幂等必须覆盖 crash-after-submit：episode claim 与 download task 关联前后都要能从稳定 source/resource fingerprint 和活动任务事实收敛，不能仅依赖内存 token。

## 6. Auth / UI / security

- `server/internal/authz/catalog.json:76-78` 当前已有 `follows.read_own`, `follows.read_all`, `follows.create`；更新、删除和执行需 own/all 细粒度权限，并同步角色种子、生成前端权限和一致性测试。
- `server/webui/src/navigation.ts:57-59` 已预留订阅导航；真实页面应替换占位并遵循 Server admin UI tokens、键盘和移动端规则。
- `server/webui/src/views/DiscoveryDetailView.vue` 已承载统一详情并有资源搜索/订阅动作；应扩展该页，不创建第二详情页。
- API 路由必须在 `/api/v1/`、默认认证、标准 envelope、服务层重复授权；action mutation 需要既有 CSRF 保护。
- follow snapshot/Job/run/事件是非敏感区：禁止 Cookie、passkey、下载器凭据、真实 torrent/magnet URL、临时 CDN/资源地址、绝对路径、上游响应体。
- owner-owned follow 数据所有查询都必须显式 scope；`read_all` 不隐式授予 update/delete/execute。
- SQLite migration additive、显式版本、fresh/previous-head/idempotent 测试；慢网络调用不持有事务。

## 7. Documentation / tooling

- 仓库检查未发现 `server/api/openapi.yaml`；每个实施子任务开始时需重新确认，若文件出现则同步更新。
- Windows-native 为权威环境；Server 完整门包括 Go tests/vet、Web UI test/typecheck/lint/build，必要时运行 `server/test.ps1`。
- 架构状态变化同步 `docs/architecture/02-server-design.md` 和 `docs/architecture/06-roadmap.md`。

