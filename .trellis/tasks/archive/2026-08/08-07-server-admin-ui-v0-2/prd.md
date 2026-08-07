# 实现 Server 管理端导航与混合型仪表盘 v0.2

## Goal

把当前用于验证认证与 RBAC 的平铺式 Server 管理端，升级为可承载完整媒体自动化产品的第一版可运行壳层。该版本落实已确认的分组导航、统一顶栏、用户管理内部层级、混合型仪表盘与响应式规则，同时继续使用真实后端数据或明确的未配置/规划状态。

## Requirements

- 仪表盘保持独立一级入口。
- 侧栏按“发现、订阅、媒体自动化、系统”分组；分组只在至少存在一个当前用户可见子项时显示。
- 发现包含推荐与探索；订阅包含订阅管理、工作流与日历；媒体自动化包含任务中心、下载管理、媒体整理、STRM / 入库与文件管理；系统包含连接与存储、站点管理、插件、用户管理与设置。
- 尚未实现的页面必须显示明确的规划状态，不请求或伪造不存在的业务数据。
- 账户与角色权限通过“用户管理”工作区的页签/二级路由统一承载；只有对应读取权限的页签可见。
- 审计日志不再平铺在侧栏，改由右上角日志中心进入；当前审计 API 和权限继续生效。
- 顶栏提供全局搜索、日志、通知和头像菜单。搜索与通知没有后端能力时显示真实的未配置/未实现状态，不生成虚假结果或未读数。
- 头像菜单展示当前用户身份、个人账户入口和安全退出；专用 self-service API 未实现前，资料、密码和个人会话操作必须清楚标注为待实现，不复用管理员 API 假装所有用户都可自助。
- 仪表盘按“状态与告警 > 活动任务 > 流水线与入库 > 订阅与快捷操作 > 发现内容”排序。
- 当前 `/api/v1/dashboard` 的初始化/恢复状态作为真实 Server 基线；媒体、连接、存储、任务、订阅和发现域在真实 API 落地前使用逐卡片规划/未配置状态。
- 复用生成的 permission code；导航、页签、按钮和顶栏入口隐藏只改善 UX，后端 API 授权仍是安全边界。
- 桌面使用固定/可收起侧栏和 12 列卡片网格；平板使用紧凑布局；移动端使用分组抽屉和单列卡片顺序。
- 保留 OhMyCine 深色影院感、青蓝/翡翠主色和克制玻璃质感，并满足键盘焦点、ARIA、触摸目标和减少动画偏好。

## Acceptance Criteria

- [x] 侧栏不再平铺“角色与权限”和“审计日志”。
- [x] 用户管理父入口根据 `users.read` / `roles.read` 显示，并只展示获权页签。
- [x] 旧 `/users`、`/roles`、`/audit` 深链接拥有明确兼容行为。
- [x] 顶栏搜索、日志、通知和头像入口可通过鼠标与键盘打开/关闭，且移动端可用。
- [x] 无 `audit.read` 的用户看不到审计日志内容，直接访问仍由路由/API 拒绝。
- [x] 仪表盘首屏以 Server、媒体/存储/连接、任务和流水线为主，不以用户/角色数量为主。
- [x] 仪表盘没有虚构的媒体数、容量、任务、下载速率、订阅或推荐内容。
- [x] 单个规划/错误状态不会清空其他卡片；发现区域始终位于运维区域之后。
- [x] 桌面、平板和移动布局保持相同信息优先级，移动导航不依赖横向平铺。
- [x] 路由/导航逻辑和仪表盘顺序拥有自动化回归测试。
- [x] `npm run permissions:check`、`npm run test`、`npm run typecheck`、`npm run lint`、`npm run build` 通过。
- [x] Server Go 测试和嵌入式 Web UI 构建验证通过。
- [x] 使用隔离端口实际启动 Server，验证登录后首页、静态资源和 API 可访问，且不破坏现有数据库。

## Definition of Done

- 产品代码、测试和必要文档同步完成。
- RBAC、会话、CSRF、Owner/最后管理员等已有安全不变量无回归。
- 不提交 `node_modules`、`dist`、`.runtime` 或数据库临时文件。
- 通过 Trellis 检查，使用中文 Conventional Commit 提交并归档任务；不推送远端。

## Technical Approach

- 在 `server/webui/src` 内提取单一导航定义与权限过滤函数，供 AppLayout、Router 和测试复用。
- 使用 Vue 3 `<script setup>` 实现 AppLayout 顶栏、侧栏/抽屉和互斥弹出面板；不引入图标运行时依赖，关键图标使用可访问的内联 SVG。
- 使用嵌套路由或兼容重定向形成 `/system/users/accounts`、`/system/users/roles` 用户管理层级，并保留旧地址兼容。
- 将 DashboardView 拆成真实运行摘要与显式 domain-state 卡片；不新增伪成功后端接口。
- 添加轻量测试环境验证纯导航逻辑、权限组合、路由兼容和关键卡片顺序。
- 本任务不改变 permission catalog，也不新增 self-service、通知、搜索或媒体流水线 API。

## Decision (ADR-lite)

**Context**: 当前 UI 能验证认证/RBAC，但菜单扁平、主页偏开发状态，无法作为完整 Server 产品入口；同时多数媒体域尚未实现。

**Decision**: 先交付“真实壳层 + 明确规划状态”的 UI v0.2，完整落实信息架构和响应式行为，只消费已存在的安全 API。后续各媒体域按 OpenList/Alist 纵向切片逐张替换规划卡片。

**Consequences**: 用户可以立即验证最终结构和交互，不会被假数据误导；部分入口在本版只展示规划状态，个人账户、搜索和通知的完整写能力需要后续专用 API。

## Out of Scope

- OpenList/Alist、115、CloudDrive2、本地文件等真实连接 API。
- 存储目标、分类规则、STRM、signed 302、Emby/Jellyfin 刷新业务实现。
- TMDB/豆瓣发现 provider、PT 搜索、下载器和追更业务。
- 新的通知持久化、全局搜索索引、运行日志流和个人账户 self-service API。
- 修改现有 permission catalog 或降低 API/service 授权要求。

## Technical Notes

- 产品设计依据：`docs/architecture/08-server-web-ui-design.md`。
- 开发契约：`.trellis/spec/backend/web-admin-guidelines.md` 的 `Administration Navigation and Mixed Dashboard` 场景。
- 当前真实仪表盘接口：`GET /api/v1/dashboard`，返回初始化、恢复、用户、角色和审计基础摘要；媒体域接口尚不存在。
- 当前实现文件：`server/webui/src/layouts/AppLayout.vue`、`router/index.ts`、`views/DashboardView.vue`、`views/UsersView.vue`、`views/RolesView.vue`、`views/AuditView.vue`。
- 安全边界：浏览器使用 HttpOnly opaque session、session-bound CSRF 和生成 permission code，不在前端保存 token。
