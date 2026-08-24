# Player Server 媒体库实时刷新

## Goal

让连接 OhMyCine Server 的 Player 安全、近实时并可补偿地获知可用媒体库变更，自动更新聚合首页，同时以不打断浏览和播放的方式提示当前列表换入新数据。

## Requirements

### Secure change delivery

- Player 事件接口只接受现有 `omc_player_` device Bearer，不接受 Cookie、普通管理会话或 query token 替代。
- 每次请求按当前用户权限过滤媒体库；用户停用、密码重置、设备撤销或重新登录后旧 token 不能继续收到变化。
- 事件只包含安全媒体库逻辑 ID、content revision、受控 change kind、时间和 opaque cursor。
- Server 不维护无界逐设备队列；历史过期或 cursor 无效时明确要求该 Server source 全量失效并返回新基线。

### Reconnect convergence

- Player 为每个 ServerDataSource 独立维护 cursor，在线时近实时接收，断线/睡眠/网络切换后自动重连。
- 离线期间遗漏的变化在重连后补齐；Server 重启或历史裁剪后也能通过 resync 收敛。
- 重连采用有界指数退避，不产生紧密循环、并发重复 poll 或跨 source 状态污染。
- 事件端点不支持的旧 Server 不影响浏览，只保留现有 TTL/手工刷新行为并显示为能力不可用。

### Player refresh behavior

- 收到 ready change 后立即失效对应 ServerDataSource 的来源根/媒体库缓存，并后台更新聚合首页。
- 当前正在浏览的受影响列表保持现有内容、滚动、选中状态、面包屑和播放上下文，合并显示一次“媒体库已更新”提示。
- 用户点击提示后原位加载最新逻辑层级；过期请求不能覆盖新结果，刷新成功不强制滚到顶部。
- 用户离开后再次进入时自动读取最新状态，不再要求点击旧提示。
- 非 Server DataSource、当前播放流、字幕/音轨、播放进度和本地缓存不被清理或中断。

### Lifecycle and feedback

- 每个启用且凭据有效的 Server source 启动一个通知控制器；禁用、删除、凭据失效和应用退出时可靠释放。
- 后台网络失败保持 source-scoped，不弹出重复错误；恢复后可显示一次安全的连接恢复/媒体更新反馈。
- 事件消费不持久化 Bearer、媒体详情、播放 URL 或 provider 私有数据。

## Acceptance Criteria

- [ ] 在线 Player 在 ready change 后无需重启/重新连接即可更新首页并收到当前媒体库提示。
- [ ] 点击“媒体库已更新”后当前列表换入最新数据，滚动/选择/导航保持合理稳定且不触发整页重载。
- [ ] 正在播放时收到通知不重载、停止或替换当前 stream，也不覆盖播放进度。
- [ ] Player 离线跨过一个或多个变更后重连能按 cursor 补齐；过旧 cursor 通过 resync 收敛。
- [ ] 多个 ServerDataSource 和多台 Player 设备分别维护状态，慢设备不阻塞其它设备或 Server 流水线。
- [ ] token 撤销、用户停用和权限变化后，旧设备及时停止收到不可见库事件。
- [ ] 事件端点、Player 持久状态、日志和错误不含 Bearer、绝对路径、provider ID、signed URL 或临时上游 URL。
- [ ] 旧 Server 返回 endpoint-not-found 时 Player 保持正常浏览且不无限重试。
- [ ] Windows 与 Android 构建路径通过；测试覆盖后台/恢复、卸载清理、重连退避、事件合并、stale request 和其它 DataSource 隔离。

## Out of Scope

- 通知 Player 刷新其直连 Emby/Jellyfin/OpenList/Alist/CloudDrive2/WebDAV/本地数据源。
- 通用数据源配置、凭据、播放进度或设置同步。
- 系统推送通知、后台常驻服务、邮件/Webhook 或应用完全退出后的唤醒。
- 在事件中传送媒体详情、封面候选、文件路径或播放方案。

## Key Decisions

- 使用现有 native Bearer HTTP 边界的有界 long polling，而不是管理端 Cookie WebSocket或浏览器 query-token WebSocket。
- 收到变更立即后台更新首页，但当前列表采用提示后原位刷新，避免滚动和播放上下文跳动。
- 实时通道只负责失效提示；权威内容始终重新读取 Player REST API。
