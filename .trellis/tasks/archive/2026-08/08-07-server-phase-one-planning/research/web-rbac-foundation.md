# Research: Server Web 管理端与细粒度 RBAC 基础

- **Query**: 研究 OhMyCine Server 自带 Vue 管理网页的首版架构与细粒度 RBAC；覆盖 Go/Gin + SQLite/GORM 最小模型、页面/导航/按钮/API 共享 permission code、自定义角色和最后管理员保护、Vue/Vite 随 Go 二进制部署，以及 CSRF/JWT/Cookie 选择。
- **Scope**: mixed
- **Date**: 2026-08-07

## Findings

### Files Found

| File Path | Description |
|---|---|
| `docs/architecture/02-server-design.md:1153` | 现有用户模型只有 `role` 字符串和“可访问页面列表”JSON，无法可靠表达按钮/API 权限。 |
| `docs/architecture/02-server-design.md:1270` | 已规划 `/api/v1/` 全功能 API 和用户 CRUD，但多数端点没有细粒度权限契约。 |
| `docs/architecture/02-server-design.md:1609` | 现有 SQLite 草案同样把角色和页面权限直接放在 `users` 表，需要拆为 user/role/permission 关系。 |
| `docs/architecture/07-security-design.md:70` | 要求 Server 管理 API 默认登录、302 默认不公开、同步秘密显式确认。 |
| `docs/architecture/07-security-design.md:76` | 当前认证方案写作“短期 JWT + 可选 refresh token”，但未区分同源管理网页与 Player/CLI API 客户端。 |
| `docs/architecture/07-security-design.md:110` | 已定义 admin/user/readonly 和服务端强制校验原则，可演进为稳定能力码。 |
| `docs/architecture/07-security-design.md:149` | 外部连接秘密必须 AES-256-GCM 加密；RBAC 不能让普通“读取连接”权限隐式获得秘密。 |
| `docs/architecture/06-roadmap.md:410` | 旧路线图只安排 admin/user 中间件，没有角色、权限目录、会话撤销和管理端部署边界。 |
| `.trellis/tasks/08-07-server-phase-one-planning/prd.md:22` | 首切片原计划先单管理员认证；用户现已明确要求首个 Server 版本同时建立独立 Web 管理端和多账户权限基础。 |
| `.trellis/tasks/08-07-server-phase-one-planning/research/initial-vertical-slice.md:63` | 原纵向切片从管理员初始化开始，适合把 Web 登录、连接管理、STRM Run 和刷新作为首批受保护页面。 |

### Current Design Gap

现有的 `User.Role + User.Permissions(JSON 页面列表)` 应当废止，而不是在其上继续叠加按钮权限：

```go
Role        string // admin / user
Permissions string // JSON 页面列表
```

主要问题：

1. “能看到页面”不等于“能执行 API”。隐藏按钮不能形成安全边界。
2. 页面名、Vue 路由名和 HTTP 路由容易漂移，权限字符串无法稳定演进。
3. 每个用户复制一份 JSON，无法统一修改角色，也无法审计角色变更。
4. 无法区分读取、创建、测试连接、删除、清理 STRM、导出秘密等风险不同的动作。
5. 无法安全处理多角色、系统内置角色、自定义角色和权限升级防护。

## Recommended Architecture

### 1. 首版定位

Server 第一个小版本应当同时交付“可用的 Web 外壳 + 可复用的认证授权基础”，但页面仍围绕首个纵向闭环，而不是一次铺完所有未来业务：

```text
首次初始化 / 登录
  → 首页与系统状态
  → OpenList/Alist 连接
  → 存储目标
  → STRM 同步 Run
  → Emby/Jellyfin 刷新
  → 用户与角色
  → 审计 / 基础设置
```

PT、下载器、追更、插件等后续页面沿用同一权限目录追加能力码，不改变 RBAC 结构。

### 2. 最小持久化模型

建议从第一版就使用多对多角色模型；“多角色权限取并集”简单、成熟，也避免未来从单 `role_id` 做破坏性迁移。

#### `users`

