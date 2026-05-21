# 系列详情主按钮播放当前/续播分集

## Goal

系列/电视剧详情页顶部不再只显示“选择季和分集后播放”的静态提示；当页面已经有当前季和当前选中分集时，顶部主操作应变成可点击播放按钮。没有播放历史时默认从第一集开始；有历史记录时默认选中并继续播放历史对应分集/位置。

## Requirements

- 系列详情页顶部应显示可点击的播放按钮，而不是“选择季和分集后播放”提示。
- 没有本地/Provider 可续播历史时，默认选中当前季第一集，顶部按钮播放第一集。
- 当前季分集加载出可续播历史时，默认选中该历史分集，顶部按钮显示“继续播放”并从历史位置开始。
- 用户手动切换分集后，顶部按钮播放当前选中分集。
- 保留分集 rail 的左右箭头、键盘、Enter、点击卡片、指示条快速定位、播放按钮、详情按钮、继续播放状态和队列能力。
- 切换季后按该季分集重新选择：有历史选历史，无历史选第一集。

## Acceptance Criteria

- [ ] 系列详情页顶部不再显示“选择季和分集后播放”。
- [ ] 系列详情页存在分集时，顶部主按钮可点击播放。
- [ ] 无历史记录时，默认选中第 1 集，顶部按钮播放第 1 集。
- [ ] 有可续播历史时，默认选中历史分集，顶部按钮显示“继续播放”并传入 resumePosition。
- [ ] 用户切换选中分集后，顶部按钮播放新的当前选中分集。
- [ ] 切换季后选择状态按该季历史/第一集重置。
- [ ] typecheck、lint、build、Windows Tauri 打包通过。

## Definition of Done

- 更新 `MediaDetailView.vue` 的系列主播放按钮和默认分集选择逻辑。
- 如 UI 合同变化，更新 component guidelines。
- 运行验证命令并通过检查代理复核。

## Technical Approach

- 复用现有 `selectedEpisodeIndex`、`selectedEpisode`、`playItem(item)`、`resumePositionForItem(item)` 和 episode playback progress map。
- 新增系列主按钮 label / disabled / action computed：系列时目标为当前 `selectedEpisode`。
- 在当前季分集加载并刷新进度后，选择该季第一个可续播分集；如果没有可续播分集，保持/重置到第 1 集。
- `playItem(selectedEpisode)` 继续使用现有播放队列构建，确保上下集/队列能力不丢失。

## Out of Scope

- 不新增跨季全库历史扫描接口。
- 不改变播放队列、播放历史存储或 Emby 同步合同。
- 不重做系列详情页视觉结构。

## Technical Notes

- 重点文件：`player/src/views/MediaDetailView.vue`。
- 相关规范：`.trellis/spec/frontend/component-guidelines.md`。
