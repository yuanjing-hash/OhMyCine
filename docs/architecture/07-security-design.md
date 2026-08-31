# OhMyCine — 安全设计文档

## 1. 设计目标

OhMyCine 是自托管家庭影院生态系统，安全设计的核心目标是：

- 保护用户的媒体库、账号凭据、网盘 Cookie、API Key 和下载器访问权限
- 避免 Server 暴露后成为公开代理、文件跳板或未授权媒体入口
- 保证 Player 独立可用时，本地配置和本地元数据不被其他应用轻易读取
- 允许插件、站点适配器、网盘驱动逐步扩展，但默认不信任第三方代码
- 在不牺牲自托管便利性的前提下，提供清晰的安全默认值

## 2. 威胁模型

### 2.1 需要保护的资产

| 资产 | 示例 | 风险 |
|------|------|------|
| 媒体服务器凭据 | Emby/Jellyfin API Key | 被盗后可读取媒体库、刷新媒体库、访问播放地址 |
| 网盘凭据 | 115/夸克 Cookie、OpenList Token、CloudDrive2 API Token、WebDAV 密码 | 被盗后可访问或操作网盘文件 |
| PT 站点凭据 | Cookie、Passkey、User ID | 被盗后可能导致账号风险 |
| 下载器凭据 | qBittorrent/Transmission 用户名密码 | 被盗后可添加、删除、控制下载任务 |
| AI API Key | OpenAI/Claude/自定义 Provider Key | 被盗后产生费用或泄露请求内容 |
| JWT / Session | Server 登录态 | 被盗后可访问 Server 管理功能 |
| 302 代理地址 | `/proxy/{driver}/{path...}` | 未鉴权时可能变成公开直链代理 |
| 本地配置 | Player config、Server config、SQLite 数据库 | 包含连接信息和用户偏好 |
| 插件代码 | Hub 插件、站点适配器、网盘驱动 | 恶意插件可读取配置、发起请求、删除文件 |

### 2.2 攻击面

- Player 本地配置文件、SQLite 元数据库、海报缓存
- Player 调用外部数据源的 HTTP/WebDAV/API 请求
- Player 与 Server 的配置同步接口
- Server REST API、WebSocket、302 代理路由
- Server Docker 映射端口和挂载目录
- 下载完成后的文件转移、硬链接、软链接、删除清理逻辑
- PT 站点适配器、网盘驱动、未来插件系统
- 日志、错误响应、调试页面、崩溃报告

## 3. 安全边界

```text
┌─────────────────────────────────────────────────────────────┐
│ 用户设备                                                     │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ OhMyCine Player                                       │  │
│  │ - 本地配置                                            │  │
│  │ - 本地元数据库                                        │  │
│  │ - DataSource 凭据                                     │  │
│  └───────────────┬───────────────────────────────────────┘  │
└──────────────────┼──────────────────────────────────────────┘
                   │ HTTPS / 局域网 HTTP / WebSocket
                   ▼
┌─────────────────────────────────────────────────────────────┐
│ 用户服务器 / NAS                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ OhMyCine Server                                       │  │
│  │ - 用户认证                                            │  │
│  │ - 连接管理                                            │  │
│  │ - 302 代理                                            │  │
│  │ - 下载/转移/STRM                                      │  │
│  └──────┬──────────┬──────────┬──────────┬──────────────┘  │
└─────────┼──────────┼──────────┼──────────┼─────────────────┘
          ▼          ▼          ▼          ▼
       网盘 API   OpenList/CD2  下载器     Emby/Jellyfin
```

安全边界原则：

1. Player 和 Server 都是用户可信组件，但它们保存的外部服务凭据需要加密或由系统密钥保护。
2. 外部数据源、PT 站点、网盘 API、插件和第三方 Provider 默认不可信。
3. Server 的管理 API 默认需要登录认证。
4. 302 代理是否允许匿名访问必须显式配置，默认不公开。
5. 当前 Player 与 Server 不自动同步配置；未来同步功能启用时默认只同步必要结构字段，敏感字段需要用户逐次确认。

## 4. 认证与会话

### 4.1 Server 登录

Server 同源 Web 管理端使用用户名密码登录，建立可撤销的服务端 opaque session；浏览器只接收 HttpOnly Cookie，不把 JWT 或 session token 写入 localStorage。Player 已使用独立 device token 边界；CLI 和其它自动化客户端仍使用后续单独设计的 API token 边界。

建议：

- 密码使用 `bcrypt` 或 `argon2id` 哈希保存
- 首次启动不创建默认管理员或默认密码；仅在数据库没有用户时允许事务化创建唯一 owner
- Cookie 中只保存高熵随机 token，数据库只保存 SHA-256 哈希
- session 默认 2 小时 idle、7 天 absolute 上限；登出、停用和密码重置可立即撤销
- HTTPS 使用 `__Host-omc_session; Secure; HttpOnly; SameSite=Lax; Path=/`；显式局域网 HTTP 模式使用 host-only 普通 Cookie 并提示保护等级差异
- 登录失败需要限速，避免暴力破解

所有 Cookie 认证的状态变更请求还必须校验 session-bound `X-CSRF-Token`、精确 Origin（必要时 Referer fallback）、Fetch Metadata 和 `application/json`。CSRF token 只保存在前端内存，不进入 URL、日志或持久化存储。

持久化任务队列的 worker payload/checkpoint 属于私有执行状态，不进入 REST、WebSocket、日志或审计。写入前限制为 64 KiB JSON 对象，并递归拒绝 Authorization、Cookie、password、secret、token、passkey、credential、签名 URL 与本地/绝对路径形态的字段。租约只持久化随机 token 的 SHA-256；所有 heartbeat、checkpoint 和完成操作都必须持有当前未过期 token。运行中暂停/取消先在短事务内持久化中断意图，事务提交后才通知 worker，且在 worker确认或租约过期前继续占用并发槽，避免非协作 worker 造成超卖。下载流水线取消必须二次确认并先调用 provider `Cancel(taskID, false)` 删除任务、保留文件；成功或明确 task-not-found 后才把 DownloadTask 与相关 Job 标记为 cancelled 并释放 Follow claim。终态删除默认也使用 `delete_data=false`，只有用户显式勾选完全删除才传 `true` 删除源/临时文件。provider 不可用、返回不确定错误或 ProviderTaskID 已存在但 Downloader 配置缺失时，必须保留本地记录；Submit 在取消后返回的迟到 provider ID 必须先持久化再用独立有界上下文清理，失败留下可诊断重试事实，不得仅为清空界面而伪造成功。

115 分享链接与提取码具有访问凭据属性，只能进入按 DownloadTask purpose/AAD 隔离的 AES-256-GCM source envelope；`provider_item` 是 Server 内部来源，HTTP 客户端不得提交。分享明文、密文、provider item/directory ID、Cookie、完整 provider 路径和原始上游响应不得进入 Job payload/checkpoint、REST DTO、WebSocket、审计、运行日志或导出。分享与手工转存接管目录必须由绑定 actor、Connection、Storage、Storage 根、用途和过期时间的短期令牌选择，并在保存、提交、sweep、worker 与删除前重新验证 ancestry；下载目录不能与最终媒体库根或同 Connection 其它启用监听目录重叠。生活事件只用于唤醒受限的权威目录枚举，不能直接证明某个文件已经完整落盘。普通取消只删除 provider 任务并保留内容；只有显式完全删除且重新证明 OMC-owned `omc-*`/adopted item 边界后，才能回收对应 provider 子树。

