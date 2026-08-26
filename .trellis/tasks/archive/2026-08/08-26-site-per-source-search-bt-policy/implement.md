# Implementation Plan — 站点单站搜索、地址驱动 BT 与统一媒体识别

## Ordered Checklist

### Phase 1 — Registry and contracts

- [ ] 扩展 `builtin.Definition`，区分内部 host registry、公开添加目录和 capabilities，保持现有 stable kind 不变。
- [ ] 增加确定性的 BT 官网 URL resolver 与稳定错误码，覆盖 URL/IDNA/host/port/alias/相似域名拒绝。
- [ ] 增加管理员 `POST /api/v1/sites/resolve` 合同，并让 Create 对 BT 地址执行服务端二次解析，拒绝客户端伪造 kind。
- [ ] 调整 Catalog 响应，使公共 BT 适配器不再作为默认可选清单展示，同时保留 PT 与 Torznab 流程。
- [ ] 为 SiteSummary 暴露安全 capability 字段（若现有 adapter 全部可搜索，也必须由 Server 推导而非 UI 猜测）。

### Phase 2 — Existing BT migration without data migration

- [ ] 将 Nyaa、AnimeTosho、Tokyo Toshokan、Mikan、AniDex 接入新 resolver，保持 adapter、kind、旧记录和下载 claim 兼容。
- [ ] 更新旧的固定 BaseURL 校验为“stable kind + 显式允许 host/origin”校验。
- [ ] 覆盖旧记录读取、编辑、测试、聚合搜索和 CookieCloud 不接管无凭据 BT 站的回归测试。

### Phase 3 — New RSS/API site adapters

- [ ] 实现并 fixture 测试 DMHY、ACG.RIP。
- [ ] 实现并 fixture 测试 YTS、EZTV。
- [ ] 验证标题、大小、发布时间、做种/下载数、分类、分页事实和受控 magnet/torrent 解析。

### Phase 4 — New HTML site adapters

- [ ] 分别实现并 fixture 测试 1337x、The Pirate Bay、EXT.to、LimeTorrents。
- [ ] 为每站覆盖正常结果、空结果、下一页、结构损坏、挑战/错误页、恶意链接、跨 host 下载与重定向拒绝。
- [ ] 确保解析失败只影响当前站点，并返回稳定安全错误，不泄露响应正文。

### Phase 5 — Server Web UI

- [ ] 将“添加 → BT”改为官网地址输入与识别预览；不渲染具体公共 BT 站下拉清单或预填域名。
- [ ] 保留 Torznab 独立选择和 API Key 表单；PT/CookieCloud 表单不回归。
- [ ] 在可搜索站点卡片增加“搜索”，禁用状态显示原因。
- [ ] 让 ExploreView 支持 route `site_id` 固定模式；请求、重试、翻页和 sessionStorage 恢复均绑定同一站点。
- [ ] 复用现有结果筛选/排序、快速识别、人工识别、下载入库组件和 API。

### Phase 6 — Authoritative identity snapshot and migration

- [ ] 盘点下载前 claim、DownloadTask recognition/override、完成清单和 Transfer snapshot 的现有字段，先写跨阶段 identity 丢失/覆盖回归测试。
- [ ] 新增版本化 `MediaIdentitySnapshot`/repository/service，定义 source、status、locked、revision、candidate/evidence 与 episode facts 引用。
- [ ] 将全局/单站搜索、手工下载、下载任务人工介入和整理重试接入同一 identity service；manual `GetByID` 后原子写 locked revision。
- [ ] 让完成 worker 只补 manifest/episode facts，Transfer 只校验指定 revision 和文件安全，移除后续阶段的独立 TMDB 重搜/置信度 gate。
- [ ] 增加旧 recognition/override 一次性 backfill；旧人工记录迁移后必须 locked，重复启动/重试幂等。

### Phase 7 — Deterministic selection and TV package recovery

