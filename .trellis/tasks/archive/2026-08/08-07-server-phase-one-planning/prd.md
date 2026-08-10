# Server 第一阶段：管理网页与权限基础版本

## Goal

在 Player 第一个正式版发布后，让 OhMyCine Server 从空目录进入可运行开发阶段。首个小版本先交付 Server 自带的独立管理网页、首次管理员设置、登录会话、账户/角色/权限管理和页面/按钮/API 一致的权限控制，为随后接入 OpenList/Alist → STRM → 302 → Emby/Jellyfin 的媒体闭环提供安全且好用的操作底座。

## What I already know

* Player 已发布第一个正式版，项目重心可以开始转向 Server。
* Server 是独立的媒体自动化引擎，不只是 Player 的 API 后端。
* 核心媒体管线是 `Discover → Download → Transfer → Import → Notify`。
* 核心领域分层是 `Connections → Storage Destinations → Category Rules`。
* 第一阶段应优先保障 115、OpenList/Alist、CloudDrive2、本地文件、STRM、302 代理和 Emby/Jellyfin 刷新组成的存储/播放闭环。
* PT 聚合、追剧订阅、插件、多用户和更多网盘仍保留在最终范围内，但不必同时进入首个开发切片。
* `server/` 当前只有 `.gitkeep`，尚无需要兼容的 Server 实现；可以先修正方案再落代码。
* Player 已有经过真实 OpenList/Alist live test 的实现，CloudDrive2/WebDAV/夸克/123 也已有不同程度实现；`server` 类型仍只存在于类型占位，尚无 `ServerDataSource`。
* 当前路线图按“框架与全部 CRUD → 多个驱动/下载器/代理 → 流水线与 STRM”横向铺开，首个真正端到端产出出现得太晚。
* 文档中的 `Connection` 定义包含媒体服务器、网盘、下载器和 PT 站点，但数据模型/API 又单独规划 `downloaders` 和 `sites`，需要明确统一连接注册表与领域配置之间的关系。
* 总览中的“自动双向配置同步”与安全设计中的“默认仅结构同步、凭据需显式确认”表述不一致。
* 永久写入 `.strm` 的 URL 与必须过期的 signed URL 存在生命周期问题，需要定义续签/轮换策略。
* 项目 owner 已确认采用 OpenList/Alist 可播放纵向切片方向，并要求 Server 自带完整、好用、可独立使用的网页管理端。
* 管理端需要支持不同账户、角色和权限；不同页面、导航项、按钮及其后端 API 可以由不同权限控制。

## Assumptions (temporary)

* 第一个开发里程碑应是可演示、可测试的纵向闭环，而不是一次性建立全部包和空接口。
* OpenList/Alist 比 115 更适合作为首个真实驱动：已有 Player 侧 live-test 经验、HTTP API 边界清晰，并且能同时验证远端列举、下载直链、302、STRM 与刷新链路。
* 身份认证、凭据加密、日志脱敏、路径约束和 302 签名机制需要在早期确定边界，即使完整多用户权限稍后实现。
* Server 管理网页从第一个小版本开始建设，采用 Vue 3 + TypeScript + Vite + Pinia + Vue Router + UnoCSS，并与 Go API 保持同源部署方向。
* 权限采用稳定 permission code，角色是权限集合，用户可分配多个角色；前端权限显隐只负责体验，后端必须对同一 permission code 强制鉴权。
* 首版优先使用可撤销的服务端会话与 HttpOnly Cookie，不把访问令牌存入 localStorage；为未来 Player/CLI 保留独立 API/device token 机制。

## Open Questions

* 无阻塞问题；按 owner 已确认方向实施。

## Requirements (evolving)

* 回顾现有 Server、路线图、安全和 Player 集成设计，找出过时、冲突或缺失部分。
* 给出第一阶段 MVP 的明确范围、非范围与验收条件。
* 给出按小 PR/小任务拆分的实现顺序。
* 保留后续外部驱动、下载器、PT、追剧和插件扩展点，不削减最终产品范围。
* 以纵向切片反向约束工程骨架，只创建首个闭环实际需要的包、表和 API。
* 把连接统一注册表、领域专用配置、同步安全语义和 STRM 签名生命周期写成明确决策。
* Server 首版提供首次设置向导：仅在数据库不存在任何用户时允许创建第一个 owner 管理员。
* 提供登录、登出、当前用户与当前权限查询；登录失败限速，会话可撤销并有过期时间。
* 提供用户管理：查看、创建、编辑显示名、启用/停用、分配角色、管理员重置密码。
* 提供角色管理：查看、创建、编辑、删除自定义角色，并通过权限矩阵分配稳定权限码。
* 首次初始化用户具有不可隐式转移的 owner 身份；内置 `administrator` 角色不可删除，系统不得允许停用/删除 owner 或让有效管理员数量降为零。
* 管理网页提供登录/首次设置页、应用壳层、仪表盘、用户管理和角色权限页面。
* Vue Router route meta、侧栏导航和按钮/操作统一使用权限码；对应后端 API 使用同一权限语义强制校验。
* 预先定义 Server 全功能模块权限目录，包括用户、角色、连接、存储目标、分类规则、STRM、下载、发现、追剧、设置和审计；首版只开放已经实现的页面与操作。
* 生产部署方向为 Go 服务同源提供管理端静态资源；开发期允许 Vite 与 Go API 分离运行。

## Acceptance Criteria (evolving)