PT 站点 Cookie/passkey 使用独立于下载任务的站点 AES-GCM purpose/AAD，普通 API 只返回 `credential_configured`，不得返回密文、种子下载 URL 或 passkey。站点 Base URL 只接受无 userinfo/query/fragment 的 HTTPS 根地址；adapter 使用超时、响应大小、重定向次数和严格同源（含端口）限制。PT 搜索结果令牌必须使用 256-bit 随机值并绑定 actor、site、torrent identity 和过期时间；下载确认采用原子 reserve，第二个并发消费者必须失败，provider/入队失败时只能在原 TTL 内恢复，成功后永久消费。SSE 只序列化脱敏结果 DTO，写入串行；客户端取消后不得继续写错误或上游信息。

### 4.2 API 鉴权

| API 类型 | 默认策略 |
|----------|----------|
| `/api/v1/auth/login` | 匿名可访问，限速 |
| `/api/v1/health` | 匿名可访问，只返回基础状态 |
| `/api/v1/*` | 默认需要认证 |
| `/ws/events` | 需要认证，Token 绑定用户 |
| `/proxy/*` | 默认需要签名 URL 或认证 |

### 4.3 Player 连接 Server

Player 首次连接 Server 时提交用户名、密码、随机设备 ID 和安全显示名称，密码只用于当次校验。Server 返回一次性可见的高熵 `omc_player_` device token；Server 数据库只保存 SHA-256 token hash 与不可逆 device ID hash，Player 只把原始 token 保存到 provider-specific 安全凭据 envelope，不保存 Server 密码。

device token 只允许进入 `/api/v1/player/*` 的独立 Bearer 路由组，不能作为 Cookie session、不能获得 CSRF 豁免，也不能进入普通管理 API。每次认证重新解析当前用户和权限；默认 30 天 idle、180 天 absolute 上限，并在同设备重新登录、登出、显式设备撤销、用户停用或密码重置时立即撤销。设备列表只返回记录 ID、安全名称、客户端类型和生命周期时间，不返回 token/hash、IP、User-Agent 或原始设备 ID。

Player 115 直连播放仍按 entry/version ID 请求 Server；Server 在每次 GET/HEAD 中重新校验媒体库权限和 active managed artifact 后才返回短期 302。Windows/Android 的 loopback 播放桥仅向 Server origin 发送 device Bearer，跨 origin 重定向必须删除 Authorization、Cookie 和 provider-private Header，禁止将 device token 转发给 115/CDN。播放 URL、Header、signed STRM URL 和上游临时地址只存在于瞬时原生播放边界，不进入路由、配置、播放历史、日志或诊断。

Player 媒体变更使用同一 `/api/v1/player/*` Bearer 边界上的 12 秒有界长轮询，不使用 query token、Cookie、WebSocket subprotocol 或管理端 Job WebSocket。每次 poll 都重新认证设备/用户并按当前媒体库权限过滤；cursor 只是可持久化的断线恢复提示，不授予访问权。事件只包含逻辑媒体库 ID、content revision、受控 kind、时间和新 cursor；禁止包含绝对路径、115/provider ID、Emby/Jellyfin upstream ID/API Key、signed STRM、临时 URL 或原始错误。ready outbox 有界保留，过期 cursor 返回 `resync_required`，不为离线设备创建无界逐设备队列。

Emby/Jellyfin 管理 API Key 继续使用 Connection provider/purpose 隔离的 AES-GCM envelope，只有刷新服务调用边界可以解密。`media_server_refresh` Job payload 只允许 target record ID；上游 library ID 仅作为私有目标配置保存，不进入目标 DTO、Job、日志或审计。外部客户端固定到已验证的 HTTP(S) endpoint，拒绝 credential-bearing redirect，限制超时和响应大小，并将认证/配置错误与可重试的网络不可用/限流明确分开。

## 5. 权限模型

### 5.1 角色与 permission catalog

Server 本地目录浏览使用独立敏感权限 `storages.browse`，不能由 `storages.read` 或创建/编辑按钮隐式获得。owner、administrator 和 operator 默认具备该权限，viewer 不具备；根列表和子目录 API 同时执行路由中间件与 service policy 校验。

媒体逻辑分类 Profile 使用独立的 `media_classification_profiles.read/create/update/delete`，不得复用流水线 `categories.*`。owner/administrator 和 operator 默认具备四项，viewer 默认不具备；路由 middleware 与 service policy 双重校验。内置 Profile 只能读取和复制，不能更新或删除。创建、复制、更新、删除的审计只记录 actor、Profile ID、动作、结果、revision、kind 和分类数量，不记录 `rules_json`、完整规则或未来媒体绝对路径。

MediaLibrary 使用独立的 `media_libraries.read/create/update/delete/scan` 权限；管理端按钮可按生成的 permission constants 隐藏，但 API 路由与 service policy 仍是授权边界。来源目录必须由 Storage 范围内的短期选择令牌解析为 provider-relative root，客户端不得从展示路径手工拼接或提交绝对来源路径。扫描与清单 API 只返回相对路径、opaque provider identity、解析/分类状态和安全错误码，不把 Storage 绝对根、原始 OS 错误或媒体绝对路径送入展示、导出、日志或 AI 字段。删除媒体库只删除配置、索引与扫描记录；扫描、初始化、重试、监听和停用流程不得修改来源媒体文件。

目录选择器仅逐层枚举 Server 进程可见目录，响应 `Cache-Control: no-store`，并使用短期、签名、用途隔离且绑定平台/adapter 版本的 opaque token 导航和选择。客户端不得拼接盘符、UNC 主机、分隔符或 `..`。symlink、junction、mount-point Reparse Point 等跳转项不可进入或选择；保存时仍重新执行 Storage 根规范化与只读探测。普通日志和审计不得记录被浏览/选择的绝对路径或子目录名，只能记录 actor、结果、稳定错误、平台和条目数等脱敏信息。

| 角色 | 权限 |
|------|------|
| administrator | 受保护系统角色，拥有 canonical permission catalog 全部能力 |
| operator | 管理连接、存储目标、STRM 和刷新，不管理用户/角色/秘密导出 |
| viewer | 只读状态和脱敏业务摘要 |
| custom | 管理员从固定 permission code 中组合，支持多角色权限并集 |

### 5.2 权限粒度

页面、导航、按钮和 API 使用同一个 `<resource>.<action>` permission code。前端只改善体验，Gin middleware 和 service policy 才是安全边界。首版 allow-only，不实现 deny、继承、ABAC 或资源实例 scope。

安全不变量：

- 首次 owner 不能删除、停用或失去系统管理能力；所有权转移必须是未来独立流程。
- 系统始终保留至少一个 active `system.admin` 有效用户。
- 首版禁止用户停用、删除或通过角色变更降权自己。
- 非系统管理员只能授予自己已经拥有的权限，不能通过角色创建/编辑/分配完成权限提升。
- 系统角色不能删除；角色权限、用户角色和审计写入同一事务。

业务权限继续按以下方向扩展：

- 连接管理：仅 admin
- 存储目标：仅 admin
- 分类规则：仅 admin
- 下载器管理：仅 admin
- PT 站点管理：仅 admin
- 文件删除/移动/重命名：默认 admin，可配置授权
- 下载任务：普通用户只能看到和操作自己创建的任务
- 追更任务：普通用户只能看到和操作自己创建的任务
- 302 代理：根据媒体库访问权限或签名 URL 校验

## 6. 凭据存储

### 6.1 Server 凭据