| 字段 | 建议 |
|---|---|
| `id` | SQLite integer PK；API 可另给不可猜的 public ID，但首版不是必需。 |
| `username` | 展示值。 |
| `username_normalized` | 小写/规范化后唯一索引，避免大小写重复账户。 |
| `password_hash` | Argon2id 优先，bcrypt 也符合现有安全文档。 |
| `status` | `active` / `disabled`，禁用后所有会话立即失效。 |
| `is_owner` | 首次初始化账户的不可隐式降权“实例所有者”标记。 |
| `authz_version` | 用户角色变化时递增，便于会话/权限缓存立即失效。 |
| `last_login_at` | 可空。 |
| timestamps | `created_at`, `updated_at`。 |

#### `roles`

| 字段 | 建议 |
|---|---|
| `id` | PK。 |
| `code` | 稳定唯一标识；系统角色不可重命名 code。 |
| `name` / `description` | 可本地化展示，不参与鉴权。 |
| `kind` | `system` / `custom`。 |
| `protected` | 系统保护角色不可删除；权限集合只能由版本迁移维护。 |
| timestamps | 审计角色变化。 |

首版内置角色建议为：

- `administrator`：全部能力；不通过给每个新权限补 join row 来决定是否拥有新能力，授权器明确识别系统管理能力。
- `operator`：管理连接、目标、同步 Run 和媒体服务器刷新，但不能管理用户、角色、安全设置和秘密导出。
- `viewer`：只读状态、连接脱敏摘要和 Run 结果。

#### `permissions`

| 字段 | 建议 |
|---|---|
| `code` | 字符串主键，是安全契约。 |
| `module` | 角色编辑器分组，如 `connections`, `strm`, `users`。 |
| `name` / `description` | 展示文案。 |
| `risk` | `normal` / `sensitive` / `destructive`，用于 UI 警示和二次确认。 |
| `deprecated_at` | 权限退役时先标记，不直接复用旧 code。 |

权限目录由代码/仓库内 canonical catalog 定义，启动迁移按 `code` 幂等补齐。不要允许管理员创建任意 permission code；管理员创建的是“角色组合”。

#### Join tables

```text
user_roles(user_id, role_id, assigned_by, created_at)
role_permissions(role_id, permission_code, created_at)
```

- 两张表都使用复合唯一键和外键。
- SQLite 必须显式启用 foreign keys。
- 角色权限替换、用户角色替换和审计日志在同一数据库事务中完成。
- 不要只依赖 GORM `AutoMigrate` 管理长期 schema；从第一版建立有版本号的 migration runner。

#### `sessions`

```text
sessions(
  id, token_hash, user_id,
  created_at, last_seen_at,
  idle_expires_at, absolute_expires_at,
  revoked_at, user_agent_hash, ip_hint
)
```

- Cookie 中只放高熵随机 session token，数据库只存 token 的 SHA-256 哈希。
- 密码修改、账户禁用、显式登出可撤销会话。
- 权限每次从数据库/短期缓存解析，并以 `authz_version` 或全局 RBAC revision 失效，不能把旧权限固化到长寿命 Cookie/JWT 中。

### 3. Permission code 是唯一共享契约

命名建议：小写、复数资源、动作结尾，格式固定为 `<resource>.<action>`。

首切片示例：

```text
dashboard.read
connections.read
connections.create
connections.update
connections.delete
connections.test
destinations.read
destinations.create
destinations.update
destinations.delete
strm.runs.read
strm.runs.create
strm.runs.cancel
media_servers.refresh
users.read
users.create
users.update
users.disable
users.delete
roles.read
roles.create
roles.update
roles.delete
roles.assign
audit.read
settings.read
settings.update
```

高风险能力必须单独拆分，例如未来的：

```text
files.delete
strm.cleanup
connections.secrets.export
plugins.install
```

不要创建 `page.connections`、`button.deleteConnection` 等 UI 专属权限。连接页面能否进入由 `connections.read` 决定，新增按钮由 `connections.create` 决定，后端 POST API 也要求 `connections.create`。

#### 单一来源与前后端共享

推荐 canonical source 位于后端 authz 包，例如：

```text
server/internal/authz/catalog.json
```

