# Player Server 搜索与入库工作流

## Goal

让 Player 在保持本地优先的前提下，按用户明确动作进入 Server 影视搜索与入库，并复用 Player 现有影视详情体验、实时展示 Server 搜索和入库状态。

## Requirements

### 1. 全局搜索行为

- 用户输入关键词时只搜索 Player 已连接媒体源，不再自动请求 Server 的 TMDB 发现搜索。
- 结果底部显示“从 Server 搜索更多并入库”；只有点击后才请求 Server。
- 未连接 Server 或当前账号无对应 capability 时，按钮禁用/给出明确说明，不把失败混入本地搜索结果。
- 搜索框只保留一个清空 X；对话框关闭使用文字“取消”，消除三个 X 并存。

### 2. Server 影视详情

- Server 发现详情复用 Player 本地海报详情的视觉结构和共享组件，不维护一套割裂页面。
- 播放主动作替换为下载入库；保留“搜索”“直接搜索（标题或 TMDB ID）”“订阅”。
- 操作使用分步流程：搜索方式 -> 站点 -> 资源 -> 下载器/媒体库/分类规则或订阅选项 -> 确认。
- 站点弹窗支持快速全选/取消全选、健康状态、禁用原因和失败单站重试。
- 页面读取 Server 持久 acquisition/coverage/follow 状态，展示正在搜索、下载、整理、入库、已入库、订阅、失败和电视剧集覆盖。

### 3. 实时搜索进度

- Player 通过带 Bearer token 的 Tauri 原生流式桥接消费 Server SSE，不能用缓冲 JSON 伪造进度。
- 搜索时显示完成站点数/总站点数的进度条、pending/running/succeeded/failed、刚完成站点以及已发现资源数。
- 每个 site 事件到达即增量展示资源；最终按 Server 提供的优先级稳定排序。
- 退出页面、发起新搜索或点击取消时关闭流并取消 Server 请求，旧事件不得污染新搜索。
- 单站失败不清空成功结果，搜索结束后可重试失败站点。

### 4. 延迟和错误隔离

- 详情首屏只加载详情；coverage 独立降级。
- 站点列表和下载选项在用户点击搜索/入库步骤时再加载。
- 不在每个操作前重复 `source.test()`；连接状态由受控请求结果更新。
- Server、TMDB、普通 JSON、站点 SSE 使用不同的合理超时/空闲超时；错误文案区分连接、权限、TMDB 和站点超时。

## Non-goals

- 未连接 Server 时，Player 的本地/已有媒体源搜索与播放必须照常工作。
- Player 不保存 Server 站点凭据、下载 URL 或管理会话。
- 本任务不复制 Server 的业务排序、权限判断或 acquisition 状态机到 Player。

## Acceptance Criteria

- [x] 输入搜索不会产生 Server/TMDB 请求，只有点击底部按钮才进入 Server 发现。
- [x] 搜索弹窗只有一个清空 X 和一个“取消”，键盘及焦点行为可用。
- [x] 无 Server、断连和无 capability 三种状态分别有清晰表现，不能创建入库。
- [x] Server 详情与本地详情共享主体组件，搜索/直接搜索/订阅和分步入库可完成。
- [x] 多站点交错完成事件时，进度单调、资源增量出现、最终排序稳定且 result_count 正确。
- [x] 取消、新搜索和离开页面都会终止旧流，迟到事件不会改变当前 UI。
- [x] 单站失败保留其他结果并能只重试失败站点。
- [x] 首屏不再串行执行 test/详情/站点/下载选项/coverage，coverage 失败不导致白屏。
- [x] Player typecheck、lint、build、ServerDataSource 验证及 Rust tests/Clippy 通过。