Server 需要保存的敏感字段包括：

- Emby/Jellyfin API Key
- OpenList Token / 用户名密码
- CloudDrive2 应用 API Token
- 通用 WebDAV 账号密码
- 115 Cookie
- PT 站点 Cookie / Passkey
- 下载器用户名密码
- AI Provider API Key

存储要求：

- 数据库中敏感配置统一加密保存
- 推荐使用 AES-256-GCM
- 主密钥从环境变量或首次启动生成的本地密钥文件读取
- Docker 部署时支持通过环境变量或 secret 文件提供主密钥
- 主密钥不写入日志、不通过 API 返回
- 配置导出默认脱敏，除非用户明确选择“导出完整凭据”

#### 已保存第三方凭据的按需查看

管理界面的普通列表与详情 API 仍只返回逐字段 `configured` 布尔，不返回明文或密文。用户主动点击眼睛时，Server WebUI 才调用 `POST /api/v1/credentials/reveal`；该动作必须同时具备登录会话、CSRF 校验、`connections.secrets.export` 权限、`Cache-Control: no-store`、资源/字段硬白名单和无明文审计。成功与失败审计只记录资源类型、内部资源 ID、字段名和安全错误码。

允许范围只包括用户自行保存的第三方连接、下载器、站点、CookieCloud、自定义 TMDB 和受支持插件凭据。OhMyCine 用户密码、JWT/会话/Player 设备访问令牌、主密钥、302 签名密钥、内置 TMDB 以及部署注入凭据永久禁止回显。前端把结果放入独立的短生命周期显示状态；隐藏、切换对象或卸载即清除，且单纯查看后保存不能把旧值作为“新凭据”提交。

配置结构建议：

```json
{
  "type": "115",
  "fields": {
    "cookie": "enc:v1:base64(nonce+ciphertext+tag)",
    "api_proxy": "https://..."
  }
}
```

### 6.2 Player 凭据

Player 本地需要保存：

- DataSource 配置
- Server 连接 Token
- AI API Key
- 本地元数据库

桌面端优先使用系统 Keychain：

| 平台 | 推荐存储 |
|------|----------|
| Windows | Windows Credential Manager / DPAPI |
| macOS | Keychain |
| Linux | Secret Service / libsecret，无法使用时提示用户风险 |
| Android | Android Keystore |
| iOS | Keychain |

普通配置文件只保存非敏感字段，敏感字段保存引用 ID。

Player 标准模式把 AES-GCM 凭据数据库的主密钥交给当前平台系统安全存储保护：Windows 使用当前用户 DPAPI，Android 使用 Keystore AES-GCM 包装，macOS/iOS 使用 Apple Keychain，Linux 优先使用 Secret Service/libsecret。Linux 桌面会话没有可用 Secret Service 时才允许降级为权限受限的本机文件密钥，并必须在设置页显示风险提示。旧裸 Base64/旧文件主密钥首次读取后原地迁移到目标系统存储，迁移不得轮换主密钥；已有 `credentials.sqlite` 但系统/文件主密钥缺失时必须保留数据库并报错，禁止静默生成新钥匙导致全部既有密文不可解。EXE 同目录存在 `portable.flag` 或使用 `--portable` 时，Player 改用 EXE 同目录 `data`、`cache`、`logs`，为了跨目录/设备移动继续使用文件主密钥；设置页必须明确提示便携模式保护等级低于系统安全存储，用户需要保护整个便携目录。

数据源非敏感配置、主题、TMDB 非敏感设置、分类规则和扫描计划进入 `settings.sqlite`。WebView localStorage 只作为标准模式的旧版本迁移输入或浏览器开发 fallback，不得继续作为 Tauri 桌面版配置源。迁移只处理固定 namespaced key 和固定 SQLite 文件白名单，不接受任意路径或敏感明文。便携模式是独立配置档案，不得自动读取、复制或删除标准目录、旧 Roaming 目录以及共享 WebView localStorage 中的数据；跨模式导入只能由用户显式触发。

OhMyCine 正式发布包可通过互斥的 GitHub Actions Secret 在构建期注入应用级 TMDB Read Access Token 或 API Key，以提供默认元数据通道；前者使用 Bearer，后者仅使用 v3 `api_key` 查询参数，类型必须显式携带而不能按内容猜测。该值不得进入 Git、普通配置、构建日志、诊断、错误或导出；Server 只返回凭据来源和类型，Player/Server 用户自定义凭据分别存入各自安全凭证边界并优先使用。由于应用级凭据会进入最终二进制，它必须被视为可提取的发布凭据，只能用于受限的只读元数据访问，并具备独立撤销、轮换和限流能力；不得复用用户账号级秘密或具有写权限的令牌。Server 数据库 v11 前的 TMDB 密文保持原样并默认解释为 Read Access Token。

用户自定义 TMDB API/图片代理属于显式外部信任边界：仅接受不含 userinfo、query、fragment 的 HTTPS Base URL，使用有限超时和响应体大小，禁止原生客户端自动重定向。API 与图片地址是独立设置，每项必须单独通过真实请求后才持久化；失败不得覆盖该项上一次验证通过的路由，也不得改变另一项。官方默认 API 可在纯网络故障时回退到官方旧域名，自定义 API 代理不得跨域回退，避免把内置或用户 TMDB 凭据静默转发给其它主机。日志、错误和诊断不得输出凭据或含 `api_key` 的完整 URL。

CloudDrive2、夸克网盘、123 云盘与 WebDAV 必须使用不同的凭据 envelope 和 DataSource 类型：

