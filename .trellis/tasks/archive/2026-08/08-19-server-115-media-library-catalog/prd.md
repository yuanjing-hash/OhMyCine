# 完善 115 媒体库目录、分页与剧集聚合

## Goal

让 115 媒体库和本地媒体库遵守相同的“Storage 受控根 -> MediaLibrary 下级目录 -> 文件索引 -> 作品目录”语义。管理员可以从 115 Storage 根继续选择具体媒体目录，浏览可分页的作品清单，并看到电视剧按 Series -> Season -> Episode 聚合，而不是每个视频文件平铺成一个作品。

## Background

- 当前管理台在 `server/webui/src/views/MediaLibrariesView.vue:217` 和 `:265` 对 `pan115` 只显示“使用数据源云端根目录”，没有进入下级目录的入口。
- 当前 handler 在 `server/internal/handlers/media_libraries.go:202` 明确拒绝 115 的非 `/` 相对根；扫描在 `server/internal/services/media_library.go:941` 始终从 `storage.RootPath` 开始。
- 当前媒体清单固定请求 `limit=500`（`MediaLibrariesView.vue:108`），Service 只有 `Limit` 没有 Offset（`media_library.go:307`），handler 返回的 `total` 是当前切片长度（`media_libraries.go:159`），不是数据库总数。
- 当前 `MediaLibraryEntry` 是文件级事实记录（`server/internal/models/models.go:273`），只有 season/episode 字段，没有作品级读模型。管理台直接遍历 Entry，所以剧集逐集平铺。
- Player 已在 `player/src/services/scraper/rawSeriesGrouping.ts` 与 `parser.ts` 建立 path-aware 解析和 Series -> Season -> Episode 聚合；Server 应复用其行为契约，而不是另造冲突规则。
- `DDSRem-Dev/MoviePilot-Plugins` 的 `p115disk` / `p115strmhelper` 从用户配置的网盘媒体目录 ID 开始递归枚举并分批处理。OhMyCine 当前 bulk tree 分页、限速和 partial 保护可以保留，但扫描起点必须改为所选 MediaLibrary 子目录。

## Requirements

### R1. 115 MediaLibrary 子目录

1. 创建或编辑 115 MediaLibrary 时，目录选择器必须从所选 Storage 根开始，并允许进入、选择任意下级目录。
2. 选择令牌必须绑定 actor、Connection、Storage、Storage 根和 provider item ID；不能导航到 Storage 根上方，也不能把另一个 Storage/Connection 的令牌用于当前媒体库。
3. MediaLibrary 同时保存稳定的 provider directory ID 和 Storage-relative 显示路径。扫描依赖稳定 ID，路径只用于展示和重叠校验，不保存 Cookie、pickcode 或临时 URL。
4. 既有 115 MediaLibrary 自动兼容：迁移后 provider root ID 回填为对应 `storage.root_path`，相对根保持 `/`，无需删除或重新创建。
5. local MediaLibrary 的签名目录令牌、路径约束和 watcher 行为保持不变。

### R2. 扫描起点与全量语义

1. 115 全量和 reconciliation 从 MediaLibrary 保存的 provider root ID 开始，而不是固定从 Storage 根开始。
2. 继续使用现有 115 bulk-tree lane、分页、限流、最大条目、取消和 `partial=true` 语义；partial 结果不得删除未见旧条目。
3. 扫描产生的 `relative_path` 相对于 MediaLibrary 所选目录，API 不暴露云端账号绝对显示路径。

### R3. 文件索引分页

1. 保留文件级 `/entries` 作为诊断/对账接口，并支持校验后的 `page`、`page_size`、可选搜索和媒体类型过滤。
2. `total` 必须来自匹配条件下的数据库 COUNT，不得使用当前页长度代替。
3. 默认每页 50，允许 20/50/100，服务端硬上限 100；越界页返回空列表和真实 total，非法参数返回稳定 `invalid_request`。

### R4. 作品级目录与剧集聚合

