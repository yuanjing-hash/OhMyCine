# Player 与 Server 连接和统一媒体来源

## Goal

在不影响 Player 独立使用现有数据源的前提下，把 OhMyCine Server 接入为一个可选 `ServerDataSource`。用户在 Player 输入 Server 地址、用户名和密码完成首次连接后，可以查看 Server 状态、浏览 Server 媒体库，并直接播放 Server 管理且已生成 STRM 的 115 媒体；当同一份媒体也由 Player 直接连接的 Emby 展示时，聚合首页和全局搜索只显示一个作品卡片。

## Background

- Player 已有通用 DataSource 契约，`DataSourceType` 已包含 `server`，但尚未实现 `ServerDataSource`。
- Player 的 Emby 数据源使用用户名密码换取用户 Token；Server 的 Emby Connection 使用管理 API Key。认证方式不同，不能用账号、地址或 Key 判断是否为同一个 Emby。
- Server 已能从 Emby `/System/Info` 读取稳定 `SystemId`，并已有 `MediaLibraryEntry → MediaArtifact → signed STRM URL → 115 302` 链路。
- Server 当前只有浏览器 Cookie session；Player/CLI 专用 device token 尚未实现。
- Server catalog 已有作品、季、集聚合，但当前 DTO 尚不足以直接满足 Player 的完整展示和播放解析。

## In Scope

### 1. Player 连接 Server

- Player 数据源管理中新增可用的 OhMyCine Server 类型。
- 表单包含显示名称、Server 地址、用户名和密码；支持添加、测试、编辑、停用和删除。
- 用户名密码只用于首次登录或重新认证。成功后换取可撤销的 Bearer device token，Player 不持久化 Server 密码。
- device token 使用现有 Player 安全凭据存储；普通 DataSource 配置仅保存 `credentialRef`、设备 ID 和非敏感显示信息。
- Server 离线、令牌失效或权限不足时只影响该 ServerDataSource，不影响其他 Player 数据源。

### 2. Player 专用 Server API

- 新增与浏览器 Cookie/CSRF 分离的 device-token 认证边界。
- 提供 Player bootstrap 状态：Server 名称、版本、当前用户、可访问媒体库数量和安全的播放器/Emby 身份摘要。
- 提供 Player 所需的媒体库、作品列表、搜索、详情、季集和媒体版本 DTO。
- DTO 不返回绝对路径、115 Provider ID、115 Cookie、Emby API Key、上游临时 URL或 signed STRM URL。
- 图片只使用不含凭据的受控地址或 Server 图片端点，不把 TMDB 凭据下发 Player。

### 3. Emby 实例、媒体库与媒体去重

- Emby 实例身份使用规范化 `SystemId` 的稳定非秘密指纹；地址、显示名称、用户名和认证方式不参与相等判断。
- Emby 媒体库身份使用 `(Emby instance fingerprint, Library/CollectionFolder ID)`；不能按库名去重。
- Emby 媒体项身份使用 `(Emby instance fingerprint, Item ID, MediaSource ID)`，不同媒体版本不得丢失。
- Server 原生 115 媒体与 Emby STRM 投影优先通过 Server 已持久化的 `MediaArtifact.OpaqueID / SourceIdentity` 建立精确等价关系。
- Player 的 EmbyDataSource 只在内存中检查 MediaSource/Path 是否为当前 Server 签发的 STRM 地址，并提取不带签名参数的 artifact 身份；原始 Path、signed URL 和查询参数不得进入配置、历史、日志或诊断。
- 当精确 artifact 身份暂时不可见时，可用 `(media type, TMDB ID)` 聚合到同一作品卡片，但必须保留各来源版本；只有标题或年份相同不能自动合并。
- 聚合首页与全局搜索在存在精确/作品级等价关系时保留 Server 项目为默认卡片，并保留匹配的 Emby 播放目标。
- 用户显式进入某个 Emby 数据源页面时仍可看到该 Emby 的完整原始库；去重不能篡改来源自身内容。
- 匹配不确定时宁可显示两个带来源标识的项目，也不能错误隐藏不同作品。

### 4. 已生成 STRM 的 115 直连播放

