# 修复 Player 设备管理与 Server 播放来源标识

## Goal

让 Server「播放器管理」页面真实显示当前账号已经安全配对的 OhMyCine Player 设备，并允许用户确认后撤销配对；同时修复 Player 把所有 Server 可播放版本误标为 `STRM · 远程` 的问题，准确显示媒体来自 OhMyCine Server 以及实际交付方式。

## Background

- `server/webui/src/views/PlayersView.vue:363-366` 仍是静态占位，没有加载配对设备。
- Server 已有 `AuthService.ListDevices` 和 `AuthService.RevokeDevice`，但现有 `GET|DELETE /api/v1/player/devices` 位于 Player Bearer 专用路由组，浏览器 Cookie Session 不能调用。
- 运行数据库已有一条有效的 `OhMyCine Player · Win32` 配对记录，说明配对流程成功，缺陷位于管理端接线。
- `player/src/services/datasource/server.ts:218-225` 当前写成 `isStrm: version.playable`，错误地把“可以播放”等同于“STRM”。
- Server 当前实际行为已经区分：本地 Storage 由鉴权 stream endpoint 直接响应 GET/HEAD/Range；115 媒体由同一 Server endpoint 鉴权后返回 302。
- `PlayerMediaVersion` 目前只有 `playable` 和 `stream_path`，没有供客户端展示的明确交付类型。

## Requirements

### R1 — 浏览器管理端设备 API

- 新增与 `/api/v1/player/*` Bearer 路由隔离的浏览器管理端设备列表与撤销接口。
- 管理端接口必须使用浏览器 Cookie Session、现有 Origin/CSRF 防护和显式权限；不得接受 Player Bearer 代替管理会话。
- 复用 `AuthService.ListDevices`、`AuthService.RevokeDevice` 和现有安全 DTO，不复制设备查询、撤销或序列化业务逻辑。
- 设备列表仅返回当前登录账号仍有效的 Player 设备，不返回 token/hash、原始 device ID、IP 或 User-Agent。
- 撤销操作仅能撤销当前登录账号拥有的设备，并继续写入现有脱敏审计事件。

### R2 — Player 设备管理 UI

- 「播放器管理」页面加载真实配对设备，只有真实空列表时才显示空状态；删除“本阶段占位”文案。
- 每张设备卡显示安全设备名、客户端类型、首次配对时间、最近活动、闲置到期与最长有效期。
- 页面提供手动刷新；撤销配对必须先确认，并明确提示该 Player 需要重新登录 Server。
- 加载、失败、空状态和撤销中的按钮状态必须清晰；失败使用现有悬浮 Toast，不遮挡或阻塞 Emby 管理卡片。
- 不提供虚假的手动添加 Player 入口；Player 仍通过自身设置页登录 Server 完成配对。

### R3 — Server 媒体交付类型契约

- `PlayerMediaVersion` 为每个可播放版本返回明确且有界的交付类型：本地文件由 Server 流式响应，115 由 Server 鉴权后 302 重定向。
- 交付类型不得暴露绝对路径、115 provider item、STRM artifact opaque ID、签名 URL、Cookie 或临时 CDN URL。
- `playable` 只表示可播放，不再承担 STRM 类型判断。
- 本地文件流、115 artifact 校验、Bearer 鉴权、GET/HEAD/Range 与 302 的现有行为不得改变。

### R4 — Player 来源与交付方式展示

- ServerDataSource 运行时解析新增交付类型，并把来源与交付方式映射到通用 `MediaSourceOption`，视图不得直接耦合 Server API DTO。
- Server 自有线路不再设置 `isStrm`；旧 Server 缺少新字段时也不得回退成 STRM。
- 本地媒体显示类似 `来自 OhMyCine Server · 文件流`；115 媒体显示类似 `来自 OhMyCine Server · 302 直链`。
- 若用户给 Server 数据源设置了自定义显示名称，来源标签使用该显示名称。
- `isRemote` 可以继续表达“文件不在 Player 本机”，但当准确来源标签存在时，界面不再只显示含糊的 `远程`。
- Emby/Jellyfin 真实 STRM 和其它数据源的既有 `isStrm`/远程显示不得回归。

### R5 — 提交、推送与 Player Beta 发布

- 修复和任务工件通过质量门禁后，按中文 Conventional Commits 提交并推送到远端 `develop`。
- Server 只随 `develop` 推送代码，不创建 Server tag、Release 或安装包发布。
- Player 从最新远端 `develop` 发布下一版 Beta；当前最新远端标签为 `v1.1.10`，本轮目标版本为 `v1.1.11`。
- 发布前必须重新 fetch，并确认发布提交等于最新 `origin/develop`；不得从本地未推送提交、旧提交或功能分支发布。
- 等待 Player Beta workflow 完成，并核实 GitHub prerelease、更新清单和预期 Windows/Android 资产。

## Acceptance Criteria

- [ ] 已配对且未撤销的 Win32 Player 在 Server「播放器管理」页面显示为真实设备卡片。
- [ ] 未配对设备时显示真实空状态；接口失败时显示错误反馈且 Emby 卡片仍可使用。
- [ ] 点击撤销先出现确认，成功后设备卡消失，原 Player device token 随后访问受保护接口得到未认证响应。
- [ ] 浏览器管理接口使用 Cookie Session；DELETE 需要 CSRF 与更新权限；Player Bearer 无法进入该管理接口。
- [ ] 设备响应不包含 token/hash、device ID、IP、User-Agent 或其它敏感字段。
- [ ] Server 本地电影详情显示 `来自 OhMyCine Server · 文件流`，不显示 STRM。
- [ ] Server 115 电影详情显示 `来自 OhMyCine Server · 302 直链`，不显示 STRM；播放仍由 Player 请求 Server 后获得 302。
- [ ] 旧 Server 响应缺少交付类型时显示 `来自 <Server 名称>`，不误标 STRM，也不导致页面崩溃。
- [ ] Server 路由/服务测试、Server Web UI 测试/typecheck/lint/build、Player ServerDataSource 验证/typecheck/lint/build通过。
- [ ] 任务提交已推送到 `origin/develop`，且本地 `develop` 与远端目标提交一致。
- [ ] Player `v1.1.11` Beta workflow 成功，GitHub prerelease 与更新资产可用；未创建 Server Release。
- [ ] 既有两个 Tauri mobile schema 修改和旧未跟踪 Trellis 目录不被修改或混入本任务结果。

## Out of Scope

- 不增加手动创建 Player 设备或复制 device token 的入口。
- 不引入实时在线心跳/WebSocket 在线状态；本轮只展示持久化的最近活动与有效期。
- 不改变 Player 与 Server 的账号登录、令牌寿命或安全存储方式。
- 不改变实际文件流、115 302、STRM 生成或 Emby 网关播放链路。
- 不实现跨账号查看或撤销其它用户的 Player 设备。
- 不发布 Player Stable，不合并到 `main`。
- 不发布 Server 二进制、tag 或 GitHub Release。

## Technical Notes

- 管理端路由复用当前页面权限：读取使用 `connections.read`，撤销使用 `connections.update`；服务仍按 `actor.User.ID` 限定当前账号。
- 管理端与 Player Bearer 路由可以复用同一 handler/DTO，但必须分别注册在各自认证中间件之后。
- 推荐交付类型 JSON 值为稳定枚举 `server_stream` 与 `server_redirect`；Player 对未知/缺失值安全降级为仅显示来源。
- Blocking open questions: none.