1. 文件索引继续作为扫描事实层；用户可见媒体清单使用独立作品级读模型，不删除逐文件记录。
2. 电影默认一份主视频形成一个 Movie 作品；电视剧按稳定 series key 聚合，同一剧的分集不能跨分页拆成多个作品。
3. Series 详情按 Season 分组，Season 内按 season、episode、文件名稳定排序；无法确定季号的分集进入“未分季”。
4. Server 的路径解析至少覆盖 Player 已支持的 `S01E02`、`1x02`、`EP02`、`第2集/话`、`Season 01`、`S01`、`第1季`，并优先使用“剧名目录/季度目录/分集文件”的父级上下文识别剧名。
5. 作品 key 和必要的 series title 持久化到文件索引并建立复合索引，使 1 万级以上文件库的作品分页不需要把全部 Entry 加载进 Go 内存。
6. 既有 Entry 在迁移时获得兼容 key；下一次 reconciliation 使用新解析器重新计算并收敛。

### R5. 管理台体验

1. “媒体清单”默认展示作品级行，包含标题、类型、季/集或文件数量、分类/匹配、大小和最近修改时间。
2. 电视剧行可展开查看季度及分集；展开只请求当前作品详情，不把整库分集嵌入分页响应。
3. 提供标题搜索、类型筛选、页码、上一页/下一页和每页数量选择；切换媒体库、搜索或筛选时重置到第一页。
4. 加载、空结果、错误、扫描轮询和快速切换媒体库时不得显示旧请求的结果。

## Acceptance Criteria

- [ ] 115 Storage 选择 `/Media` 后，MediaLibrary 可以继续选择 `/Media/TV`；保存后 UI 显示 `/TV`，扫描只索引该目录的后代。
- [ ] 另一个账号、另一个 Storage、过期或越界的 provider selection token 均被拒绝，且响应/日志不泄露凭据或上游路径。
- [ ] 旧 115 MediaLibrary 升级后仍从原 Storage 根正常扫描，不丢失配置和文件索引。
- [ ] 12099 条文件记录的 `/entries?page=2&page_size=50` 返回第二页 50 条且 `total=12099`；非法分页参数返回 400。
- [ ] 同一剧的多季多集在作品清单中只占一条 Series；展开后按 Season -> Episode 展示，电影保持独立作品。
- [ ] `Show/Season 01/Show.S01E01.mkv`、`Show/S01/EP02.mkv` 和中文季集命名均通过自动化测试形成正确 series key、季号和集号。
- [ ] 作品搜索和类型筛选在数据库查询层生效，分页 total 与筛选结果一致。
- [ ] 115 bulk scan 的分页、限流和 partial-preserve 回归测试继续通过。
- [ ] local MediaLibrary 的目录选择、扫描、监听和已有 API 测试继续通过。
- [ ] Server Go 测试、vet/build，以及 Web UI test/typecheck/lint/build 通过。

## Out Of Scope

- 本任务不实现 TMDB 自动匹配、海报墙或元数据编辑；作品聚合使用扫描路径和既有匹配字段。
- 本任务不复制 MoviePilot 插件业务结构，也不替换现有 `SheltonZhu/115driver` 与 bulk-tree 实现。
- 本任务不实现 115 云端移动、重命名、删除、回收站、STRM 投影或生活事件的新能力。
- 本任务不把 Player TypeScript 直接作为 Server 运行时依赖；只对齐可测试的识别行为和用例。

## Technical Constraints

- 保持 `Connection -> Storage -> MediaLibrary` 边界，handler 只解析请求，目录令牌验证和目录身份解析属于 service。
- SQLite 迁移单调递增、幂等且仅新增列/索引；不得重建或清空用户媒体库数据。
- Provider 物理身份只在 Server 内部使用；普通 DTO、日志、审计和 AI 字段不返回 provider root ID。
- 当前工作区存在大量并行 Server 改动，实施必须增量协作，禁止还原或覆盖不属于本任务的变化。
