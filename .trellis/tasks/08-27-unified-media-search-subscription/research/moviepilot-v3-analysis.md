# MoviePilot v3 搜索、详情与订阅分析

## Evidence scope

- MoviePilot backend: `jxxghp/MoviePilot`, branch `v3`, commit `fcdef31ecf8529fa40f1eb3faac2d05ad0ac475b`.
- MoviePilot frontend: `jxxghp/MoviePilot-Frontend`, branch `v3`, commit `d8a843d5eb97a3c4b82e49911408edd0f4f96b3c`.
- 2026-08-27 使用浅克隆逐文件分析。本文只提炼产品流和边界，不复制 GPL 项目实现代码。

## 1. MP 实际搜索路径

MP 把“搜索媒体身份”和“搜索站点资源”明确分成两种入口：

1. 全局搜索默认调用媒体搜索。`SearchBarDialog.vue:530-556` 将关键词导航到 `/browse/media/search`，携带媒体类型和选择的数据源。
2. `/browse/media/search` 调用 `media/search` 返回海报卡片；卡片以 `media_source + media_id` 作为稳定身份，点击进入统一媒体详情。
3. 只有用户显式选择资源搜索时才进入 `/resource`。详情页的 `handleSearch()` 也不会只传标题，而是把 `media_source + media_id + type + title + year + season` 一起传给资源页（`MediaDetailView.vue:701-718`）。
4. 资源页识别出稳定媒体身份后调用 `search/media/{media_id}`；没有稳定身份时才调用 `search/title` 做自由文本模糊搜索（`resource.vue:922-945`）。

这正是 OhMyCine 当前缺的一层：默认入口应先把字符串解析成海报/作品身份，再从身份进入资源搜索。

## 2. MP 多名称聚合的真实行为

`SearchChain.__prepare_params()` 按顺序生成并去重查询词：

```text
title → names[] → original_title → en_title → hk_title → tw_title → sg_title
```

对应源码为 `app/chain/search.py:1209-1224`。数量受 `max_search_name_limit` 限制；v3 默认值为 3。搜索前还会调用 `supplement_media_info()` 聚合启用的数据源别名（同步 `:1714`，异步 `:1811`，流式 `:1904`）。

`search_multiple_name` 控制行为：

- 开启：遍历受限的全部名称并合并结果。
- 关闭：某个名称已有结果后停止继续搜索（`:1748`, `:1844`, `:1951`）。
- v3 源码默认值是 `false`（`app/application/configuration.py:189`）；用户实例观察到同时搜索多语言，说明对应实例开启了该设置或使用了会聚合名称的配置。

MP 的关键不只是“多搜几次”，而是先有可信媒体身份与完整别名，再把所有候选统一送入作品、季集和规则匹配。OhMyCine 应采用相同产品原则，同时复用自身站点限速、流式分组和 opaque result token。

## 3. MP 媒体库状态展示

媒体详情加载后会并行读取三类事实：

- `mediaserver/exists`：作品是否已入库，可跳转播放。
- `mediaserver/notexists`：每季的已有集数、总集数和缺失状态（`MediaDetailView.vue:339-357`）。
- `mediaserver/exists_remote`：展开季后标记具体已存在集（`:283-299`）。

每季状态分为：

- `已入库`
- `部分缺失`
- `缺失`

详情展开到逐集时，已有集显示明确勾选。MP 同时支持 TMDB 默认季序和 episode group。OhMyCine 本期可先做 TMDB 默认季序，但必须保留稳定的 season/episode 集合契约，不能只显示一个粗略百分比。

## 4. MP 订阅界面与数据表达

详情页的电视剧订阅不是一个立即执行按钮，而是进入季选择界面：

- `SubscribeSeasonDialog` 列出各季、总集数、入库状态和已订阅状态。
- 用户可以一次选择多季。
- MP 还提供普通订阅、分集洗版和全集洗版；本项目需求只需要普通缺集订阅。
- `SubscribeCard` 用 `total_episode - lack_episode` 展示下载/补齐进度。
- 编辑界面允许覆盖 sites、downloader、save_path、filter groups 等运行配置。

对 OhMyCine 的直接启发是：详情页负责选择作品与季，订阅管理页负责展示进度和编辑自动化参数；下载器/媒体库不能藏在不可见的全局魔法里，至少要在订阅详情中可检查和修改。

## 5. MP 自动订阅管线

MP 的订阅搜索把以下环节串起来：

```text
订阅记录 → 媒体服务器缺集事实 → SearchChain 精确搜索 → 资源过滤/优先级 → DownloadChain 批量下载 → 整理/入库 → 更新 lack_episode
```

证据：

- `SubscribeChain` 是订阅编排入口（`app/chain/subscribe.py:205`）。
- 资源提交复用 `DownloadChain.batch_download()`，没有另建下载旁路（`:482`, `:495`）。
- 搜索调用共享 `SearchChain`（`:1243`）。
- 缺集对账通过 `get_no_exists_info` 获取媒体服务器事实（`:3445`）。
- `lack_episode` 随入库/下载事实持续更新（`:2191-2199`, `:2530-2550`, `:3694`）。
- API 同时支持搜索所有订阅和按订阅立即搜索（`app/api/endpoints/subscribe.py:425-455`）。

## 6. OhMyCine 当前差距

- `DiscoveryDetailView` 已有稳定 TMDB 身份，但详情按钮仍把用户直接送到 PT 资源页。
- `ExploreView` 只承担资源搜索、识别候选和下载提交，没有媒体海报搜索模式。
- `MediaLibraryEntry` 已有 `tmdb_id/season/episode`，足以构建覆盖率读模型；目前只按单媒体库 catalog 暴露，详情页没有跨库聚合。
- 现有 `SiteService` 已提供多站点并发搜索、限速、短期 `result_token` 和 `DownloadService` 交接，应扩展“媒体身份搜索会话”，不应替换该安全边界。
- 现有持久 Job/Worker/Scheduler 和下载→Transfer→入库链路已经成熟，follow 应新增编排 Worker，并通过内部受校验接口提交现有下载任务。
- `follows.*` 权限与导航已经预留，模型、服务、API 和真实页面尚未落地。

## 7. Recommended adoption boundary

应该学习 MP 的：

- 先媒体海报搜索、再作品详情、最后资源搜索的默认路径。
- 稳定媒体身份驱动的多语言/别名聚合。
- 详情页按季/集展示媒体库覆盖率。
- 详情页季选择 + 管理页进度/配置的订阅体验。
- 订阅复用现有搜索、下载和入库链路。

不应照搬的：

- MP 的全部洗版、分享、热门订阅、音乐订阅复杂度。
- 只依赖媒体服务器 API 的缺集算法；OhMyCine 的权威事实首先来自自己的 MediaLibrary catalog，外部 Emby/Jellyfin 只作为刷新目标或未来补充来源。
- MP 的具体代码结构和接口命名；OhMyCine 应遵循 Go service、现有 Job queue、权限目录、opaque result token 和安全 DTO 规范。