- [ ] 用用户日志对应的未清洗真实文件名建立 10 集 fixture，并先补出“旧逻辑只选 1/10、Transfer 拒绝”的失败测试。
- [ ] 将 TMDB 候选排序改为完整稳定序：匹配分、popularity/vote、稳定 TMDB ID；把 0.78/0.68/0.64、0.06 conflict margin 和新增 0.35 AI rewrite 线集中在版本化 ScoreConfig。
- [ ] 删除 legacy confidence gate 与 Transfer 独立 0.80 gate；输出 matched/low/conflict/extreme/no-match 决策，AI 关闭时普通 low/conflict（含完全同分）稳定命中最高候选，只有 extreme/no-match 进入 local provisional。
- [ ] 增加 AI 关闭边界测试：0.35 附近、0.64/0.68/0.78 门槛、runner-up gap 0.06 和完全同分都产生确定且候选顺序无关的结果，不进入人工等待。
- [ ] 无 TMDB 候选时生成 `local_provisional` 和待整理状态，确保不会因为 metadata 为空而丢失任务或重提下载器。
- [ ] 抽取共享包级 episode resolver，覆盖强结构、动漫方括号集号、`v2` 和相邻 BIG5/编码/分辨率发布证据。
- [ ] 让 `selectDownloadPackageManifest` 与 `validateAutomaticTransferSnapshot` 消费同一逐文件事实，移除 TV 多视频全部无集号时的最大文件 fallback。
- [ ] 新增安全错误 `transfer_episode_unrecognized`、日志计数和 Web UI 文案，保留已验证 TMDB 身份与完整来源。
- [ ] 增加人工 override 端到端恢复测试：不访问/重提 qBittorrent，10 个有效剧集进入一个 TransferTask，重复执行幂等。
- [ ] 增加反例：年份、数字电影名、`1080p`、`1920X1080`、`10bit`、重复/缺失/异常集号不得被误判或部分入库。

### Phase 8 — Server AI-assisted recognition

- [ ] 增加 AI 媒体识别设置模型/API：enabled 默认 false、provider type、OpenAI Base URL、encrypted API Key、model、允许发送清理 basename、revision；接入 SecretInput/reveal/权限/审计。
- [ ] 在任何运行时 AI client 调用前集中检查 enabled；覆盖关闭状态下搜索、下载完成、Transfer、重试、扫描和后台 worker 的“零 AI 请求”测试。测试连接/获取模型使用独立显式管理员端点。
- [ ] 定义 `AIRecognitionProvider`，实现 OpenAI-compatible Chat Completions `/v1/models`、`json_schema → json_object` 一次性兼容降级和受控 Base URL。
- [ ] 实现 Google AI Studio native `v1beta/models`/`generateContent`、`x-goog-api-key`、固定官方 origin、`responseMimeType` 与 `responseSchema`；只列出支持 generateContent 的模型。
- [ ] 将候选仲裁与标题重写两套系统 prompt、payload DTO、严格 JSON Schema 和 prompt version 固化为代码/fixture；输入字段均视为不可信数据。
- [ ] ranker 返回 low/conflict 且不极低时调用一次候选仲裁，只接受输入中的 candidate_ref；模型请求 rewrite/unknown 时进入标题重写。
- [ ] best total/title similarity < 0.35 或无候选时调用一次标题重写，限制最多 5 条查询，重新走 TMDB search/rank；每 revision 最多两次 AI 调用且禁止递归。
- [ ] 对 AI 输出执行 byte limit、DisallowUnknownFields/trailing content、枚举/长度/数量/季集范围/candidate_ref 校验；模型 confidence 仅用于诊断。
- [ ] 实现超时/限流/非法 JSON/Schema 不支持/模型不可用回退：有候选稳定最高、无候选 local provisional，确保失败不占住后续 Job。
- [ ] Web UI 增加 AI 总开关、两种 Provider、模型下拉+手填、测试连接、发送范围提示和双主题测试；开关关闭要清晰显示“运行时不会调用 AI”。

### Phase 9 — Correct identity and reorganize

- [ ] 为下载历史、整理历史和媒体详情增加“修正识别并重新整理”入口，复用人工 TMDB 搜索/选择组件。
- [ ] 实现 reorganization planner：读取 managed artifact manifest 和当前规则 revision，计算 old → new、字幕/伴随文件、NFO/JPG/STRM 与冲突预览。
- [ ] 生成绑定 actor、identity/library/artifact/rule revision 的短期 plan token；执行前重新验证并通过既有冲突策略确认。
- [ ] 实现幂等 reorganization Job，分别覆盖本地和 115 driver 的移动/重命名/对账；copy/move/symlink 遵循既有来源/目标语义。
- [ ] 成功后更新 locked identity 和 artifact manifest、重建元数据/STRM、清理系统创建且已空旧目录并刷新 Player/Emby/Jellyfin。
- [ ] 覆盖部分失败、重复执行、规则/目录在预览后变化、非托管文件、路径越界、115 item 变化和冲突回滚测试。

### Phase 10 — UI states, documentation and verification

- [ ] 更新 OpenAPI（若仓库已存在对应合同）和 `docs/architecture/02-server-design.md`。
- [ ] 更新 `.trellis/spec/backend/pt-discovery.md` 中“Public BT RSS and Torznab”场景为地址驱动内建适配器合同。
- [ ] 更新 `.trellis/spec/backend/downloader-management.md`、`transfer-organization.md`、security guidelines 与 cross-layer guide，固化 identity revision、人工锁、置信度语义、AI 和重新整理合同。
- [ ] 统一 Web UI 状态：自动暂定、AI 辅助、人工锁定、正在重新识别并入库、身份已确认但集号待整理、重新整理预览/执行/失败。
- [ ] 执行 Server、Web UI、跨层安全与兼容测试；对新增 HTML/API fixtures 做离线确定性验证。
- [ ] Windows 浏览器实测：BT 添加/拒绝相似域名、单站搜索/分页、AI 关闭零请求、OpenAI/Google 测试连接、低分候选仲裁、极低标题重写、人工一次贯穿、AI 失败回退、10 集恢复和重新整理；测试结束关闭进程。