构建时生成：

```text
server/webui/src/auth/generated-permissions.ts
```

生成文件提供字符串常量和 TypeScript union；CI 重新生成并检查无差异。后端启动时校验 catalog 唯一性并同步 `permissions` 表。这样：

- Gin middleware 使用同一个 Go 常量/目录 code。
- Vue Router `meta.permissionsAny/permissionsAll` 使用生成常量。
- 导航从静态路由/导航定义中过滤，不从数据库动态拼任意组件路径。
- 按钮通过 `can(Permissions.ConnectionsCreate)` 或 `v-permission` 控制。
- 角色编辑器从 `GET /api/v1/permissions` 读取相同目录和分组说明。

`GET /api/v1/auth/me` 应返回当前用户、角色 code、排序后的有效 permission codes 和 owner 状态。前端 Pinia 保存它用于 UX；服务端仍是最终裁决者。

#### Route / nav / button / API 的映射

| 层 | 用法 | 安全地位 |
|---|---|---|
| Vue Router | `meta: { permissionsAny: [connections.read] }`；全局 guard 防止直接输入 URL。 | UX，不是安全边界。 |
| Navigation | 复用 route permission metadata 过滤菜单，避免再维护一套页面权限。 | UX。 |
| Buttons | `can(code)` 隐藏无权操作；因业务状态不可用时显示 disabled + 原因。 | UX。 |
| Gin route | `RequirePermission("connections.create")`。 | 必须。 |
| Service/policy | 校验 own/all、目标资源归属、危险操作条件；不能只相信 handler。 | 最终业务安全边界。 |

未来下载/追更任务的“本人/全部”不要用页面权限表达。首版可先用明确能力码，例如 `downloads.read_own` / `downloads.read_all`，后续资源量增加后再引入 Grafana 风格 scope；不要在首版同时实现通用 scope DSL。

### 4. 自定义角色：首个 Web 版本应该支持，但必须收窄

用户要求不同账户可以管理不同页面和按钮，因此首个可见 Web 版本就应支持自定义角色，否则权限体系只有开发者可配置，产品价值不完整。

首版支持：

- 创建/命名/停用自定义角色。
- 从后端提供的 permission catalog 勾选权限。
- 一个用户分配一个或多个角色；有效权限为 allow 集合的并集。
- 展示“敏感/破坏性”权限警告和变更差异。
- 用户、角色、权限分配全部进入审计日志。

首版明确不支持：

- deny 规则。
- 角色继承。
- 任意条件表达式/ABAC。
- 按单个 Connection/Destination 的资源 scope。
- 字段级读取秘密；外部服务秘密仍默认永不回显，只允许覆盖更新。

这与 Kubernetes 的“纯 additive、无 deny”模式一致，也符合 Grafana“先固定角色，确有需要再自定义组合”的经验。

#### 防止权限提升

拥有 `roles.update` 不应自动允许把自己变成管理员。除实例 owner / system administrator 外，任何角色创建、角色编辑或角色分配都必须满足：

```text
被授予的有效权限集合 ⊆ 操作者自己的有效权限集合
```

并且不能授予 `system.admin` 或 owner 身份。此规则同时用于：

- 给角色添加 permission。
- 给用户分配角色。
- 把某个普通角色改造成更高权限角色。

Grafana 的自定义角色也限制创建者只能授予自己已有的权限；Kubernetes 则对 bind/escalate 做专门防护。OhMyCine 应从第一版保留同样边界。

### 5. 防止删除或降权最后一个管理员

单纯在 UI 禁用按钮不够；所有用户/角色写操作必须走同一 `UserAdminService` / `RoleAdminService` 事务和不变量检查。

推荐双保险：

1. **Owner 不变量**：首次初始化事务创建唯一 `is_owner=true` 用户。owner 在首版不能被删除、禁用、取消 owner 或失去系统管理能力。未来若需要，提供独立“转移所有权”流程，要求重新输入当前 owner 密码并在单事务中完成。
2. **最后管理员检查**：删除/禁用用户、移除角色、编辑管理员角色权限前，计算变更后的 active `system.admin` 有效用户数；若为 0，返回稳定业务错误 `LAST_ADMIN_REQUIRED`。

