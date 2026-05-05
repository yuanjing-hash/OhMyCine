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
| 网盘凭据 | 115 Cookie、OpenList Token、CloudDrive2 账号、WebDAV 密码 | 被盗后可访问或操作网盘文件 |
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
5. Player 与 Server 的配置同步默认只同步必要字段，敏感字段需要用户确认。

## 4. 认证与会话

### 4.1 Server 登录

Server 使用用户名密码登录，返回短期访问令牌。

建议：

- 密码使用 `bcrypt` 或 `argon2id` 哈希保存
- 首次启动必须强制修改默认管理员密码
- JWT `access_token` 默认短有效期，例如 2 小时
- 可选 `refresh_token`，默认 7 天有效期
- JWT 签名密钥启动时检查强度，不允许默认值 `change-me` 在生产模式运行
- 登录失败需要限速，避免暴力破解

### 4.2 API 鉴权

| API 类型 | 默认策略 |
|----------|----------|
| `/api/v1/auth/login` | 匿名可访问，限速 |
| `/api/v1/health` | 匿名可访问，只返回基础状态 |
| `/api/v1/*` | 默认需要认证 |
| `/ws/events` | 需要认证，Token 绑定用户 |
| `/proxy/*` | 默认需要签名 URL 或认证 |

### 4.3 Player 连接 Server

Player 连接 Server 时支持两种方式：

1. 用户名密码登录，保存 refresh token
2. Server 生成设备授权 Token，Player 输入或扫码绑定

推荐长期使用设备授权 Token，便于撤销单台设备访问权限。

## 5. 权限模型

### 5.1 角色

| 角色 | 权限 |
|------|------|
| admin | 管理所有连接、用户、下载器、站点、网盘、系统设置 |
| user | 使用媒体库、发起下载、查看自己的追更和下载任务 |
| readonly | 只浏览媒体库和播放，不允许修改配置或发起下载 |

### 5.2 权限粒度

Server 设计中保留页面级权限，同时关键 API 需要服务端强制校验：

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
- CloudDrive2 WebDAV 账号密码
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

`CleanInvalid()` 删除 STRM 前必须：

- 仅删除 STRM 输出根目录内的 `.strm`
- 不跟随任意符号链接删除外部文件
- 支持 dry-run 预览
- 记录被删除文件列表

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
- 禁止访问 `file://`、`gopher://`、`ftp://` 等非预期协议

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

推荐顺序：

1. 站点适配器先作为内置驱动实现
2. 后续插件优先采用 WASM 或外部进程
3. 高风险插件能力必须通过权限声明控制

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

## 12. AI 功能安全

AI 功能默认在 Player 侧实现，使用用户自己的 API Key。

要求：

- AI API Key 保存到系统 Keychain，不进入普通配置文件
- 发送给 LLM 的内容默认只包含媒体元数据，不包含本地绝对路径和凭据
- 用户可选择是否允许发送简介、文件名、观看历史
- RAG 检索结果只包含用户库中已有媒体
- 不允许 AI 直接执行删除、下载、改配置等操作
- AI Provider Base URL 由用户配置，但需要明确提示风险

## 13. WebSocket 安全

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

- 登录认证和管理员账号初始化
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
| 插件系统使用 Go plugin 还是 WASM | 长期推荐 WASM，短期内置适配器 |
| Server 是否支持公开公网访问 | 支持，但文档强制建议 HTTPS + 反向代理 |
| 配置完整同步是否默认开启 | 不默认开启，必须用户确认 |