- `clouddrive2` 保存用户在 CloudDrive2 中创建的应用 API Token，通过 Tauri Rust gRPC 客户端以 Bearer metadata 瞬时使用；不保存 CloudDrive2 主账号密码。
- `quark` 只保存最终夸克 Cookie。扫码登录 token、service ticket 和账号登录窗口状态只在内存中短期存在；二维码必须在本机生成，不得把 token 发送给第三方二维码服务。账号密码、验证码和设备验证仅在固定夸克官方 HTTPS WebView 内处理，Player 不读取、不保存账号密码。
- 夸克 API Base URL、扫码和账号登录地址均为固定官方 HTTPS 地址。普通配置只保存 `credentialRef`、固定 provider identity 和 `rootPath`；Cookie、`__puus`/`__pus` 轮换值、下载直链及播放 Header 不得进入普通配置、localStorage、扫描缓存、路由、日志或诊断文本。
- `123` 保存官方 API Access Token；账号登录模式还会把手机号/邮箱与密码保存在同一个独立 AES-GCM credential envelope 中，仅用于 token 过期后的原生重登录。高级令牌模式不得伪造空账号凭据，且 token 失效后必须要求用户重新导入。
- 123 云盘 API Base URL 和登录地址固定为官方 HTTPS 地址。动态 CRC32 查询签名只在 Rust 请求前生成；Access Token、账号密码、签名参数、`FileId` 下载请求字段、临时下载直链与 Referer 不得进入普通配置、localStorage、扫描缓存、路由、日志或诊断文本。下载地址只允许 HTTP(S)，限制响应体与重定向，禁止携带 URL userinfo。
- `webdav` 保存独立 WebDAV 用户名和密码，通过 Basic Auth 瞬时使用；账号密码禁止嵌入 URL。
- 两者的 token、Authorization header、直链和附加播放 header 均不得进入普通配置、localStorage、扫描缓存、播放历史、日志或诊断文本。
- Android 为绕过原生 libmpv/FFmpeg 的设备 TLS 兼容问题，可由 Rust reqwest/rustls 建立临时媒体桥，但只能绑定 `127.0.0.1` 随机端口。每次播放必须使用新的高熵 URL-safe 令牌并以精确比较验证，只允许 GET/HEAD，停止播放后清除内存目标；原始直链、签名参数和认证 Header 不得写入回环 URL、持久化或普通日志，TLS 证书校验不得关闭。
- Android 应用更新只允许读取固定 `yuanjing-hash/OhMyCine` GitHub Release 中与标签精确匹配的 ARM64 APK 和 `.sha256`。重定向只允许 GitHub release asset 域名，限制响应大小和跳转次数，Rust 完成 SHA-256 校验后只写入应用 cache 的 `updates/`；FileProvider 只暴露该子目录，禁止外部存储和任意 cache 路径。安装必须由 Android 系统界面确认，`REQUEST_INSTALL_PACKAGES` 不得用于静默安装。
- Android preview keystore 和密码不得进入 Git、构建日志、Release asset 或普通 app data。CI 只从 GitHub Actions Secrets 注入；本机备份必须位于用户配置目录并限制文件权限。更换签名会破坏覆盖升级能力，应视为显式密钥轮换事件。
- Vue Router 只保存 `sourceId`、`itemId`、可选媒体版本 ID 和短生命周期上下文 ID；远程播放 URL、签名参数、播放 header 与本地绝对路径不得进入 route query/history。PlayerView 只在调用 mpv 前即时解析播放请求。
- Player 路由构造统一走 query allowlist，Player route guard 对旧链接执行 replace 清洗，移除 `path`、标题、海报/背景/Logo URL、续播位置和其它非身份字段；本地文件 locator、展示元数据和续播位置只存在于进程内 `PlaybackMediaContext`。该清洗不得把 Emby/云盘播放地址提前固化，也不得改动 Android Rust 回环 302 桥。
- Tauri 生产 CSP 必须至少限制 `script-src 'self'`，禁止 `unsafe-eval`、frame 与 object；`connect-src` 只保留 Tauri IPC 和 DataSource 所需 HTTP(S)，图片只开放应用自身、asset/data/blob 与 HTTP(S)。开发 CSP 可额外开放 Vite HMR WebSocket，但不得把通配符或任意远程脚本带入生产策略。
- 删除媒体源时，必须按精确 `sourceId` 删除该来源的本机播放历史、单视频播放偏好和来源拥有的字幕缓存，并按 source/root 清理原始文件扫描缓存；禁止使用空来源或不受约束的批量删除。
- OpenSubtitles API Key 或账号密码以互斥认证模式进入独立 credential envelope，不得进入普通设置、localStorage、日志或导出配置。普通设置只保存默认字幕语言和提供器启用状态；旧组合凭据迁移时不得同时保留两套秘密。
- Player 本地字幕下载只允许 Tauri 受控客户端访问固定 OpenSubtitles HTTPS REST/XML-RPC 端点和受信任下载域名，限制超时、重定向、搜索响应、Base64/gzip 解码后大小和字幕文件大小。XML 响应拒绝 DTD/Entity，远端文件名不得直接成为本地路径；只读取允许的字幕扩展名并使用哈希文件名写入当前存储模式的 `cache/subtitles/<source-hash>/<media-hash>`，以便按媒体源安全清理。
- 单视频播放偏好只允许保存稳定媒体身份、字幕/音轨指纹、字幕偏移、倍速和画面模式。缓存字幕路径读取时必须 canonicalize 并确认仍位于当前 Player `cache/subtitles` 根目录内；不得保存远程字幕 URL、播放直链、签名参数、请求 Header 或凭据。全局清缓存只能清媒体缓存、扫描缓存和单视频偏好，不得删除凭据、数据源配置、播放记录或全局软件设置。
- Player 海报/背景缓存只写入当前存储档案的 `cache/images`。文件名使用稳定 artwork key 的 SHA-256，sidecar 只保存原 URL 的不可逆 hash、受控 MIME、字节数和最近访问时间，禁止保存原 URL、API Key、签名参数、Cookie 或 Authorization。下载仅允许 HTTP(S)、同源有限重定向、固定单图大小上限和图片魔数；总容量使用 100-4096 MB 受控设置、默认 500 MB，并按 LRU 删除完整 `.bin/.json` 文件对。前端接收的 `data:` URL 只存在于当前 WebView 内存，不进入 `settings.sqlite`、播放记录或日志。
- 字幕搜索只向提供器发送作品 ID、用户当前选择的媒体标题/文件 basename/自定义关键词、年份、媒体类型和季集号，不发送目录、本地绝对路径、数据源凭据、签名播放 URL、查询参数或播放 Header。

示例：

```json
{
  "datasources": [
    {
      "id": "home-emby",
      "type": "emby",
      "name": "家庭 Emby",
      "url": "http://nas:8096",
      "credentialRef": "cred_home_emby_api_key"
    }
  ]
}
```

## 7. 配置同步安全

Player ↔ Server 配置同步是高风险功能，因为它可能把本地凭据复制到另一端。

### 7.1 默认同步策略

| 字段 | 默认同步 |
|------|----------|
| 数据源名称 | 是 |
| 数据源类型 | 是 |
| URL / Base URL | 是 |
| 路径、媒体库 ID | 是 |
| API Key / Cookie / 密码 | 否，需用户确认 |
| AI API Key | 否 |
| 下载器密码 | 否 |
| PT Cookie / Passkey | 否 |

### 7.2 同步模式

- **结构同步**：只同步数据源类型、名称、URL、路径，不同步凭据
- **完整同步**：同步凭据，需要用户二次确认
- **单向导入**：从 Server 拉取配置到 Player
- **单向导出**：从 Player 推送配置到 Server

### 7.3 冲突处理

- 同名不同 URL：提示用户选择保留哪一端
- 同 URL 不同凭据：不自动覆盖凭据
- 删除同步：默认不级联删除另一端配置，只标记为未同步

## 8. 302 代理安全

302 代理是 Server 的关键能力，也是最容易被滥用的入口。

### 8.1 默认策略

`/proxy/{driver}/{path...}` 默认不允许裸奔公开访问。

支持三种访问模式：

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| authenticated | 请求携带登录态或 API Token | Player 直连 Server 播放 |
| signed-url | STRM 中写入带签名和过期时间的 URL | Emby/Jellyfin 扫描 STRM 后播放 |
| trusted-lan | 仅允许内网 IP 访问 | 家庭局域网简化部署 |

默认推荐 `signed-url`。

监听地址与对外公布地址必须分离。`0.0.0.0`/`::` 可以作为 Server bind address，但不能成为 `OMC_PUBLIC_ORIGIN`、CSRF origin、STRM 内容或 Emby gateway 地址；这些地址只由启动时严格校验的全局 `OMC_PUBLIC_ORIGIN` 生成，不能信任请求 Host/Forwarded，也不能在每个媒体库或播放器上重复配置。

Emby 管理使用加密 Connection 凭据，独立“播放器管理”页面只读取受控聚合摘要：服务器名、版本、媒体库/电影/剧集/单集数量及查询时间。禁止返回 API Key、库名、item ID、路径、用户、session 或上游原始 payload；可选统计失败必须保持 unknown/partial，不能伪造成 0。302 gateway 不注入保存的服务 API Key，只保留客户端自身 Emby 权限。