额外规则：

- 首版禁止用户删除、禁用或降权自己，即使系统还有另一名管理员；由另一名管理员执行可减少误锁死。
- 系统 `administrator` 角色不可删除或重命名 code。
- owner/system admin 的敏感变更要求最近重新认证，而不是只依赖长期会话。
- 账户禁用和密码重置后撤销该用户所有 session。
- 启动时若数据库已经初始化却没有 owner，进入安全恢复状态并输出不含秘密的恢复指引，不能静默创建新的默认管理员。
- 初始化端点在 owner 创建成功后永久关闭；创建过程必须事务化并防并发重复初始化。

### 6. Vue 3 管理端结构

建议目录：

```text
server/
  webui/                     # 同时是 Vite root 和 Go webui package
    package.json
    vite.config.ts
    src/
      api/                   # typed fetch client；统一 401/403/CSRF/response envelope
      auth/                  # Pinia auth store、can()、permission directive
      router/                # typed RouteMeta permission requirements
      layouts/
      views/
      components/
    dist/                    # gitignored, Vite 产物
    embed_prod.go            # build tag，go:embed dist
    embed_dev.go             # 非 embed 构建，不要求 dist 存在
```

把 `embed_prod.go` 放在 Vite root 同级，是因为 `//go:embed` pattern 相对 Go package directory，且不能包含 `..`。生产构建顺序：

```text
npm ci (或项目统一包管理器 install --frozen)
→ npm run typecheck/lint/test/build
→ go build -tags webui ./cmd/server
```

开发模式：

- Vite 和 Gin 分离运行。
- 浏览器访问 Vite dev server。
- Vite 将 `/api`, `/ws`, `/proxy` 代理到 Gin；避免开发时开放宽松 credentialed CORS。
- 默认 Go 开发构建使用 `embed_dev.go`，因此尚未生成 `dist` 时 `go run`/`go test` 不会失败。

生产模式：

- Vite `dist` 通过 `embed.FS` 编进同一个 Server 二进制。
- Gin 先注册 `/api/v1/*`、`/ws/*`、`/proxy/*`，最后注册 SPA handler。
- SPA fallback 只对 GET/HEAD 且接受 HTML 的非 API 路径返回 `index.html`；丢失的 `/assets/*` 必须 404，不能回退成 HTML。
- 哈希静态资源使用长期 `immutable` cache；`index.html` 使用 `no-cache`，避免升级后二进制与旧入口错配。
- 管理 UI 不加载第三方 CDN 资源。设置 CSP：至少 `default-src 'self'`, `script-src 'self'`, `object-src 'none'`, `frame-ancestors 'none'`, `base-uri 'self'`，按实际 WebSocket/图片需求收窄 `connect-src`/`img-src`。

Vue Router 官方支持 typed route meta 和全局 guard；权限 meta 应定义为生成的 `PermissionCode[]`。Pinia 的 `/auth/me` 状态只用于菜单和交互，遇到后端 401 清会话状态，遇到 403 显示明确“权限已变更/无权操作”并刷新 `/auth/me`。

### 7. Cookie / JWT / CSRF 决策

#### 推荐结论

```text
同源 Server Web UI：opaque server-side session + HttpOnly Cookie
Player / CLI / 自动化客户端：Authorization Bearer token（设备 token 或短期 JWT）
```

不要让浏览器管理端把 JWT 放在 `localStorage`。对单体 Go + SQLite 管理端，服务端会话具备直接撤销、禁用用户立即生效、权限变化立即生效和审计清晰等优势；JWT 放进 Cookie 仍然需要 CSRF 防护，却增加撤销复杂度，收益很小。

Cookie 建议：

- HTTPS 部署：`__Host-omc_session`，`Secure; HttpOnly; SameSite=Lax; Path=/`，不设置 `Domain`。
- 若用户明确使用局域网 HTTP，无法使用 `Secure` / `__Host-` 前缀，应显示风险警告并使用普通 host-only Cookie；不要伪装成同等安全。
- session 登录后轮换，登出服务端 revoke 并清 Cookie。
- 可采用 2 小时 idle、7 天 absolute 上限以延续现有安全文档语义；首版不做永久“记住我”。