- ServerDataSource 默认播放 `server-direct`，不下载、读取或解析 `.strm` 文件内容，也不经过 Emby。
- Player 使用媒体 entry/version ID 请求播放；Server 重复校验用户权限、媒体库状态、active managed STRM artifact 和 115 storage/connection 归属。
- Server 复用现有 `SignedProxyService.ResolveArtifactForClient` 和 115 多设备协调逻辑，针对当前 User-Agent/客户端生成短期 115 地址并返回 302。
- Player 原生播放桥必须在跨 origin 跳转时移除 Server Bearer Authorization，不能把 device token 发送给 115/CDN。
- 当同一作品存在已匹配的 Emby 目标时，详情页可以选择 `Server 直连` 或该 Player 自己已登录的 Emby 线路；默认仍为 Server 直连。
- 115 直链、Cookie、signed STRM URL、播放 Authorization 不得落库、写入普通日志、播放历史或前端持久配置。

### 5. Device token 安全与撤销

- device token 绑定用户、设备 ID、设备名称、创建时间、最近使用时间、idle/absolute 过期时间和撤销状态；数据库只保存 token hash。
- Bearer token 仅允许访问 Player API/播放端点，不直接获得浏览器管理 API 的 CSRF 绕过能力。
- 用户停用、密码重置或显式撤销设备时，相关 device token 立即失效。
- 提供最小设备查询/撤销 API，Server WebUI 设备管理页面可在后续任务完善。
- 登录、撤销、认证失败和播放拒绝写脱敏审计/模块日志。

## Out of Scope

- Player 与 Server 之间的数据源配置同步、凭据同步和冲突合并。
- 多 Player 设备之间的数据源、设置、播放进度或历史同步。
- Server 接管 Player 本地数据源。
- Emby-only 媒体导入 Server 原生 catalog。
- Server 管理 API Key 代替 Player 的 Emby 用户身份播放。
- 未生成 active STRM artifact 的 115 媒体直连。
- Server 本地文件跨设备 Range 流、观看进度回写和继续观看合并。
- 自动线路质量评估、故障切换偏好和跨来源元数据写回。

## Acceptance Criteria

- [x] 未配置或断开 Server 时，Player 的本地、Emby/Jellyfin、OpenList/Alist、CloudDrive2 等现有数据源行为不变。
- [x] 用户能在 Player 使用 Server 地址、用户名和密码首次连接；重启后使用安全保存的 device token 恢复连接，配置文件中没有密码或 token。
- [x] Server device token 不能绕过浏览器 CSRF 进入普通管理写接口；撤销、停用用户或重置密码后令牌失效。
- [x] Player 能显示 Server 状态、可访问媒体库、作品详情、海报和季集，并能处理离线、无权限和空库状态。
- [x] Player 用户认证连接和 Server API Key 连接指向同一 Emby `SystemId` 时，被识别为同一 Emby 实例；相同库名但不同 `SystemId` 不会被误合并。
- [x] 同一份 Server 115 媒体和 Emby STRM 投影在聚合首页与全局搜索中只显示一个默认 Server 卡片；进入 Emby 来源页仍可看到 Emby 原始项目。
- [x] 不同文件版本在聚合后仍可见；缺少可靠身份时不按标题强行去重。
- [x] 已生成 active STRM 的 115 媒体可通过 ServerDataSource 播放，实际链路不读取 STRM 文本且不经过 Emby。
- [x] Player 能在匹配项详情中选择自己的 Emby 用户线路，Server 保存的 Emby 管理 API Key从不下发或注入播放请求。
- [x] 115 临时 URL、Cookie、device token、Emby API Key、signed STRM URL 和绝对路径不进入 API 普通 DTO、日志、诊断、配置或播放历史。
- [x] Server Go 测试、Player typecheck/lint/build、Rust/Tauri相关检查和跨层真实 302 测试通过。

## Technical Notes

- Player 专用 API 与浏览器管理 API 分组，复用 service/RBAC，但使用不同认证中间件。
- Player 远程播放仍走现有 DataSource → `getStreamRequest` → Rust/libmpv 安全播放桥，不在 Vue 页面编写 provider-specific 播放逻辑。
- STRM artifact 身份只用于等价判断和 Server 内部解析；它不是可独立访问媒体的凭据。
- 当前任务没有未决产品问题；数据源同步已按用户决定整体延期。
