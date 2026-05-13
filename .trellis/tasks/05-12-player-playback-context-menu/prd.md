# Player 播放详情与右键菜单

## Goal

为播放页提供自定义右键菜单或等效播放详情菜单，展示当前播放信息和常用动作，替代浏览器默认菜单并保持沉浸式体验。

## Requirements

- 播放视频区域右键打开自定义菜单，或在播放设置面板中提供等效详情入口。
- 菜单展示媒体标题、来源、当前时间/总时长、播放状态、渲染 backend/状态等安全信息。
- 菜单提供常用动作入口：播放/暂停、复制安全化媒体标识或标题、打开诊断、进入设置/主页等。
- 菜单不展示 tokenized URL、未脱敏路径或 native handle。
- 菜单打开期间控制层不自动隐藏。

## Acceptance Criteria

- [ ] 默认浏览器右键菜单不再作为播放页主要 UX。
- [ ] 自定义菜单/详情面板可打开关闭，并显示安全播放详情。
- [ ] 常用动作可用且不会破坏播放状态。
- [ ] 敏感 URL/token/path 已脱敏或不展示。
- [ ] `npm run typecheck` / `npm run lint` / `npm run build` 通过。

## Out of Scope

- 复杂开发者调试面板替代品。
- 分享/外部打开/文件系统定位等高风险动作。

## Technical Notes

- Parent task: `05-12-player-playback-controls`.
- Likely files: `VideoPlayer.vue`, `PlayerView.vue`, `useMpv.ts`, datasource error/redaction helpers.
