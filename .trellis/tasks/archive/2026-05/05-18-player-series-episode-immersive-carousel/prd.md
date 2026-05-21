# 重设计 Player 剧集分集沉浸式懒加载卡片

## Goal

将 Player 媒体详情页中的剧集分集区域从纵向全量罗列，改成符合 OhMyCine Cinema OS 的沉浸式横向卡片/轮播体验：参考 Emby 官方分集浏览的信息结构，但使用暗色、液态玻璃、artwork-first、悬浮/渐隐控制的项目 UI 语言，减少长剧集页面噪音并提升浏览和播放效率。

## What I already know

- 用户明确不希望多集剧把所有分集直接纵向列在页面下面。
- 用户希望改成横向、沉浸感强的卡片设计，左右按钮切换。
- 用户希望支持懒加载，避免一次把所有分集全部铺开渲染。
- 用户希望“学一下 Emby 官方设计”，但最终视觉必须符合 OhMyCine 现有 UI。
- 现有详情页已经有剧集/季/分集数据、播放、详情跳转、本机续播状态和分集进度条能力。

## Assumptions (temporary)

- MVP 做客户端 UI 层懒渲染/横向分页，不改 DataSource API。
- 已加载到前端的当前季分集数据仍来自现有 `source.list(seasonId)`；“懒加载”指可视窗口渲染与分页浏览，而不是新增 provider 分页接口。
- 分集卡片保留播放、详情、继续播放状态、缩略图、集数、标题、时长、简介。

## Decision (ADR-lite)

**Context**: 当前 DataSource 只有 `list(path?: string)`，Emby/Jellyfin 虽支持 API 分页，但跨 DataSource 分页合同会扩大任务边界。

**Decision**: MVP 先做前端横向 rail + 窗口化/按页增量渲染，不新增 provider 分页接口。

**Consequences**: 视觉和交互问题能快速解决；极长季仍会先由 provider 拉回当前季数据，但不会一次在 DOM 中渲染所有富卡片。后续如要真分页，可单独扩展 DataSource contract。

## Research References

- [`research/episode-carousel-patterns.md`](research/episode-carousel-patterns.md) — 推荐桌面剧集详情使用 hero-led、16:9 横向分集 rail、轻量季切换、hover/focus 控制和增量渲染。

## Requirements (evolving)

- 剧集详情页的分集区域改为横向沉浸式卡片/轮播，不再默认纵向铺满所有集数。
- 分集卡片应突出缩略图/剧照，保留标题、集数、时长、小简介、继续播放状态和播放入口。
- 分集区域提供左右切换按钮，并能通过横向滚动浏览。
- 长分集列表应避免一次渲染全部卡片；至少按窗口/页增量显示。
- 季选择仍保留，并切换当前季后刷新轮播内容与续播状态。
- UI 使用现有 Cinema OS / liquid-glass tokens，不引入新的视觉语言。

## Acceptance Criteria (evolving)

- [ ] 系列详情页的分集不再以两列/纵向列表全量展示。
- [ ] 分集以横向 artwork-first 卡片展示，支持左右按钮切换。
- [ ] 长季集数只渲染/展示当前窗口附近或按页增量加载，而不是一次铺开全部。
- [ ] 分集卡片保留 `播放本集/继续播放`、详情、简介、缩略图、进度条状态。
- [ ] 季切换后轮播回到开头并加载对应季分集。
- [ ] 空状态、加载状态、错误状态仍清晰可见。
- [ ] Typecheck、lint、build、Windows Tauri 打包通过。

## Definition of Done

- 更新 MediaDetailView 的分集布局与交互。
- 如需要，抽取小型局部组件，但优先避免不必要抽象。
- 更新相关 frontend component guideline/code-spec（若新增可复用布局合同）。
- 运行验证命令。

## Out of Scope (explicit)

- 不新增服务端/API 分页合同，除非调研发现现有 DataSource 已支持且改动很小。
- 不改变播放队列、播放历史、Emby 进度同步合同。
- 不重做整个媒体详情页 hero 区。
- 不实现移动端专门适配；保持桌面优先。

## Technical Notes

- 重点文件：`player/src/views/MediaDetailView.vue`。
- 相关规范：`.trellis/spec/frontend/component-guidelines.md`，其中要求媒体详情页区分剧集与电影，Cinema OS 暗色液态玻璃、artwork-first、hover-revealed chrome。
- 需要研究 Emby/类似媒体库的分集浏览结构，并映射为 OhMyCine UI。