Emby Web 的 HTML5 播放器若为远程 DirectPlay 设置 `crossOrigin=anonymous`，浏览器会要求 302 最终 CDN 返回 CORS Header；中间网关给 302 响应添加 CORS Header 不能替代最终 CDN 授权。为保持流量直达 115 CDN，网关允许对固定的 `basehtmlplayer.js` 与 `plugin.js` 播放器资源执行确定性、限长、identity 编码的兼容修补，移除远程 DirectPlay 的 crossOrigin 赋值并禁用该响应缓存。由于 Service Worker/Cache Storage 可能完全复用旧模块，固定 `/web/index.html`、`/web`、`/web/` HTML 壳还可优先加载一个网关固定同源路径提供的不可配置兼容脚本；脚本正文硬编码、不含用户输入或凭据，保留上游 CSP，HTML 与脚本均清除缓存验证器并设为 `no-store`。禁止任意 HTML/JavaScript 注入、用户脚本、宽泛路径改写或放宽管理 API CORS；日志不记录脚本正文、播放地址或请求查询。

固定同源脚本的外部播放器模块只处理本系统接管后的 PlaybackInfo：候选必须是同源 gateway stream，且恰好包含一个短时 `omc_ticket` 与一个 MediaSource 绑定；普通 Emby 媒体不渲染入口。PotPlayer、VLC、MPV、IINA、Infuse、MX Player 和弹弹Play 等协议链接最多包含网关地址与短时票据，不得包含 Emby Token/API Key、115 Cookie、provider file ID、原 signed STRM URL 或最终 CDN URL。Fanart 模块仅通过当前 Emby 用户会话读取 `BackdropImageTags` 与同源图片 API，不引入第三方脚本、图标或 CDN。两个内建模块只由 revision CAS 保护的布尔策略启停，不能承载用户脚本；策略修改推进 gateway revision 并使旧 ticket 失效。

115 signed 302 的多设备兼容使用有界 lease，不把复制能力开放成通用代理参数：第一台活动设备读取原文件，第二台只可在 `/OhMyCine/.playback-copies/lease-*` 创建一个系统持有的短命副本，第三台安全限流。设备路由摘要由 Remote IP 与 User-Agent 生成但不参与鉴权，数据库和日志均不保存原始值。副本只按持久化的精确目录 item ID 送入回收站并永久清理；回收站安全码使用 AES-GCM 保存，缺失或错误时保留待清理事实并重试，自动路径永不以空 ID 清空用户整个回收站。

管理员显式配置的账号级定时清空是另一条独立危险能力：默认关闭，只挂在 115 Connection，首次启用必须二次确认，并要求已有或本次提交有效操作密码与合法 5 段 Cron。空密码输入保留原密文，显式移除才清空，策略启用时禁止移除。调度 Job 不携带密码、Cookie、上游条目或路径，只携带 Connection ID 与 revision；执行前必须再次验证连接/策略仍启用、revision 未变化且密码仍配置。全量接口必须调用 SDK `CleanRecycleBin(password)` 且不传 item ID，不能把空 ID 交给精确清理能力。日志、审计、队列 DTO 和错误信息只保存稳定错误码，不保存密码或上游响应正文。

### 8.2 签名 URL

STRM 内容建议：

```text
http://server:3000/proxy/alist/media/movies/Inception.mkv?exp=1735689600&sig=...
```

签名内容：

```text
HMAC-SHA256(secret, method + path + exp + user_or_library_scope)
```

校验要求：

- `exp` 过期后拒绝访问
- `sig` 不匹配拒绝访问
- 路径必须规范化，禁止 `../`、重复编码绕过
- 可选绑定客户端 IP 或媒体库 ID
- 持久 STRM 继续使用有过期时间的 capability，不得改成永久 URL，也不得写入 provider item ID、pickcode、Cookie 或临时直链。生成器可复用现有 URL，但必须重新验证固定 public origin、opaque/library、HMAC、active managed manifest、当前 key/格式，并且剩余有效期超过七天；否则安全续签一次。

### 8.3 URL 缓存

真实网盘下载 URL 通常带过期时间。

要求：

- 缓存 TTL 不超过上游 URL 的过期时间
- 缓存键包含 driver、path、用户/权限上下文
- 缓存内容不写入日志
- 缓存命中时仍需校验外层代理权限

### 8.4 Range 与 Header

- 302 模式下 Range 请求由客户端直接发给 CDN
- 如果未来支持反向代理模式，必须正确透传 Range、Content-Type、Content-Length
- 不允许把上游要求的敏感 Header 直接暴露给前端，除非该网盘协议必须如此

## 9. 文件与路径安全

媒体库作品源文件删除不能复用“删除媒体库配置”。它要求独立 `media_libraries.media_delete` 权限、五分钟 actor-bound 单次 opaque token，并在 confirm 时重新对账 library/work revision 与完整 entry digest。浏览器不能提交绝对路径或 provider ID 作为授权：本地目标只能由 Storage root + library relative root + entry relative path 重算，并拒绝 traversal、symlink、junction/Reparse Point；115 必须先证明媒体库 provider root 仍在 Storage root 内，再逐项证明 item identity、parent、name 和 size 未漂移，只能回收预览中的精确 item，不能清空回收站。逐项完成状态必须持久化以便部分失败后收敛，数据库 catalog 不得先于源文件删除。审计只保存库 ID、作品键摘要、数量、storage type、结果和安全错误码。

### 9.1 路径规范化

所有本地文件操作必须：

