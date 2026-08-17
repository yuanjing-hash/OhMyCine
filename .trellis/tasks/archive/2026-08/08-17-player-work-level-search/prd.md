# 修复作品级搜索结果层级

## Goal

让顶层搜索返回可浏览的电影和电视剧作品，而不是把电视剧的每一集作为独立搜索结果平铺。

## Confirmed Facts

- Android 截图显示 Emby 搜索结果由 Episode 卡片占据，卡片带“第 N 集”和单集播放按钮。
- `player/src/services/datasource/emby.ts:463` 当前在 `/Users/{UserId}/Items` 搜索中传递 `IncludeItemTypes=Movie,Series,Episode`。
- Jellyfin 复用 `EmbyDataSource('jellyfin')`，因此相同查询契约会同时影响两个数据源。
- `player/src/views/SourceLibraryView.vue:550` 将 `source.search(keyword)` 的返回值直接交给媒体网格，页面本身没有作品级归并。
- 跨源搜索 `player/src/services/datasource/searchAggregation.ts` 目前只按 `sourceId:id` 去重，不会把 Episode 归并为 Series。

## Requirements

- Emby/Jellyfin 顶层搜索优先请求并返回 Movie 与 Series 作品实体。
- 顶层网格不得显示 Episode/Season 作为独立作品；若某数据源只提供文件级结果，则保留可用结果，但已识别电视剧必须复用其 Series 分组。
- 电视剧卡片使用 Series 海报、标题和详情路由；电影卡片保持现有行为。
- 聚合搜索和单数据源搜索使用一致的作品层级规则，避免一个页面修好而另一个页面仍展开单集。
- 结果限制、错误隔离、数据源身份和已有媒体操作能力不得退化。

## Acceptance Criteria

- [x] Emby 和 Jellyfin 搜索“哆啦A梦”不再返回大量 Episode 卡片。
- [x] 同名电视剧与电影能够同时作为独立作品显示，并进入各自正确详情页。
- [x] 跨数据源搜索不会重新引入 Episode 平铺。
- [x] 原始媒体源中已刮削/分组的电视剧仍展示为 Series；无法识别的普通文件仍可按现有能力搜索。
- [x] 搜索失败隔离、空结果和结果数量限制保持正常。

## Out of Scope

- 在顶层结果中提供单集全文搜索；该能力归属剧集详情页子任务。
- 修改 Emby/Jellyfin 服务器端索引或元数据。
