# 建立媒体操作能力契约与共享菜单

## Goal

建立所有页面和来源共用的媒体动作能力、执行、确认和反馈边界，杜绝页面硬编码菜单及空按钮。

## Requirements

- 定义稳定、无凭据的 `MediaActionTarget` 和 action/capability 类型。
- capability 表达 available/disabled/hidden、禁用原因和危险等级。
- controller 负责 provider adapter、并发防重、确认、错误脱敏和缓存失效。
- 提供桌面定位 popover 与移动响应式 sheet，共用动作分组和可访问性行为。
- 媒体与媒体库对象分别提供适用动作；未实现能力隐藏。

## Acceptance Criteria

- [ ] 首页、搜索、海报墙、详情和来源页使用同一动作解析器。
- [ ] 来源不支持的动作不显示，权限不足显示明确禁用原因。
- [ ] 菜单不会接触临时 URL、Header、Cookie 或凭据。
- [ ] 重复点击不会重复执行 mutation，完成后相关页面刷新。

## Out of Scope

- 各 provider 的全部具体 mutation；由后续子任务逐项接入。