- 使用绝对路径或受控根目录
- 清理 `..`、符号链接逃逸、重复分隔符
- 校验目标路径位于允许的根目录内
- Windows 下同时处理 `\`、盘符、UNC 路径

### 9.2 转移策略

| 策略 | 风险 | 要求 |
|------|------|------|
| move | 误移动、覆盖文件 | 目标存在时默认不覆盖 |
| copy | 大文件占用空间 | 复制前检查剩余空间 |
| hardlink | 跨文件系统失败、保种路径混乱 | 失败时不自动降级为复制，需用户确认 |
| symlink | 符号链接逃逸 | 默认仅 admin 可启用 |
| delete | 数据不可逆 | 高风险操作需要确认和审计日志 |

### 9.3 STRM 清理

无效投影清理必须：

- 仅删除 manifest 中 inactive、managed、`local_projection` 的托管产物；范围包括 kind/扩展名一致的 STRM、NFO、生成 JPG、字幕和 generation policy 快照内的源伴随文件，不得根据目录扫描猜测删除对象。
- 自动清理仅由完整成功、非 partial 且类型白名单内的权威扫描触发；失败、partial、superseded、投影根变化或边界异常时保留文件。
- 自动与人工路径共用一个删除 primitive；执行时持有同库扫描锁，每文件先以 generation/root/manifest/ownership/path/kind/fingerprint CAS 持久化 claim，再删除文件并与 manifest/计数事务收敛。
- 每个删除边界重新检查 canonical root、manifest snapshot 和 generation，不跟随 symlink、Windows junction 或其它 reparse point 到外部文件。
- 人工清理必须先预览，再使用绑定 operation、actor、library、generation、完整 root identity set hash、manifest snapshot 和过期时间的短时 HMAC token。投影根更换后，旧根只能从 artifact owner 的不可变 policy 解析；token 严格限长、使用 canonical Base64URL/严格 JSON，不编码任何绝对根路径。
- 清理状态持久化为 `pending|running|completed|failed|skipped`；崩溃重放不双重计数，删除失败不回滚已完成的产物 generation。
- 日志、审计和 API 仅暴露内部 ID、计数、状态与稳定安全错误码，不记录绝对路径、provider 身份、临时 URL 或凭据。

### 9.4 下载暂存垃圾清理

- 下载完成必须同时保存 provider 完整清单与经过统一识别、分类和主视频/剧集筛选的安全入库清单；自动清理候选只能是两者按稳定身份计算的精确差集。
- 两份清单都必须完整，安全入库清单必须是完整清单的严格子集，且识别、转移和目标对账全部成功。任一条件不满足时保留全部来源数据，不通过目录扫描重新猜测“垃圾文件”。
- 本地项逐个复验系统暂存根边界、普通文件类型、Reparse Point/symlink 和快照大小；115 项逐个复验稳定 item ID、暂存根 ancestry、父目录、大小与可用 SHA1，只回收该精确 item。
- qBittorrent `copy|symlink` 在做种结束前保持来源包完整。copy 的 provider `deleteData=true` 成功后记录整包清理；symlink 的 `deleteData=false` 保留链接目标，只清理未选中差集。失败与重试累计计数但不扩大候选集合。
- 普通媒体源、用户任意目录和媒体库根不属于此自动清理授权。删除任务历史也不得借用该能力删除文件。

## 10. 外部服务访问安全

### 10.1 HTTP 客户端

所有外部请求统一使用受控 HTTP Client：

- 设置超时
- 限制重定向次数
- 限制响应体大小
- 校验 URL scheme，仅允许 `http` / `https` / WebDAV 对应协议
- 可配置代理，但代理配置不通过普通用户接口暴露

### 10.2 SSRF 防护

用户可配置 URL，因此 Server 需要防 SSRF：

- 管理员配置的 URL 默认可信，但测试连接时仍要限制危险 scheme
- 普通用户输入的 URL 不允许访问内网管理地址
- 插件/站点适配器发起请求需要走统一 HTTP Client
- Emby 登录、系统信息、媒体库、搜索、详情、标记已观看、PlaybackInfo 和播放进度 JSON 请求统一走 Tauri Rust 受控客户端；只允许 GET/POST、HTTP(S) Base URL 和根路径，15 秒超时、禁用自动重定向、限制查询/请求体，并对声明长度与实际流式读取同时执行 4 MiB 响应上限。浏览器开发 fallback 也必须使用 `redirect: 'error'`、AbortController 和流式大小限制，不得退回无边界 `ofetch`。
- 禁止访问 `file://`、`gopher://`、`ftp://` 等非预期协议

115 跨数据源物化使用专用 `ReadDriver`，不是通用 URL 下载器。115 adapter 用稳定 file ID 在单次内存调用中取得临时地址，Cookie、Authorization、pickcode、SDK acquisition headers 和临时 URL 不得离开 adapter，也不得进入 Job/checkpoint、数据库、WebSocket、日志或审计。初始地址及每次跳转都必须重新验证为公网 HTTPS/443，拒绝 userinfo、fragment、HTTP 降级以及 loopback/private/link-local/multicast/unspecified 地址；连接前再次解析并只拨号到本次复验的公网 IP，防止 DNS rebinding。跳转最多三次，每一跳只保留 `User-Agent`、`Range`、`Accept-Encoding`，其它 Header 全部清除。

跨源文件只写入统一暂存根下 `.omc-cross-source/<transfer UUID>/` 的任务私有 `.partial`。断点续传只有在 `206 Content-Range` 起点与 checkpoint 完全一致时才能追加；服务端忽略 Range 时关闭响应并从零重启，矛盾响应直接失败。完整文件必须再次核对 provider package-root ancestry、稳定 file ID、parent、size 和 SHA1，写盘后 flush、校验完整 size/SHA1，再原子改名，半成品不能进入识别或目标库。取消只删除该任务根中的 `.partial`，保留已完成文件供安全重试或人工修正；目标对账成功后才能删除该 UUID 根，清理不得从全局暂存根递归，也不得跟随 symlink、Junction 或其它 Reparse Point。

站点页面渲染是更窄的专用边界，不复用普通可配置 URL 规则：

- 自动 `RenderedFetcher` 首版只接受 Server 已登记的公开 BT `1337x` / `EXT.to` profile，并同时复验精确 HTTPS/443 目标 host 与最终 URL；请求参数不能增加 host，也不能把它变成任意 URL 浏览器代理。
- CloakBrowser 只作为用户显式安装、接受上游许可后的本机 companion；Server 仅连接 loopback，OhMyCine 不自动下载、打包或重新分发其浏览器二进制。
- 每次请求限制超时、HTML/JSON 响应大小与重定向；Cloak 不可用时只回退该站点显式配置的 FlareSolverr，不扩大到其它站点或内网地址。
- RenderedFetcher 请求合同不含 Cookie/passkey。公开 BT 无站点凭据；PT Cookie/passkey 只由 Server 直连站点使用，既有 PT FlareSolverr 入口也不得把这些凭据传给外部 solver。
- 日志、审计、普通 DTO、SSE 与浏览器存储不得出现 companion/Flare 请求正文、profile 内部状态、Cookie、passkey 或上游响应正文。

### 10.3 日志脱敏

日志中必须脱敏：

- `Authorization` Header
- Cookie
- API Key
- Passkey
- JWT
- 下载器密码
- 真实 CDN URL 中的 token 参数
- AI API Key

示例：

```text
115 cookie=***redacted***
api_key=sk-***redacted***
Location=https://cdn.example.com/file?token=***redacted***
```

Server 运行日志在 stdout 与文件分流前执行同一套结构化脱敏，查询 API、Web UI 和导出只读取已脱敏 JSONL，不能依赖前端遮盖。HTTP 请求日志只记录规范化路由，不记录 raw query 或正文；媒体/存储/扫描事件使用 provider-relative path 或资源 ID，不记录 Server 绝对路径。日志查询、导出和策略修改分别由 `logs.read`、`logs.export`、`logs.configure` 控制，并在 permission middleware 前设置 `Cache-Control: no-store`。导出会记录脱敏审计摘要；运行日志轮转不得操作 SQLite 审计记录。

插件日志的 `plugin_id` 必须由宿主 Logger 绑定，插件提供的同名字段不能覆盖宿主身份。日志 reader 只识别配置目录内由应用生成的严格文件名，游标绑定规范化筛选，不允许客户端提供文件名或路径。文件 sink 故障只触发限频的 stdout 降级诊断，不得递归写入失败 sink 或导致 Server 退出。

### 10.4 Player 字幕提供器凭据与下载边界

