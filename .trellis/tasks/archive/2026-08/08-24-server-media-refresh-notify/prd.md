# Server 媒体服务器与 Player 入库通知闭环

## Goal

补齐 OhMyCine Server 媒体流水线的 `Notify` 阶段：当媒体库发生已经安全提交、可被消费的内容变更后，Server 自动通知绑定的 Emby/Jellyfin 刷新，并通知已连接且有权访问该媒体库的 Player 更新 ServerDataSource 内容，使新增或变更媒体无需重启 Player、重新连接数据源或手工清理缓存即可出现。

## Background

- Server 已具备本地与 115 媒体库扫描、下载、转移、识别、STRM/NFO/图片产物、signed 302、持久任务队列和 Player device token 接入。
- Emby 连接当前具备探测、摘要和 302 gateway，但没有媒体库刷新服务；Jellyfin 刷新适配尚未实现。
- `media_servers.refresh` 权限已经预留，当前没有对应业务 API、持久任务或管理页面。
- Player 的 ServerDataSource 已能浏览 Server 物理与在线媒体库；Player store 已有来源根快照、首页缓存和后台重载能力，但当前没有使用 device token 的 Server 媒体变更事件通道。
- 现有 `/api/v1/jobs/events/ws` 是管理端 Cookie 会话的 Job 事件流，不能作为 Player device-token 媒体事件流直接复用。

## Requirements

### 1. Authoritative notification boundary

- 通知必须基于已经提交的媒体库可见状态，而不是下载器“完成”、生活事件到达或文件开始转移等中间信号。
- 本地入库、115 云端整理、STRM generation、既有媒体库扫描和人工识别修正只要产生真实 catalog 变化，都进入同一通知入口。
- 失败、partial、superseded、仍等待冲突处理或产物尚未完成的执行不得发布“可用”通知。
- 同一媒体库短时间内的重复变化必须合并，避免连续扫描、批量入库或 watcher 抖动造成媒体服务器刷新风暴和 Player 重载风暴。
- Server 重启、通知重试和客户端断线重连后必须最终收敛，不能只依赖瞬时内存事件。

### 2. Emby/Jellyfin refresh

- 提供统一的媒体服务器刷新能力，MVP 同时覆盖 Emby 与 Jellyfin，不把 Jellyfin 仅保留为无实现占位。
- OhMyCine 媒体库可以绑定一个或多个明确的媒体服务器刷新目标；目标必须使用受控连接和上游媒体库身份，不能依靠标题或本地绝对路径猜测。
- 自动刷新、手动刷新、失败重试和重启恢复必须共享同一持久执行语义。
- 多次相同刷新请求应可安全合并或幂等执行；单个目标失败不能阻止其它目标或 Player 通知收敛。
- 管理端可以测试连接、选择刷新目标、查看最近结果、手动重试，并看到不含凭据和上游敏感响应的安全错误。

### 3. Player media refresh

- Server 必须通知所有在线、未撤销且有权访问变更媒体库的 Player 设备；事件不得暴露绝对路径、115 provider ID、Cookie、媒体服务器 API Key、signed STRM URL 或上游临时地址。
- Player 收到通知后只失效对应 ServerDataSource 和受影响媒体库相关缓存，并使统一首页的 Server 内容收敛；不得清理或重建本地文件、Emby、OpenList/Alist、CloudDrive2、WebDAV 等独立 DataSource。
- Player 离线、睡眠、网络切换或事件丢失后，重连时必须通过服务端持久版本/游标发现遗漏变化并自动收敛。
- 设备 token 撤销、用户停用或权限变化后，旧连接不得继续接收媒体事件。
- 多台 Player 设备分别维护自己的消费进度，慢设备或断线设备不能阻塞 Server 媒体流水线。
- Player 断开 Server 时继续保持独立可用；通知失败不影响媒体文件、STRM、Emby/Jellyfin 刷新或其它 DataSource。

### 4. Visibility and operations

- 管理端能够查看一次媒体变更对应的媒体服务器刷新状态和 Player 通知投影，不显示伪造的“全部设备已实时刷新”。
- 日志、审计、REST 和实时事件使用安全媒体库身份、变更版本、目标摘要和错误码，不记录凭据、私有 Job payload、内部绝对路径或临时播放 URL。
- 自动通知有明确的限频、重试上限和可观察失败状态；长期离线 Player 不产生无限增长的逐设备事件队列。

## Acceptance Criteria

- [ ] qBittorrent 本地入库成功后，绑定的 Emby 与 Jellyfin 刷新目标自动执行；已连接 Player 无需重启或重新连接即可看到新增媒体。
- [ ] 115 原生下载/分享转存完成并成功生成所需 STRM 产物后，媒体服务器刷新和 Player 更新均从最终可用状态触发，不会在 STRM 尚不可播放时提前展示。
- [ ] 对既有本地或 115 媒体库的外部新增、移动、重命名、删除及人工匹配修正，在权威 reconciliation 提交后产生一致的媒体变更版本并通知消费者。
- [ ] 一次批量导入只产生受控数量的媒体服务器刷新和 Player 重载；重复任务、重试和 Server 重启不造成通知风暴。
- [ ] 一个 Emby/Jellyfin 目标失败时，其它目标和 Player 仍能更新；失败目标可安全重试并最终显示真实结果。
- [ ] Player 在线时能自动失效对应 ServerDataSource 缓存；离线后再次连接也能发现并补齐遗漏变更。
- [ ] 正在播放的视频、播放进度和非 Server DataSource 不因媒体库通知而中断、丢失或被清理。
- [ ] 无权访问某媒体库的用户和设备收不到该库事件；撤销 device token 后现有事件连接及时失效。
- [ ] REST、实时事件、日志、审计和错误响应均不泄露凭据、绝对路径、provider ID、签名播放地址或上游临时 URL。
- [ ] fake Emby、fake Jellyfin、多 Player 设备、断线重连、权限变化、批量合并和失败重试均有自动化回归；至少完成一次隔离环境的端到端入库验证。

## Out of Scope

- Player 数据源配置、凭据、设置或播放进度的通用多设备同步。
- PT 聚合搜索、追更引擎、Transmission、OpenList/Alist 或 CloudDrive2 Server 驱动本身。
- 通知 Player 去直接刷新其独立 Emby/Jellyfin 数据源；本任务只更新 ServerDataSource 及包含它的聚合视图。
- 移动端系统级推送、邮件、Webhook、企业微信等离线通知渠道。
- 通过事件传递完整媒体详情、文件路径或播放 URL；Player 始终通过受保护的 Player REST API 重新读取权威数据。

## Key Decisions

- Emby/Jellyfin 与 Player 是同一权威媒体变更的并行消费者；Player 更新不等待媒体服务器刷新成功。
- Player 收到变更后立即失效对应 ServerDataSource 缓存并后台更新聚合首页。
- Player 当前正在浏览的媒体列表不强制换入新数据，保持滚动位置、选中状态和播放上下文，并显示“媒体库已更新”提示；用户点击后原位刷新。
- 离开后再次进入受影响页面时直接读取最新数据，不要求再次点击提示。
