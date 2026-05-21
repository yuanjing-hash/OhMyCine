# 让 Player 分集位置指示器支持快速定位

## Goal

分集 rail 底部只保留 OhMyCine 自己的分集位置指示器，并让它同时承担 scrollbar / 快速定位能力：用户点击或拖动下方条即可切换当前选中集，视觉上保持一条干净的进度/定位控件。

## Requirements

- 继续隐藏卡片容器原生 scrollbar。
- 下方分集位置指示器同时作为当前选中集位置展示和快速定位控件。
- 点击指示条任意位置时，根据点击位置切换到对应集。
- 拖动指示条时，当前选中集随拖动位置变化。
- 指示条变化应复用现有选中集逻辑，保证可见窗口自动跟随选中卡片。
- 保留左右箭头、键盘左右键、Enter、点击卡片选中/进入详情、播放按钮、详情按钮、继续播放状态和单集进度条。
- 指示条应有可访问标签，能表达当前选中集在整季中的位置。

## Acceptance Criteria

- [ ] 分集 rail 底部只出现一条 OhMyCine 指示/定位条。
- [ ] 点击该条左/中/右不同位置能跳到对应集。
- [ ] 按住拖动该条时能连续切换当前选中集。
- [ ] 当前选中卡片仍有明确视觉状态，并自动保持可见。
- [ ] 左右箭头和键盘左右键仍每次切换一集。
- [ ] Enter 或点击当前选中卡片仍进入详情。
- [ ] 切换季后仍重置到第一集。
- [ ] typecheck、lint、build、Windows Tauri 打包通过。

## Definition of Done

- 更新 `MediaDetailView.vue` 的指示条交互和样式。
- 若形成可复用 UI 合同，更新 frontend component guidelines。
- 运行验证命令并完成任务。

## Technical Approach

- 保留 `selectedEpisodeIndex` 作为唯一分集选择状态。
- 将指示条容器改成 `role="slider"` 的交互控件，使用 pointer events 计算点击/拖动位置对应的 episode index。
- 位置到集数映射：`round(percent * (episodes.length - 1))`，并通过 `selectEpisodeIndex(index)` 统一更新选中集和可见窗口。
- 继续隐藏 `.episode-card-strip` scrollbar，只展示自定义指示条。

## Out of Scope

- 不恢复浏览器原生 scrollbar。
- 不改变 DataSource、播放队列、播放历史或 Emby 同步逻辑。
- 不重做分集卡片视觉结构。

## Technical Notes

- 重点文件：`player/src/views/MediaDetailView.vue`。
- 相关规范：`.trellis/spec/frontend/component-guidelines.md`。
