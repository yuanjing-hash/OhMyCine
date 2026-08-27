# 技术设计

## 1. 服务边界

在现有 Discovery、TMDB、SiteService 和 MediaLibraryService 上增加三个可复用能力，不引入新的大一统 service：

- `MediaSearch`：调用 TMDB 候选搜索并投影为现有 `DiscoveryWork`/同源图片。
- `MediaIdentitySearch`：验证 TMDB 身份、生成查询名称并编排现有站点搜索/识别/去重。
- `MediaCoverage`：从 TMDB 季集快照和 actor 可读的 media-library entries 构建只读覆盖率。

HTTP handler 只解析参数、actor 和标准响应；所有权限、网络、聚合和安全投影在 service 层。

## 2. API 契约

计划新增：

```text
GET /api/v1/discovery/media-search
  ?query=<text>&media_type=all|movie|tv&page=<n>

GET /api/v1/discovery/media/{mediaType}/{tmdbID}/coverage

GET /api/v1/discovery/media/{mediaType}/{tmdbID}/torrent-search
GET /api/v1/discovery/media/{mediaType}/{tmdbID}/torrent-search/stream
  ?site_ids=<bounded list>&season=<optional>
```

资源搜索响应复用现有 `SiteSearchGroup/SiteSearchResult` 安全字段并增加安全的 `query_names`/`matched_name` 摘要；SSE 事件顺序和终态与现有 torrent search 一致。旧 `/discovery/torrent-search*`、兼容 `/pt-search*` 和下载确认 API 不变。

如果现有路由风格或 handler 测试要求不同命名，实施时可以做等价调整，但必须保持“身份 endpoint 与原始 keyword endpoint 分离”的契约。

## 3. 名称生成和搜索预算

1. `GetByID(mediaType, tmdbID, zh-CN)` 获取可信快照。
2. 复用/扩展 TMDB candidate enrichment 的 alternative titles 和 translations，补取 `en-US` 详情或翻译以获得英文名。
3. 顺序建议：本地化标题 → zh-CN/zh-TW/zh-HK/zh-SG 别名 → 原名 → 英文名 → 其它替代标题。
4. 使用规范化 key 做去空、折叠空白、Unicode 兼容和不区分大小写去重；显示值保持原文。
5. 默认最多 6 个名称并保留更低的可配置默认，硬上限不可被 HTTP 参数放大；单名称沿用站点查询长度限制。
6. 将每个名称送入共享 SiteService 内部搜索，沿用站点限速、有界并发、context 取消和每站分页/结果上限。

聚合键优先使用 `site_id + adapter resource identity/torrent id/info hash`；缺少稳定 ID 时使用受限 canonical title/size/published fingerprint。去重后才铸造或保留一个 actor-bound opaque claim，避免浏览器拿到真实来源。

稳定排序先按现有识别匹配质量，再按站点输入优先级、seeders、发布时间、大小和稳定 identity tie-break；禁止用 goroutine 完成顺序作为排序因素。

## 4. 覆盖率算法

### 4.1 输入

- 服务端验证的 TMDB `Snapshot` 和每季 `EpisodeSnapshot`。
- actor 可读取、enabled 且最近一次扫描状态可解释的 `MediaLibrary`。
- `MediaLibraryEntry` 中已确认的 `tmdb_id/media_type/season/episode`；只使用确定 identity，忽略 provisional/unrecognized 条目。

### 4.2 投影

```text
MediaCoverageDTO
  identity
  status
  freshness { checked_at, library_scan_state, tmdb_state }
  libraries[] { id, name }
  movie { present }
  tv {
    totals
    seasons[] {
      season_number, name, poster_url, status, counts
      episodes[] { episode_number, air_date, status, library_ids[] }
    }
  }
```

逻辑集键为 `(tmdb_id, season_number, episode_number)`；多库或重复文件只增加 `library_ids`，不增加 logical present count。`air_date` 晚于当前日期为 future；缺失/非法 air date 为 unknown。只有已播日期明确且 catalog 没有该逻辑集时才是 missing。

扫描从未成功、扫描为 partial 且目标事实无法证明、条目身份不完整或 TMDB season endpoint 失败时保守返回 unknown。覆盖率可以短缓存，但 cache key 必须包含 actor 可读库集合及其 scan generation/update marker，不得跨用户复用权限投影。

### 4.3 Season 0

特别篇保留逐集事实并标记 `special=true`；全剧普通缺集 totals 默认排除。API 不删除 Season 0，供订阅页显式选择。

## 5. Web UI

- 新增 typed discovery media-search/coverage client 和纯函数：路由构造、状态标签、季/集计数、搜索名称展示。
- 探索页默认展示媒体搜索模式；高级资源搜索通过清晰 tab/button 切换，并保持查询参数可分享。
- 扩展而不是复制现有详情页：作品 hero、操作区、相关推荐/类似作品沿用；新增覆盖率 section 和可展开季卡。
- 资源搜索继续进入/复用 Explore 结果体验，携带稳定身份或使用新的 identity search client；路由状态不保存真实 token 之外的来源信息。
- 响应式、键盘焦点、loading/empty/error/unknown 状态均加入组件测试和必要的浏览器视觉检查。

## 6. 测试与文档

- TMDB 名称顺序、地区别名、英文名、Unicode 去重、上限和部分 provider 失败。
- 多名称 × 多站取消/失败、跨别名去重、稳定排序和 opaque claim 安全。
- 电影存在、跨库剧集去重、partial scan、未播、无日期、Season 0、无权限库。
- handler/router/RBAC/安全字段、Vue route/client/coverage rendering、默认/高级搜索切换。
- 更新 `docs/architecture/02-server-design.md`、`06-roadmap.md`；若实施时存在 OpenAPI 则同步。

## 7. 回滚

本任务不需要持久业务表。出现问题时可以把 Web UI 默认入口切回高级资源搜索并停用新增身份/覆盖率路由；旧推荐详情、原始搜索、下载和 catalog 均不受影响。

