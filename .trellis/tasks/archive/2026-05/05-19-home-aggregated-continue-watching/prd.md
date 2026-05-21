# 首页继续观看聚合与图片修正

## Goal

首页是聚合页，继续观看区域不应显示“本机继续观看”这种让用户误解为只包含本机历史的标题。该区域应聚合本机历史和 Provider（Emby/Jellyfin 等）继续观看，并确保继续观看卡片优先显示可用图片：电影显示海报/背景图，剧集/单集优先显示本集图/剧照，缺失时再 fallback，不出现空黑块。

## Requirements

- 首页继续观看区域标题统一为“继续观看”，不显示“本机继续观看”。
- 继续观看区域应合并本机播放历史和 Provider 返回的 continue watching 项。
- 本机历史和 Provider 项应去重，避免同一个 source/item 重复出现。
- 排序应优先保留本机最近历史，其次保留 Provider 顺序或可用更新时间。
- 继续观看卡片必须尽量显示图片：优先 `backdropUrl`，其次 `posterUrl`；剧集/单集应优先使用该集图/剧照或已有 episode backdrop/poster。
- 图片缺失时显示带标题的 fallback，不应是无信息空黑块。
- 副标题可以保留“本机记录 · Emby”这类来源信息，但区域标题不能限定为本机。
- 保留点击继续播放、进度条、首页聚合布局和 Player 独立优先原则。

## Acceptance Criteria

- [ ] 首页继续观看标题显示为“继续观看”，不出现“本机继续观看”。
- [ ] 本机历史和 Provider 继续观看能出现在同一继续观看区域。
- [ ] 同一 source/item 不重复显示。
- [ ] 继续观看卡片有可见图片或带标题 fallback，不出现无信息黑块。
- [ ] 剧集/单集卡片优先使用该集图片。
- [ ] 点击继续观看仍进入播放器并携带 resumePosition。
- [ ] typecheck、lint、build、Windows Tauri 打包通过。

## Definition of Done

- 更新 `datasource` store 的首页 continue watching 合并逻辑。
- 更新 `HomeView.vue` 的继续观看卡片图片/fallback 展示。
- 如 UI 合同变化，更新 frontend component guidelines。
- 运行验证命令并通过检查代理复核。

## Technical Approach

- 在 `loadHomeSections()` 中查找 Provider continueWatching section，与本机 local continue section 合并成统一 `continueWatching` section。
- 对合并结果按 `sourceId:item.id` 去重，本机历史优先覆盖 Provider 的 resume/progress，本机缺失图片时可继承 Provider 图片。
- `toContinueWatchingMediaItem` 已会带本地存储的 poster/backdrop；HomeView 继续观看卡片使用 `backdropUrl ?? posterUrl`，无图时渲染标题 fallback。
- 保持副标题来源表达为“本机记录 · <source>”或 provider source 名称。

## Out of Scope

- 不改播放历史 DB schema。
- 不新增 Provider API。
- 不重做首页整体布局。

## Technical Notes

- 重点文件：`player/src/stores/datasource.ts`、`player/src/views/HomeView.vue`。
- 相关规范：`.trellis/spec/frontend/component-guidelines.md`、`.trellis/spec/frontend/type-safety.md`。
