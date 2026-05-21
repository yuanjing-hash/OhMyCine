# 修正 Player 分集 Rail 选择交互

## Goal

修正媒体详情页分集 rail 的文案和交互模型：标题使用朴素的“分集”，左右箭头像键盘左右键一样切换当前选中的集，而不是翻页；用户按 Enter 或点击当前选中集后进入详情，且下方进度/指示条随选中集同步变化。

## Requirements

- 分集区标题从 `沉浸分集` 改为 `分集`。
- 左右箭头语义改为“选择上一集/下一集”，行为等同键盘左右方向键。
- 分集 rail 维护一个当前选中的 episode index。
- 选中项变化时，当前可见窗口应自动跟随，保证选中卡片可见。
- 点击非当前选中卡片时先切换选中项；点击当前选中卡片进入详情。
- 当前选中卡片按 Enter 进入详情；左右方向键切换选中集。
- 下方条/指示器随当前选中集变化，展示当前选中集在整季中的位置。
- 保留播放按钮、详情按钮、继续播放状态、进度条、季切换重置等既有能力。

## Acceptance Criteria

- [ ] 分集标题显示为 `分集`，不出现 `沉浸分集`。
- [ ] 左右箭头每次切换上一集/下一集，而不是跳过一组。
- [ ] 键盘左右键能切换当前选中集；Enter 能进入当前选中集详情。
- [ ] 当前选中集有明确视觉状态。
- [ ] 点击未选中卡片只改变选中项；点击已选中卡片进入详情。
- [ ] 当前选中项变化时，下方指示条同步变化。
- [ ] 切换季后重置到该季第一集。
- [ ] typecheck、lint、build、Windows Tauri 打包通过。

## Definition of Done

- 更新 `MediaDetailView.vue` 的 rail 状态、按钮、键盘和点击行为。
- 若 UI 合同有变化，更新 component guidelines。
- 运行验证命令并通过检查代理复核。

## Technical Approach

- 将现有 `episodeWindowStart` 保留为可见窗口起点，新增/使用 `selectedEpisodeIndex` 作为当前选中集。
- 左右箭头和键盘方向键调用同一个选择函数，移动 `selectedEpisodeIndex ± 1`。
- 选中项超出当前窗口时调整 `episodeWindowStart`，让当前集始终在可见窗口内。
- 用选中 index 计算指示条宽度/位置；卡片上用 `aria-selected`、视觉 class 和 Enter 处理可访问行为。

## Out of Scope

- 不新增 DataSource 分页接口。
- 不改变播放队列、播放历史、Emby 同步逻辑。
- 不重做整个详情页视觉。

## Technical Notes

- 重点文件：`player/src/views/MediaDetailView.vue`。
- 相关规范：`.trellis/spec/frontend/component-guidelines.md`。