* [x] 新数据库启动后可通过网页创建唯一的首个 owner 账户，已有用户时 setup API 拒绝再次初始化。
* [x] 用户可登录、刷新页面后保持会话、退出后会话立即失效；Cookie/CSRF/登录限速符合安全基线。
* [x] owner 可创建普通用户和自定义角色，为角色勾选权限并为用户分配角色。
* [x] 没有权限的账户看不到对应页面、导航或按钮，直接请求对应 API 时得到 403。
* [x] 无法删除/停用 owner，也无法让有效管理员数量降为零。
* [x] Go 单元/集成测试覆盖 setup、login、session、用户和角色权限边界。
* [x] Vue 管理端通过 typecheck、lint、build，并具有加载、空、错误和无权限状态。
* [x] Server 默认构建与 `webui` 嵌入构建均通过，管理端与真实 API 契约完成联调测试覆盖。
* [x] 架构与路线图更新为“管理端基础 → OpenList/Alist 可播放闭环”的实际实现顺序。

## Definition of Done (team quality bar)

* Tests added/updated (unit/integration where appropriate)
* Lint / typecheck / CI green
* Docs/notes updated if behavior changes
* Rollout/rollback considered if risky

## Out of Scope (explicit)

* 不删除 PT、追剧、AI、插件、多用户或更多网盘等长期规划。
* 首个管理端版本不实现 OpenList/Alist、STRM、302、Emby/Jellyfin、下载器、PT、追剧和插件的业务 API；这些作为随后纵向切片接入现有菜单与权限体系。
* 首版不实现直接用户级 allow/deny 覆盖、资源实例级 ACL、组织/租户、多因素认证、OAuth/LDAP/SSO 或 Player 设备授权。
* 首版不提供危险的任意自定义权限码；权限目录由代码定义并迁移/同步到数据库。

## Technical Notes

* 必读：`docs/architecture/01-overview.md`、`02-server-design.md`、`06-roadmap.md`、`07-security-design.md`、`DEVELOPMENT.md`、`AGENTS.md`。
* REST API 使用 `/api/v1/` 和标准响应信封。
* Go 1.22+、Gin、GORM；handler 保持薄，业务编排放在 service，外部系统通过接口隔离。
* 推荐候选首切片：OpenList/Alist → Storage Destination → STRM → signed 302 → Emby/Jellyfin refresh。
* 不推荐首切片直接从 115 开始：供应商/API 特殊性会让初始领域接口被单一驱动牵引。
* 不推荐以本地文件为唯一首切片：能验证扫描和路径安全，但无法验证 Server 的关键云端 302 价值。

## Research References

* [`research/initial-vertical-slice.md`](research/initial-vertical-slice.md) — 推荐以 OpenList/Alist 打通签名 STRM → 302 → Emby/Jellyfin 刷新的首个真实闭环，本地驱动用于契约与路径安全测试。
* [`research/web-rbac-foundation.md`](research/web-rbac-foundation.md) — 首版采用稳定 permission code、多角色权限并集、owner/最后管理员保护、opaque HttpOnly Cookie Session + CSRF，以及 Vue 构建产物随 Go 二进制同源部署的方向。

## Feasible Approaches

### Approach A: OpenList/Alist 可播放纵向切片（推荐）

* 从管理员初始化、连接与加密凭据开始，贯穿远端扫描、STRM、签名代理、302、媒体服务器刷新和实际播放。
* 优点：最早证明 Server 的独特产品价值；已有 Player live-test 经验；可以反向收敛最小接口。
* 代价：首个切片就必须认真处理认证、密钥、路径、签名和外部服务失败。

### Approach B: 基础设施与全量 CRUD 优先

* 先实现规划中的目录、认证、全部三层 CRUD、用户管理，再逐个接驱动。
* 优点：后台资源表面完整，便于多人按模块分工。
* 代价：很长时间没有可播放闭环，容易产生空接口、伪能力和后续重构。

### Approach C: 本地文件导入/转移优先

* 先验证本地扫描、分类、move/hardlink/copy 与 Emby/Jellyfin 刷新。
* 优点：外部依赖少，适合验证路径安全和任务编排。
* 代价：不能验证云盘临时直链、302 和 STRM 这一 Server 核心差异化能力。

## Expansion Sweep

* Future evolution: 驱动接口采用“最小基础能力 + 可选 capability”，避免 Upload/Delete/Search/Quota 强迫所有 provider 实现；首期 STRM 同步 run 作为未来通用 Job/Run 编排的第一个实例。
* Related scenarios: Server 最终仍需独立管理 Web UI；Player 配置同步默认只同步结构，秘密同步必须显式确认。
* Failure/edge cases: 同步必须幂等、可重跑、单文件失败可形成 partial；STRM 原子写入；签名 URL 需要续签/轮换；代理缓存不能超过上游 URL 过期时间；日志不得记录 token/CDN URL。

## Decision (ADR-lite, pending owner confirmation)

**Context**: 当前 Server 为空目录，旧路线图按横向模块铺开，首个端到端用户价值出现太晚。

**Decision**: 采用 Approach A，以 OpenList/Alist 可播放闭环作为第一个媒体里程碑；在此之前先交付管理网页与 RBAC 基础小版本。管理端不是临时脚手架，后续所有 Server 功能直接接入同一应用壳层、权限目录和 API 安全边界。

**Consequences**: 首个版本不会展示媒体闭环，但会产生可独立运行和真实可操作的账户/权限闭环；第二个小版本开始实现 OpenList/Alist Connection，并沿用此权限体系。长期全功能页面保留，但只有已实现能力可操作。