### Phase 11 — Four-scope transfer deletion and Server Beta

- [ ] 定义四个删除 scope、预览/确认 DTO、稳定错误码和权限合同；保留旧 DELETE 为 record-only。
- [ ] 实现 actor/Transfer/Download/Library/identity/task revision、来源 manifest、managed ownership digest 和五分钟过期绑定的单次 opaque token，数据库仅保存 SHA-256。
- [ ] 实现 source 删除：本地暂存根逐项边界校验、qBittorrent provider delete-data 收口、115 稳定 item ID/root ancestry 回收；活动下载/做种/重整安全阻断或先收口。
- [ ] 实现 library 删除：仅 active + managed ownership，本地拒绝 symlink/junction/Reparse Point，115 重验 item/parent/root；部分失败保留可重试状态。
- [ ] 文件动作完成后清理相关 Transfer/Download/Seeding/Reorganization/Job 历史；媒体库删除推进对账并刷新 NFO/JPG/STRM、Player 与 Emby/Jellyfin。
- [ ] Web UI 用明确的四档按钮/选择器展示安全计数和影响范围；包含文件的 scope 需要二次确认且不只依赖颜色表达风险。
- [ ] 增加 record-only、source、library、both、本地/115、越界、身份变化、活动任务、重复 token、部分失败、审计和前端交互测试。
- [ ] 更新 Server 架构、路线图、Trellis backend/frontend 规范，执行完整 Go/Web UI 门禁与 `git diff --check`。
- [ ] 按 Conventional Commits 提交全部当前任务改动，推送最新 `develop`，从最新 `origin/develop` 触发并验证 Server Beta；不修改或发布 Player。

## Validation Commands

```powershell
cd server
go test ./pkg/site/...
go test ./pkg/metadata/... ./internal/medialibrary/...
go test ./internal/services -run 'TestSite|TestCookieCloud'
go test ./internal/services -run 'TestIdentity|TestRecognition|TestSelectDownloadPackageManifest|TestTransferEnqueue|TestReorganization|TestAI'
go test ./internal/handlers -run 'TestSite|TestDiscovery|TestAI|TestReorganization'
go test ./...
go build -tags webui ./cmd/server

cd webui
npm run test
npm run typecheck
npm run lint
npm run build

cd ../..
git diff --check
```

## Risky Files and Rollback Points

- `server/pkg/site/builtin/catalog.go`：stable kind、旧记录和 CookieCloud host 发现的共同根；先补 registry 兼容测试再改。
- `server/internal/services/site.go`：凭据加密、candidate probe、claim 和下载交接集中处；地址 resolver 不能削弱现有安全边界。
- `server/pkg/site/btrss/*` 及新增 adapter：任何通用化都必须保留每站 download host 限制。
- `server/webui/src/views/ExploreView.vue`：已有搜索会话恢复逻辑；固定 `site_id` 必须进入缓存身份，避免跨上下文串结果。
- `server/webui/src/views/SitesView.vue`：PT、BT、Torznab 三种添加流程共享一个表单，需以测试防止凭据字段串型。
- `server/internal/services/download_manifest.go` 与 `transfer.go`：当前分别选择/复验 TV 文件；必须共享包级 episode facts，避免一层接受、一层拒绝。
- `server/internal/medialibrary/parse.go`：不能用宽泛“任意方括号数字”正则修复，否则会把分辨率、年份和合法数字标题当成集号；必须携带发布证据或包级一致性。
- identity 相关 model/repository/service：迁移期间同时存在旧字段和新 snapshot；必须 add/backfill/read-old-write-new，禁止覆盖 locked revision。
- AI settings/client：包含可回传的加密 API Key、自定义 OpenAI Base URL 与 Google 固定 origin；必须复用 credential reveal 与 SSRF-safe client，不在 handler 内直接发请求，enabled=false 必须在 provider 调用前短路。
- reorganization planner/worker：会移动既有媒体产物；必须先预览、绑定 manifest/rule revision、仅操作托管项，并以本地/115 fixture 验证幂等与回滚。

## Pre-Start Gate

- [ ] 用户已批准最新 PRD/Design/Implementation Summary。
- [ ] `implement.jsonl` 与 `check.jsonl` 各含真实 spec/research 条目。
- [ ] 执行 `task.py start` 前通过 `trellis-before-dev` 重新加载 backend/frontend 规范。