- OpenSubtitles API Key 模式使用 OpenSubtitles.com REST API；账号密码模式使用固定 HTTPS OpenSubtitles.org 旧 XML-RPC 接口，两种模式互斥。旧接口对现代邮箱账号返回 401 时只允许回退到官方匿名 XML-RPC 会话，并必须向用户显示未认证兼容状态；不得尝试网页抓取、复用第三方应用 API Key 或把 401 伪装成账号登录成功。API Key 或账号密码进入 Player 凭据库，认证/匿名 XML-RPC 会话只缓存于 Rust 进程内，不写入 SQLite 普通设置、日志或导出配置。
- 射手网和迅雷 CID 增强对本地文件直接计算内容哈希，对远程媒体只在 Rust 中使用当前播放 URL 与 Header 做受限 Range 取样。播放 URL、签名参数、Authorization/Cookie 和数据源 Header 不进入 Vue 结果状态、日志或字幕提供器请求；射手网只接收哈希、安全文件 basename 和语言，迅雷名称接口只接收用户选定的媒体名、文件名或自定义关键词。
- 远程媒体哈希必须固定为 HTTP(S)，限制 Header 数量与总大小，禁止调用方覆盖 `Range/Host/Content-Length/Connection/Accept-Encoding`，先用单字节 Range 验证总大小，再读取算法要求的精确片段。重定向次数受限，跨源时清空数据源 Header，HTTPS 不得降级到 HTTP，服务端忽略 Range 时直接停止而不是下载完整媒体。
- 射手网搜索和下载固定到 HTTPS `www.shooter.cn`。迅雷名称搜索固定到 HTTPS `api-shoulei-ssl.xunlei.com/oracle/subtitle`，CID 只作为本机限时可选增强，Range/302/CID 失败不得阻断名称结果；下载 URL 必须限制到 HTTPS `subtitle.v.geilijiasu.com`。
- 迅雷结果精筛使用的媒体类型、年份、原始标题、时长、季集号和文件名 token 只在 Player 本地参与评分，不得作为额外查询参数、日志字段或诊断明文发送到迅雷；外部请求仍只包含用户当前选定的单一搜索词。
- 射手网和迅雷返回的下载 URL 不进入 Vue 状态、设置或日志。Rust 使用有数量和时间上限的短期不透明引用映射，并在下载时再次校验提供器和域名。
- 所有字幕下载限制响应大小、重定向次数和 `srt/ass/ssa/vtt/sub` 扩展名，拒绝未知压缩包，并写入当前存储模式下按来源和媒体身份哈希隔离的 `cache/subtitles` 子目录。

### 10.5 Player 下载与离线媒体包边界

- Player 下载任务只持久化稳定的 `sourceId/itemId/mediaSourceId/variantId`、可选 Server 在线作品身份、目标目录引用、受控文件名、字节 checkpoint 和安全状态。临时 URL、302 Location、Authorization、Cookie、API Key、Server device token、Provider Header 与签名参数只允许存在于单次 Rust 解析内存中，禁止进入 SQLite、Tauri 事件、Vue Router、日志和诊断。
- 下载地址每次从稳定身份重新解析；跨源重定向必须清空 Provider Header，HTTPS 不得降级到 HTTP。续传必须同时证明正确的 `206 Content-Range` 与同一媒体实体（强 ETag，或 Last-Modified + 总大小）；不可信 Range、身份变化或分段覆盖不完整时只删除当前任务精确拥有的 partial/checkpoint 并从零开始。
- 桌面目标目录在入队、写入、重命名、解析和删除时都重新约束到用户选择的 root，并拒绝 traversal、符号链接、junction 与 Windows reparse point；Android 只在持久 SAF tree grant 内按任务拥有的文档名操作。取消后用户任务立即消失，暂时失败的清理只进入内部 cleanup 表，不得成为可重试下载。
- 离线包数据库只保存包内受控相对资产路径。海报/背景/still、字幕和弹幕分别执行类型、魔数或 UTF-8/JSON 结构及大小限制；弹幕最多 20 万条。远程附件命令最多接受 32 个 Header、单值 4 KiB、合计 16 KiB，并拒绝 Host、Range、Content-Length、hop-by-hop 和 request-shaping Header。
- 附件替换必须先把新内容写入同目录临时文件，`sync_all`、原子重命名并成功登记数据库，再清理旧文件。网络、校验或数据库失败不得删除已有完整资产，也不得把完整视频降级为失败。
- 本地优先播放前由 Rust 重验 root/SAF 所有权、存在性、大小与实体指纹；文件缺失或被同大小替换时删除错误离线事实，并仅在原来源可用时在线回退。离线历史和进度仍使用原来源媒体身份，不使用离线数据库行 ID。
- Server 在线插件没有用途受限、稳定可重新解析的离线流契约时必须禁用 Player 离线下载；禁止复用只为播放设计的短期 URL 绕过设备令牌、媒体权限或下载限制。

### 10.6 Player 更新信任根

- Player updater 公钥可以提交到仓库和打包进应用；私钥不得提交、打印、写入 Release notes 或普通构建产物。
- 本地私钥默认位于 `~/.config/ohmycine/updater/ohmycine-updater.key`，权限必须为 `0600`，并在首次正式发布前做离线备份。
- GitHub Actions 只通过 `TAURI_SIGNING_PRIVATE_KEY` 和可选 `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` Secret 读取私钥。Secret 缺失时发布必须失败，禁止生成无签名清单。
- 更新清单和安装包只接受固定 `yuanjing-hash/OhMyCine` GitHub Release asset；用户不能配置自定义 updater URL，避免把更新器变成 SSRF 或任意代码安装入口。
- `latest.json` 必须包含目标平台安装包 URL 和 `.sig` 内容。Tauri updater 在安装前校验 minisign 签名；SHA-256 只用于人工校验，不能替代签名信任根。
- 更新发现后必须由用户确认。自动检测不等于自动安装，不能在播放中静默退出应用。
- 便携模式只把 NSIS 目标目录设置为当前 EXE 目录，不删除 `portable.flag`、`data`、`cache` 或 `logs`，也不把便携配置迁入标准目录。
- 私钥丢失后不能仅替换应用内公钥来修复已安装客户端；必须保护并备份原私钥。

## 11. 插件与 Hub 安全

插件系统是长期能力，但安全边界需要提前设计。

### 11.1 插件默认策略

- 默认不自动安装插件
- 默认不自动更新插件
- 安装插件前展示权限声明
- 插件启用、禁用、更新、删除需要审计记录
- 第三方插件默认不允许读取全局凭据

### 11.2 插件运行方式

优先考虑 WASM 插件沙箱，而不是 Go `plugin` 热加载。

| 方案 | 优点 | 风险 |
|------|------|------|
| Go plugin | 性能好，Go 生态直接复用 | 平台限制多，进程内执行，不易隔离 |
| WASM | 权限边界清晰，跨平台较好 | 接口设计成本高 |
| 外部进程 | 隔离强，语言无关 | IPC 和部署复杂 |

正式方向：

1. PT 站点适配器保持 Server 内建；通用第三方能力通过版本化 WASM 插件协议扩展，Bilibili 只是首个真实参考插件
2. 插件只在 Server 安装运行，Player 只消费 Server 校验后的声明式 DTO 和播放方案
3. 用户在 Server 插件页添加 `https://github.com/{owner}/{repo}` 仓库地址；Server 固定 GitHub 提交读取标准 Registry，只从同仓库 GitHub Release 下载包
4. 安装必须校验 Manifest、Server 兼容范围、SHA-256、可选签名和权限；不得直接执行仓库源码、raw URL 或任意压缩包
5. 高风险插件能力必须通过权限声明控制，新增权限的升级需要再次确认
6. `.omcp` 的发布包 SHA-256 与解包后整树 SHA-256 必须分别保存；确认、启用、回滚和 Server 重启恢复前逐文件复验，不能只信任内容寻址目录名
7. 安装预览必须绑定管理员、安装/升级操作、安装修订和固定仓库提交；确认时重新验证仓库仍启用且 Registry 中 Manifest URL、Package URL 与摘要完全一致
8. 运行时替换与数据库状态写入失败时必须恢复受校验旧版本；无法恢复或补偿写入失败时停止插件并标记故障，不能留下虚假的“运行中”状态
9. 插件不获得本地绝对路径、115 Cookie、Storage 凭据、上传、移动或删除能力；它只返回受校验的 DownloadPlan/ProviderMetadata，由 Server 内置下载、TransferService 和 Storage Driver 执行通用动作
10. `media.metadata` 必须绑定原插件 ID、实际版本、连接和内容身份；其图片等 opaque asset 也必须绑定同一插件连接。已保存快照在运行时查询之前优先使用，不得因插件停用/卸载而改用全局元数据，也不得为其他来源提供候选
11. `settingsPage` 只是经 Manifest 校验的宿主组件树；未知组件、重复绑定、Schema 外字段、enum 外选项、放宽数值边界和任意 HTML/JavaScript/CSS 必须在安装时拒绝
12. 插件分层导航的 node key 不直接提供给 Player。Server 签名 token 必须绑定在线媒体库、深度、祖先链和短期有效期，并限制最大深度/宽度、拒绝循环与跨库复用
13. 普通插件 HTTP 继续禁止自定义端口；短时在线播放资产可使用 Server 内建的已知 CDN HTTPS 端口白名单（当前 `443/4483/8082`），但注册、读取和每次重定向都必须重新验证 Manifest 域名权限与公网 IP。插件升级新增 CDN 域名仍必须重新确认权限

