# 首页系列聚合主按钮播放/续播分集

## Goal

首页聚合/轮播展示系列内容时，左侧主按钮不应只是“查看详情”。它应与系列详情页一致：有播放历史则显示继续播放并从历史对应集/位置开始；没有历史记录则从第 1 集开始播放。右侧“详情”按钮保留跳转详情能力。

## Requirements

- 首页聚合/轮播中的系列主按钮应作为播放入口。
- 对系列：有可续播历史时，主按钮显示“继续播放”并播放历史分集/位置。
- 对系列：没有历史时，主按钮显示“播放”并从第 1 集开始播放。
- 右侧“详情”按钮继续跳转媒体详情页。
- 播放系列分集时应保留播放队列/上下集能力。
- 非系列媒体保持现有播放/详情行为。
- Provider 不返回分集或播放地址不可用时应显示已有错误提示，不破坏首页。

## Acceptance Criteria

- [ ] 首页系列 hero/聚合卡片左侧主按钮不再显示“查看详情”。
- [ ] 系列有历史记录时主按钮显示“继续播放”，并跳转 Player 播放历史对应分集与位置。
- [ ] 系列无历史记录时主按钮显示“播放”，并从第 1 集开始。
- [ ] 详情按钮仍进入系列详情页。
- [ ] 播放时带上 episode queue/context，使上下集/队列可用。
- [ ] 非系列电影/单集播放不回归。
- [ ] typecheck、lint、build、Windows Tauri 打包通过。

## Definition of Done

- 更新首页聚合/轮播主按钮行为与 label。
- 若 UI 合同变化，更新 frontend component guidelines。
- 运行验证命令并通过检查代理复核。

## Technical Approach

- 检查 `HomeView.vue` 和首页 hero/聚合组件的 `play/select/open-detail` 事件。
- 对 `series` 类型主播放：解析该系列的 seasons/episodes，优先找到可续播分集，否则选第 1 集。
- 复用 `getPlaybackProgress`、`shouldResumePlayback`、`createPlaybackQueue`、`savePlaybackMediaContext` 和现有 Player route query。
- 对非系列继续走现有播放逻辑。

## Out of Scope

- 不新增跨数据源分页接口。
- 不改变播放历史/Emby 同步合同。
- 不重做首页视觉布局。

## Technical Notes

- 重点文件：`player/src/views/HomeView.vue`，可能涉及 `player/src/components/media/HeroCarousel.vue`。
- 相关规范：`.trellis/spec/frontend/component-guidelines.md`、`.trellis/spec/frontend/type-safety.md`。