#### CSRF

Cookie 自动随请求发送，因此 `SameSite` 只能作为防御层之一。所有状态变更请求应同时满足：

1. `X-CSRF-Token` 校验。
2. `Origin`（必要时 Referer fallback）与配置的 Server public origin 匹配。
3. 拒绝明显 cross-site 的 Fetch Metadata 请求；项目最低 Go 1.22，不能假设 Go 1.25 的 `http.CrossOriginProtection` 已可用。
4. 仅接受受控 JSON content type；不开放任意 credentialed CORS。

一个不暴露 session cookie 给 JS 的简单方案：后端用独立 CSRF HMAC key 对请求中的原始 session token 派生 session-bound CSRF token；`GET /api/v1/auth/csrf` 返回派生值，Vue 只保存在内存并通过自定义 header 回传。后端 constant-time 校验。token 不进入 URL和日志。

登录端点没有已认证 session，也要做 Origin/Fetch-Metadata 校验、JSON content type 限制和 IP+username 组合限速。`logout` 必须使用 POST 并校验 CSRF。健康检查和签名播放 GET 不套用浏览器 CSRF，但仍执行各自认证/签名边界。

### 8. API 与服务边界

建议新增/明确：

```text
POST /api/v1/setup/owner              # 仅未初始化时
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/auth/me
GET  /api/v1/auth/csrf

GET  /api/v1/users
POST /api/v1/users
PATCH /api/v1/users/{id}
POST /api/v1/users/{id}/disable
POST /api/v1/users/{id}/enable
DELETE /api/v1/users/{id}

GET  /api/v1/roles
POST /api/v1/roles
PATCH /api/v1/roles/{id}
DELETE /api/v1/roles/{id}
PUT  /api/v1/roles/{id}/permissions
PUT  /api/v1/users/{id}/roles
GET  /api/v1/permissions
```

所有接口沿用项目标准 response envelope。密码、session token、CSRF token、外部连接秘密、完整 upstream URL 不进入普通审计 metadata 或日志。

管理端 handler 保持薄：解析输入 → RequirePermission → 调 service。最后管理员、owner、权限提升、角色权限差集和 session revoke 必须在 service/transaction 中统一处理，不能散落在 Vue 或单个 handler。

## Mature Patterns Compared

### Grafana RBAC

- 角色由多个 permission 组成，每个 permission 是 action + scope。
- 同时保留 basic/fixed/custom roles；fixed role 不可修改或删除，自定义角色用于固定角色不够时。
- 自定义角色创建者只能授予自己已经拥有的权限。
- **借鉴**：稳定 action code、保护系统角色、防权限提升、以后再加 scope。
- **不直接照搬**：Grafana 的完整 custom RBAC 属于 Enterprise/Cloud 范围且 scope 体系很大；OhMyCine 首版无需复制其复杂度。

### Kubernetes RBAC

- Role 保存纯 additive rules，没有 deny；RoleBinding 把用户/主体与角色分离。
- resource + verb 是稳定契约；绑定到哪个 role 的 `roleRef` 不可原地修改，以防委派者借更新 binding 提升权限。
- **借鉴**：roles 与 assignments 分表、allow-only、多角色并集、角色分配与角色内容是不同高风险动作。
- **不直接照搬**：OhMyCine 首版没有 namespace/cluster 两级 scope。

### Casbin

- 支持 user-role mapping、多角色、角色继承、menu permissions 和 domain RBAC。
- Casbin 只评估字符串 policy，不负责确认用户/角色对象真实存在；层级和 deny 组合也会增加运维解释成本。
- **结论**：首版不必引入 Casbin。当前固定能力目录 + 五张小表 + service policy 足够，并更易与 GORM 事务、owner 不变量和审计结合。若未来出现插件权限、资源 scope、组织/家庭多租户，再重新评估 Casbin 或自建 policy engine。

### OWASP Session / CSRF guidance