### 11.3 插件权限模型

插件声明能力：

```json
{
  "permissions": [
    "network:site.example.com",
    "storage:read:media",
    "events:subscribe:download.completed"
  ]
}
```

禁止默认授予：

- 读取所有连接凭据
- 删除文件
- 执行系统命令
- 访问任意网络地址
- 修改用户和权限配置

通用存储上传不是插件 Host API。只有 Server Transfer worker 可在重新校验任务专属 managed staging root、普通文件类型和大小后调用 `UploadDriver`。覆盖仅允许精确回收媒体库根内已对账的冲突项；上传结果不明时保留 staging，重试必须先分页对账已完成项，不得盲目再传一份。

## 12. AI 功能安全

AI 推荐默认在 Player 侧使用用户自己的 API Key；Server 只为管理员显式开启的低置信媒体识别提供独立 AI Provider 设置，两者不自动同步凭据或上下文。

共同要求：

- Player AI API Key 保存到系统 Keychain；Server AI API Key 使用 AES-GCM 凭据 envelope，不进入普通配置、日志、审计详情或默认导出
- 发送给 LLM 的内容默认只包含必要媒体元数据，不包含本地绝对路径、provider item ID、magnet/torrent URL、Cookie、token、下载器信息或其它凭据
- 用户可选择是否允许 Player 发送简介、文件名、观看历史；Server 识别默认只发送相对 basename，并允许管理员进一步关闭
- RAG 检索结果只包含用户库中已有媒体
- 不允许 AI 直接执行删除、下载、移动、覆盖、改配置或锁定媒体身份等操作
- 自定义 AI Provider Base URL 使用受控 HTTP Client，执行 scheme、SSRF、重定向、超时和响应大小限制，并明确提示管理员风险

Server 媒体识别额外要求：

- 总开关默认关闭；关闭时下载、扫描、Transfer、重试和后台任务不得解密 API Key、构造 Provider 或产生 AI 网络请求。管理员显式“测试连接/获取模型”是独立配置动作
- 仅支持版本化的候选仲裁与标题重写两种严格 Schema；原标题和文件名必须标记为不可信数据，模型不得把其中内容当作指令
- 候选仲裁只能返回输入中的 `candidate_ref`；标题重写只产生结构化查询事实，仍须由 Server 重新搜索 TMDB 并复验
- 每个 identity revision 最多一次候选仲裁和一次标题重写，禁止递归调用或无限重搜；模型自报 confidence 不参与安全授权
- Provider 输出必须经过 JSON Schema、长度、枚举、候选引用及季集范围校验。非法、超大、非 JSON、超时或网络失败统一回到确定性的 provisional/local_provisional 兜底，不得阻塞队列或扩大文件操作权限

## 13. WebSocket 安全

- Player 媒体库 change feed 不复用本节管理 WebSocket；它使用可携带 Authorization Header 且每次重验权限的 device Bearer 长轮询。
- WebSocket 连接必须认证
- 事件按用户权限过滤
- 普通用户只能收到自己的下载/追更事件，以及有权访问的媒体事件
- 服务端限制消息频率，避免大量进度事件导致前端或网络压力
- 不通过 WebSocket 推送敏感凭据

## 14. Docker 与部署安全

### 14.1 默认部署

Docker Compose 默认只暴露必要端口：

- Server API：3000
- Emby/Jellyfin：用户可选
- qBittorrent Web UI：默认建议只绑定内网或 localhost

示例：

```yaml
ports:
  - "127.0.0.1:8080:8080" # qBittorrent Web UI 默认不公网暴露
```

### 14.2 挂载目录

- 数据库、配置、日志、STRM、下载目录分开挂载
- Server 容器不默认挂载宿主机根目录
- 文件管理功能只能访问配置过的存储根目录

### 14.3 HTTPS

- 局域网部署可使用 HTTP
- 公网暴露时必须放在反向代理后并启用 HTTPS
- 文档中需要提醒用户不要直接公网暴露下载器和未加固的 Server

## 15. 审计日志

需要记录：

- 登录成功/失败
- 用户创建、删除、权限变更
- 连接创建、修改、删除
- 下载器、站点、网盘配置变更
- 下载任务创建、删除
- 文件删除、移动、重命名
- STRM 清理
- 插件安装、启用、更新、删除
- 302 代理异常访问、签名失败

审计日志不记录敏感字段原文。

## 16. 安全默认值

| 项目 | 默认值 |
|------|--------|
| 默认管理员密码 | 首次启动必须修改 |
| Server API | 默认需要认证 |
| 302 代理 | 默认 signed-url 或 authenticated |
| 配置导出 | 默认脱敏 |
| 配置同步 | 默认不同步敏感字段 |
| 插件 | 默认禁用自动安装和自动更新 |
| 文件删除 | 默认需要确认 |
| symlink 策略 | 默认仅 admin 可启用 |
| qBittorrent Web UI | Docker 示例默认不公网暴露 |
| 日志 | 默认脱敏 |

## 17. MVP 阶段安全要求

即使项目初期只实现 Player 和部分 Server，也需要保留以下安全要求：

### Player MVP

- 敏感凭据不明文写入普通配置文件
- 本地配置导出默认脱敏
- AI API Key 保存到系统安全存储
- 外部 URL 请求设置超时和错误处理

### Server MVP

- 首次 owner 设置、opaque Cookie Session、CSRF 和登录限速
- API 级 permission code、owner/最后管理员/防权限提升不变量与审计基础
- 数据库敏感配置加密
- 302 代理默认不匿名公开
- STRM 签名 URL 或内网白名单至少实现一种
- 日志脱敏
- 文件路径限制在配置的根目录内

### 后续阶段

- PT 站点和追更上线前补充 Cookie/Passkey 风险提示
- 插件系统上线前实现权限声明和沙箱策略
- 多用户上线前完成 API 级权限校验和事件隔离

## 18. 待决策问题

| 问题 | 推荐方向 |
|------|----------|
| 302 代理默认使用 signed-url 还是 trusted-lan | signed-url，更安全 |
| Player 凭据是否必须使用系统 Keychain | 桌面端必须优先使用，Linux 无可用服务时提示风险 |
| 插件系统使用 Go plugin 还是 WASM | 正式默认使用 WASM；PT 适配器内建，非 PT 站点通过 Server GitHub 插件仓库分发 |
| Server 是否支持公开公网访问 | 支持，但文档强制建议 HTTPS + 反向代理 |
| 配置完整同步是否默认开启 | 不默认开启，必须用户确认 |
