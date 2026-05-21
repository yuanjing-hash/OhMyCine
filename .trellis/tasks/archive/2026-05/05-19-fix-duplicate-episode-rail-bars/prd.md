# 修正 Player 分集 Rail 双条显示

## Goal

分集 rail 当前同时显示横向滚动条和选中位置指示条，视觉上出现两个条。保留用户认可的选中进度指示条，隐藏卡片容器自身的横向滚动条，让交互视觉更干净。

## Requirements

- 分集 rail 底部只保留选中位置指示条。
- 隐藏卡片横向滚动容器的浏览器原生/自定义 scrollbar。
- 保留左右箭头、键盘左右键、Enter、点击选中/进入详情的既有交互。
- 保留横向窗口化渲染和选中卡片自动可见能力。

## Acceptance Criteria

- [ ] 分集 rail 下方不再出现两个条。
- [ ] 选中位置指示条仍随当前选中集变化。
- [ ] 卡片容器仍可由左右箭头/键盘/选中项变化滚动到可见位置。
- [ ] typecheck、lint、build 通过。

## Definition of Done

- 更新 `MediaDetailView.vue` 的 episode strip scrollbar 样式。
- 运行前端验证命令。
- 完成 Trellis 任务。

## Technical Approach

- 保留 `overflow-x-auto` 以便 `scrollIntoView` 和横向布局继续工作。
- 对 `.episode-card-strip` 隐藏 scrollbar（Firefox/Chromium/WebKit），不移除下方选中指示条。

## Out of Scope

- 不改变分集卡片内容、播放逻辑或 DataSource 行为。
- 不重做 rail 布局。

## Technical Notes

- 重点文件：`player/src/views/MediaDetailView.vue`。
- 相关规范：`.trellis/spec/frontend/component-guidelines.md`。