- 推荐 Cookie 作为浏览器 session ID 交换机制，并明确 `Secure`, `HttpOnly`, `SameSite` 和 host/path 范围。
- Cookie 认证的状态变更仍需要 CSRF token；stateful 应用优先 synchronizer token，custom header 受 same-origin policy 保护。
- **借鉴**：浏览器使用不可被 JS 读取的 session cookie，CSRF token 走 header，Origin/Fetch Metadata 作为纵深防御。

## External References

- [Grafana RBAC overview](https://grafana.com/docs/grafana/latest/administration/roles-and-permissions/access-control/) — action + scope、basic/fixed/custom role 模式；完整 RBAC 有 Enterprise/Cloud 版本约束。
- [Grafana create custom roles](https://grafana.com/docs/grafana/latest/administration/roles-and-permissions/access-control/create-custom-roles/) — 自定义角色应后置于固定角色，并限制只能授予操作者已有权限。
- [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/) — additive Role/Binding 分离、resource/verb、不可变 roleRef 和 privilege escalation 防护。
- [Casbin RBAC](https://casbin.org/docs/rbac/) — 多角色与继承能力，以及 authn/entity validation 不属于 Casbin 的边界。
- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) — Cookie 属性、session rotation/expiry 与安全交换机制。
- [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) — synchronizer token、custom header、SameSite 与 Fetch Metadata。
- [Go `embed` package](https://pkg.go.dev/embed) — embed pattern 相对 package directory，不能包含 `..`，`embed.FS` 可直接适配 `net/http`/`io/fs`。
- [Vite static deployment](https://vite.dev/guide/static-deploy.html) — 默认 `dist` 构建产物；`vite preview` 不是生产服务器。
- [Vite backend integration](https://vite.dev/guide/backend-integration.html) — 开发/生产前后端集成方式和 production manifest。
- [Vue Router route meta](https://router.vuejs.org/guide/advanced/meta.html) — typed meta 与全局 navigation guard。

## Related Specs

- `docs/architecture/02-server-design.md` — 需要把页面 JSON 权限模型改为 RBAC，并补 Server Web UI 部署设计。
- `docs/architecture/07-security-design.md` — 需要区分浏览器 Cookie session 与 Player/CLI bearer token，并补 CSRF/owner/最后管理员规则。
- `docs/architecture/06-roadmap.md` — 首个 Server 纵向切片应加入 Web 外壳、用户/角色/权限基础，但不因此横向铺完未来全部页面。

## Recommended First-Version Decision

1. 首个 Server Web 版本使用 Vue 3 + TypeScript + Vite + Pinia + Vue Router，开发态由 Vite proxy Gin，生产态构建后通过 build-tagged `go:embed` 进入单一 Go 二进制。
2. 用 `users / roles / permissions / user_roles / role_permissions / sessions` 建立基础，系统角色 + 受约束自定义角色从首个 Web 版可用。
3. permission code 是后端拥有的稳定安全契约；页面、导航、按钮和 API 使用同一 code，UI 只负责体验，Gin/service 负责强制授权。
4. 首版 allow-only、多角色取并集；暂不做 deny、继承、ABAC 和通用资源 scope。
5. 采用唯一 owner + 事务级最后管理员检查 + 权限提升子集检查，阻止删除/降权锁死和委派式提权。
6. 浏览器用可撤销的 HttpOnly Cookie session + CSRF header；Player/CLI 后续继续用 bearer/device token，不把浏览器 JWT 放入 localStorage。

## Caveats / Not Found

- `server/` 当前只有 `.gitkeep`，没有现成 Go module、Gin middleware、GORM migration 或 Web UI 可以复用。
- 当前本机 shell 未发现 `go` 命令，因此未执行 Go 原型或编译验证；`go:embed` 约束依据 Go 官方源码/文档。
- 本研究未选定管理端组件库。可在实现前比较 Naive UI / Element Plus，但组件库不应影响 permission code、会话和 API 安全边界。
- 资源级权限（只管理某一个 Connection/Destination）、组织/家庭多租户和插件 permission namespace 应在实际需求出现时另做 ADR，不要提前塞进首版 RBAC。
