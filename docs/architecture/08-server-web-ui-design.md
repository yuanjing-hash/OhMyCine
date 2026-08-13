# OhMyCine Server Web UI — 管理端信息架构与仪表盘设计

> 本文档是 Server 管理端的产品与交互契约。它定义目标信息架构、权限可见性、仪表盘数据边界和响应式规则，不表示文中所有业务 API 已经实现。

## 1. 设计目标与边界

OhMyCine Server 是以媒体自动化流水线为核心的自托管服务，管理端应优先回答三个日常问题：

1. Server、连接和存储是否正常？
2. `Discover → Download → Transfer → Import → Notify` 流水线正在做什么，哪里需要人工处理？
3. 最近入库、订阅和发现内容有什么变化？

因此管理端采用“内容 + 自动化运维”混合模式，但信息优先级始终是：

```text
状态与告警 > 活动任务 > 流水线与入库 > 订阅与快捷操作 > 发现内容
```

本文档不改变以下边界：

- Player 仍可独立使用，Server 管理端不是 Player 基础播放的依赖。
- 发现页可聚合 TMDB、豆瓣等公开元数据与趋势；基于用户库的 AI 推荐仍主要属于 Player。
- 导航、卡片、按钮和顶栏入口的权限控制只改善体验；Gin middleware 与 service policy 才是安全边界。
- 未实现的数据和操作不得用静态数字、假成功 API 或伪造趋势填充。

## 2. 视觉与参考原则

管理端延续 OhMyCine Cinema OS 语言：深色影院感背景、青蓝/翡翠状态色、克制的透明与模糊层次、高可读性文字和明确的告警色。玻璃效果用于顶栏、侧栏和少量升起卡片，不能牺牲数字、表格和错误信息的对比度。

可以向 MoviePilot 等成熟媒体后台学习的是结构方法：

- 固定/可收起的分组侧栏；
- 搜索与全局工具集中的顶栏；
- 不同跨度卡片构成的高信息密度仪表盘。

不复制其品牌名、紫色视觉语言、图标、文案、栏目命名或完整页面布局。OhMyCine 的导航必须围绕自身的媒体流水线与 `Connections → Storage Destinations → Category Rules` 三层架构组织。

## 3. 全局页面层级

```text
OhMyCine Server
├─ 仪表盘
├─ 发现
│  ├─ 推荐
│  └─ 探索
├─ 订阅
│  ├─ 订阅管理
│  ├─ 工作流
│  └─ 日历
├─ 媒体自动化
│  ├─ 任务中心
│  ├─ 下载管理
│  ├─ 媒体整理
│  ├─ STRM / 入库
│  └─ 文件管理
└─ 系统
   ├─ 连接与存储
   │  ├─ Storage（本地根与只读能力，已实现）
   │  ├─ 连接（规划中）
   │  ├─ Storage Destination（规划中）
   │  └─ 分类规则
   ├─ 站点管理
   ├─ 插件
   ├─ 用户管理
   │  ├─ 账户
   │  ├─ 角色与权限
   │  └─ 会话
   └─ 设置

顶栏全局入口（不占用侧栏一级菜单）
├─ 全局搜索
├─ 日志中心
│  ├─ 运行日志
│  └─ 审计日志
├─ 通知中心
└─ 头像菜单
   ├─ 我的资料
   ├─ 修改密码
   ├─ 登录会话
   ├─ 界面偏好
   └─ 退出登录
```

### 3.1 导航行为

- 仪表盘是独立的首要入口，不放入任何分组。
- 侧栏分组标题只用于建立上下文，不是空白落地页。点击可展开/收起分组，路由深链接仍指向具体子页。
- “角色与权限”不再是侧栏一级项，与账户、管理员视角的会话统一收拢到“用户管理”。
- “审计日志”不再平铺到侧栏，它是右上角“日志中心”的受权限子页。
- 侧栏底部可显示脱敏的 Server 版本与连接状态，不放账户编辑操作。
- 侧栏收起状态和分组展开偏好属于当前用户的非敏感界面偏好，不影响授权。

### 3.2 页面职责与 Server 范围映射

导航名称只负责帮助用户定位，不重新划分后端业务所有权。完整 Server 能力按下表进入相应工作区，避免遗漏能力，也避免照搬其他媒体后台的栏目：

| 工作区 | 承载范围 | 不属于该页的责任 |
|--------|----------|------------------|
| 发现 / 推荐、探索 | PT/资源聚合搜索、一键提交下载或追更、TMDB/豆瓣等公开元数据与趋势 | 不承担 Player 本地库 AI 推荐；下载后的执行状态进入任务中心 |
| 订阅 / 订阅管理、工作流、日历 | 追更生命周期、缺集检测、站点/质量/发布组过滤、调度工作流和日历事件 | 不直接保存 PT 凭据；凭据归站点连接 |
| 任务中心 | 汇总下载、转移、STRM、入库、媒体服务器刷新和追更执行状态，突出失败/阻塞与重试入口 | 只是权限裁剪后的读模型，不成为各类任务的新写入 owner |
| 下载管理 | 下载任务队列、速度、暂停/恢复/删除和下载器健康跳转 | qBittorrent/Transmission 凭据与连接测试归“连接” |
| 媒体整理 | 分类规则应用结果、元数据匹配、命名/转移记录、失败重试和待人工处理项 | 分类规则定义与存储目标配置归“连接与存储” |
| STRM / 入库 | 增量/全量同步、无效 STRM 清理预览、NFO/海报生成结果、signed 302 状态、Emby/Jellyfin 刷新结果 | 不把 302 上游签名 URL 或本地绝对路径暴露给浏览器 |
| 文件管理 | 在已配置连接和根目录内浏览、上传及受控的移动/重命名/删除 | 不绕过配置根、路径规范化、symlink 防逃逸、确认与审计 |
| 连接与存储 | 已实现的本地 Storage 根/能力/只读探测，以及规划中的 115、OpenList/Alist、CloudDrive2、Emby/Jellyfin、下载器等 Connections、Storage Destinations 与 Category Rules | Storage 只声明 Server 可安全访问的提供方根；Connection 拥有接入能力与凭据；归档位置和分类决策仍分别归 Destination 与 Rule |
| 站点管理 | PT 站点、分类映射、测试状态及脱敏配置 | Cookie/Passkey 不进入列表、日志或通知载荷 |
| 插件 | 插件浏览、权限审阅、手动安装/启用/更新/卸载和运行状态 | Hub 是分发站点而非 Server 运行时；插件默认无全局凭据访问 |
| 用户管理 | 管理员视角的账户、角色与权限、全局会话治理 | 当前账户自助操作归头像菜单 |
| 设置 | 调度、通知策略、安全与 signed proxy 模式、Player ↔ Server 结构同步、运行参数和界面默认值 | 敏感凭据同步必须显式确认，不能混入默认结构同步 |

## 4. 全局壳层线框

### 4.1 桌面

```text
┌──────────────────────────┬───────────────────────────────────────────────────────────┐
│ OhMyCine Server          │ 面包屑 / 页面标题        [ 搜索媒体、任务、设置… ]  [日志] [通知] [头像] │
├──────────────────────────┼───────────────────────────────────────────────────────────┤
│ 仪表盘                  │                                                            │
│                          │  路由内容区：12 列网格 / 表格 / 设置表单                │
│ 发现                     │                                                            │
│   推荐                   │  仅此区域滚动；顶栏和侧栏在大屏保持稳定              │
│   探索                   │                                                            │
│                          │                                                            │
│ 订阅                     │                                                            │
│   订阅管理               │                                                            │
│   工作流                 │                                                            │
│   日历                   │                                                            │
│                          │                                                            │
│ 媒体自动化               │                                                            │
│   任务中心 …              │                                                            │
│                          │                                                            │
│ 系统                     │                                                            │
│   连接与存储 …            │                                                            │
└──────────────────────────┴───────────────────────────────────────────────────────────┘
```

### 4.2 顶栏入口

| 入口 | 桌面行为 | 安全与空状态 |
|------|----------|----------------|
| 全局搜索 | 展开宽面板，按媒体、任务、订阅、连接和设置分组 | 只搜索用户有权读取的域；无搜索 provider 时显示可搜索范围与配置引导，不伪造结果 |
| 日志中心 | 打开可筛选的日志抽屉/独立页，包含运行与审计子页 | 运行日志使用 `logs.read`，导出和策略修改分别使用 `logs.export` / `logs.configure`；审计日志使用 `audit.read` |
| 通知中心 | 显示未读数和按严重性排序的事件，可跳转到相应任务 | `/ws/events` 需认证并按用户/权限过滤；无通知时不显示虚假红点 |
| 头像菜单 | 打开当前账户的资料、密码、会话、界面偏好和退出入口 | 仅操作当前账户；密码修改后按安全策略撤销相关会话，不与管理员“用户管理”混合 |

### 4.3 日志、通知与账户边界

- 日志中心是历史查询面：运行日志页支持时间、级别、模块、组件、插件、关键字及业务关联 ID 的 URL 可恢复组合筛选，并显示部分读取/降级状态；审计日志用于不可由普通 UI 操作改写的安全/配置变更记录。两者分别受 `logs.*` 与 `audit.read` 约束且独立保留。
- 通知中心是面向当前用户的可操作投影，只保存任务失败、连接异常、追更新集等必要摘要和目标资源 ID，不代替运行日志或审计日志。标记已读/清除通知不得删除、修改对应任务或日志。
- 如实现持久化未读数，先在 catalog 增加稳定的 `notifications.read_own`、`notifications.ack_own`；管理员查看全局通知需另设 `notifications.read_all`，不能仅凭 `dashboard.read` 或 WebSocket 已认证获得。目标页跳转仍需目标资源的 read permission。
- WebSocket 只负责实时增量；通知历史、未读状态和确认操作归未来 notification service/API。该服务落地前入口显示“尚未配置/尚未实现”，不在前端生成未读数。
- 头像菜单是认证用户的 self-service 面，不继承“用户管理”的管理员权限。任何自助 API 都必须从 session 主体确定目标用户，不能接受任意 user ID 来越权操作他人资料或会话。

## 5. 仪表盘信息架构

### 5.1 12 列桌面线框

下图的数字是占用列数。卡片高度由内容级别约束，不为了补齐版面填充假数据。

```text
12-column content grid

┌───────────────────────────────────────────────────────────┐
│ 问候 / Server 总状态 / 最后更新时间                       12 │
└───────────────────────────────────────────────────────────┘

┌───────────────────┐ ┌───────────────────┐ ┌──────────────────┐
│ 媒体概览       4 │ │ 存储空间       4 │ │ 连接健康      4 │
│ 真实库统计/引导  │ │ 容量、用量、异常  │ │ 正常/异常/待配置 │
└───────────────────┘ └───────────────────┘ └──────────────────┘

┌──────────────────────────────────┐ ┌───────────────────────┐
│ 活动任务                                  7 │ │ 流水线状态            5 │
│ 失败/阻塞 > 运行中 > 排队；显示所有者范围             │ │ Discover → … → Notify │
└──────────────────────────────────┘ └───────────────────────┘

┌──────────────────────────────────┐ ┌───────────────────────┐
│ 近期入库 / 整理                          7 │ │ 后台任务队列          5 │
│ 真实媒体、目标和结果；可跳转到运行记录                 │ │ 调度、重试、下次运行     │
└──────────────────────────────────┘ └───────────────────────┘

┌──────────────────┐ ┌───────────────────────┐ ┌──────────────┐
│ 下载速率/队列  4 │ │ 订阅日历              5 │ │ 快捷操作  3 │
│ 真实采样+时间窗口 │ │ 将播、待搜、失败事件     │ │ 按权限生成 │
└──────────────────┘ └───────────────────────┘ └──────────────┘

┌───────────────────────────────────────────────────────────┐
│ 发现 Hero / 趋势内容（后置，只使用真实 provider 结果）          12 │
└───────────────────────────────────────────────────────────┘
```

### 5.2 卡片数据、permission 与 API owner

仪表盘最终可以由一个读模型聚合接口减少请求数，但聚合层只做查询、裁剪和状态组合，不能成为事实来源或合成不存在的成功状态。每个字段的真实性、授权和失败语义仍归原业务域所有。下表的接口与“未来” permission 都是实现建议，不宣称已存在；落地时必须先更新 permission catalog 和 OpenAPI。

| 卡片 | 显示内容 | 事实来源 owner | 最小读取 permission | 建议读边界 | 空状态/失败行为 |
|------|----------|----------------|---------------------|--------------|------------------|
| Server 总状态 | 版本、运行时长、数据库/调度器状态、最后更新 | runtime、database health、scheduler service | 现有 `dashboard.read` | 认证后的 `/api/v1/dashboard/summary`；匿名 `/api/v1/health` 仍只给基础状态 | 聚合失败显示时间戳、重试与可用的局部域 |
| 媒体概览 | 已管理电影/剧集/集数和近期新增 | media catalog 与 import history service（均为规划域） | 未来 `media.read`；仅 `dashboard.read` 不可绕过 | `/api/v1/dashboard/media-summary` 或聚合字段 | 未建立媒体目录时引导连接媒体服务或完成入库，不显示伪造的 `0 部` |
| 存储空间 | 按存储目标聚合容量、用量、未知与异常 | destination service + cloud/local drivers | 现有 `destinations.read` | `/api/v1/dashboard/storage-summary` | provider 不支持容量时显示“未提供”，不当作 0 B |
| 连接健康 | 115、OpenList/Alist、CloudDrive2、本地、下载器、Emby/Jellyfin 脱敏健康摘要 | connection service 与各 provider adapter | 现有 `connections.read` | `/api/v1/connections?summary=health` 或聚合字段 | 未配置时显示“添加连接”；错误不回显 URL 中的用户信息/令牌 |
| 活动任务 | 失败、阻塞、运行中、排队的下载/转移/STRM/刷新/追更任务 | download、transfer、STRM run、media refresh、follow service；task read model 只聚合 | 对应域的 own/all read；现有包括 `downloads.read_own/read_all`、`follows.read_own/read_all`、`strm.runs.read`，其余上线前新增 | `/api/v1/tasks?state=active` | 逐域按 own/all scope 过滤；无任务显示真实完成状态，不生成演示任务 |
| 流水线状态 | Discover/Download/Transfer/Import/Notify 各阶段运行、积压与最近失败 | 各阶段业务 service；dashboard projection 只计算摘要 | 对应可读域 permission 的并集，不能用 `dashboard.read` 扩权 | `/api/v1/dashboard/pipeline` | 未启用阶段显示“未配置”而非“正常”；无权阶段省略且不泄露计数 |
| 近期入库/整理 | 媒体名、来源、目标、结果和时间 | transfer/import history service | 上线前新增稳定的 transfer/import read scope | `/api/v1/imports?sort=-finished_at&limit=...` | 不展示本地绝对路径、签名 URL 或 provider 凭据 |
| 后台任务队列 | scheduler job、下次运行、重试次数和最近结果 | scheduler service | 未来 `scheduler.read` | `/api/v1/scheduler/jobs?summary=true` | 调度器未启用时给出设置入口，不伪造“全部成功” |
| 下载速率/队列 | 按受控时间窗口采样的上/下行速率、排队和暂停数 | downloader service/adapter | 现有 `downloads.read_own` 或 `downloads.read_all`；速率只能按可见任务范围汇总 | `/api/v1/downloads/summary` | 无下载器时引导创建 connection；无可靠采样时不绘制假曲线 |
| 订阅日历 | 将播、待搜、下载中、失败事件 | follow service + scheduler | 现有 `follows.read_own` 或 `follows.read_all` | `/api/v1/follows/calendar` | 按 own/all scope 过滤；无订阅时显示创建引导 |
| 快捷操作 | 添加连接、新建订阅、提交下载、运行 STRM、刷新媒体服务 | 对应 connection、follow、download、STRM、media-server service | 分别为 `connections.create`、`follows.create`、`downloads.create`、`strm.runs.create`、`media_servers.refresh` | 无独立“仪表盘写 API”；调用各域既有/未来写 API | permission、按钮和 API/service policy 一一对应；二次确认与审计要求不变 |
| 发现 Hero | 真实趋势/推荐内容、provider 来源与更新时间 | discovery service + metadata provider adapters | 现有 `discovery.read` | `/api/v1/discovery/recommendations` | 无 provider、无权限或请求失败时整块省略或显示配置引导，绝不使用占位海报充当真实内容 |

### 5.3 排序、刷新与局部失败

- 首屏优先请求总状态、连接/存储健康和活动任务；发现 Hero 延后加载。
- 实时变化通过受权限约束的 WebSocket 事件增量更新，不受支持的域才使用有界轮询。
- 卡片显示“更新于…”；当上游不可用时保留已标注过期的最后可知值，或显示错误，不将旧值宣称为实时值。
- 聚合接口应允许部分成功；单个 provider 失败不应清空整张仪表盘。每个域需返回可区分的 `ready / unconfigured / empty / stale / error / forbidden` 状态。
- 卡片内的任务排序为失败/阻塞、运行中、排队、已完成；告警严重性高于纯媒体统计数据。

## 6. 用户管理与个人账户

### 6.1 管理员用户管理

“系统 → 用户管理”是一个带内部页签或二级路由的工作区：

| 子页 | 内容 | 最小可见权限 | 写操作边界 |
|------|------|----------------|--------------|
| 账户 | 账户列表、状态、owner 标识、角色摘要；最近登录仅在同时具备 `sessions.read_all` 时显示 | 现有 `users.read` | 继续使用 `users.create/update/disable/delete` 和 `roles.assign`；owner/最后管理员/防自锁不变量仍由服务端事务保证 |
| 角色与权限 | 系统/自定义角色、成员数、canonical permission matrix | 现有 `roles.read` | 继续使用 `roles.create/update/delete/assign`；非 owner 不能授予自己没有的权限 |
| 会话 | 用户、设备/浏览器摘要、创建/最后活动/到期、撤销状态 | 实现前新增 `sessions.read_all` | 撤销他人会话实现前新增 `sessions.revoke_all`；不返回 session token/hash，操作记审计 |

父菜单“用户管理”在用户可看到上述任一子页时出现；打开时跳转到第一个可见子页。例如只有 `roles.read` 的用户可进入角色页，不因缺少 `users.read` 而被整个工作区拒绝。

### 6.2 头像中的自助设置

当前用户的个人操作永远通过头像菜单进入，不要求管理权限：

- “我的资料”只修改服务端允许的当前账户字段，不可修改 owner/角色。
- “修改密码”需要当前密码，并按安全策略撤销其他或全部会话。
- “登录会话”只显示自己的会话，允许撤销其他会话；当前会话的撤销等价于退出。
- “界面偏好”保存主题、密度、侧栏和动画减少等非敏感选项。
- “退出登录”撤销当前服务端会话，而不是仅清除前端状态。

## 7. 权限可见性契约

### 7.1 基本规则

1. 路由 meta、导航过滤、页签和按钮必须复用 canonical permission code 生成的 TypeScript 常量，不在组件中发明字符串。
2. 分组的可见性是子页可见性的派生值；没有可见子页时整组消失，不显示空分组。
3. 有多个读范围时使用服务端返回的数据范围。例如 `downloads.read_own` 只能产生自己的任务摘要，`downloads.read_all` 才能给出全局摘要。
4. 没有某张卡片所需权限时，仪表盘聚合接口应省略或标记 `forbidden` 域，前端重排网格；不显示一张泄露数量的锁定卡。
5. 403 后前端刷新 `/api/v1/auth/me` 以获取当前权限，但不自动重放有副作用的请求。
6. 写操作不会因为在仪表盘或快捷操作中出现而降低确认、CSRF、Origin、审计或 service policy 要求。

### 7.2 导航与读权限建议

下表用于后续路由设计。“未来”标记表示当前 catalog 尚无对应 code，不得在前端先硬编码使用。

| 页面 | 读取权限（任一/按 scope） |
|------|---------------------------|
| 仪表盘 | 现有 `dashboard.read`；内部卡片再按域权限剪裁 |
| 发现 / 推荐、探索 | 现有 `discovery.read` |
| 订阅 / 订阅管理、工作流、日历 | 现有 `follows.read_own` 或 `follows.read_all` |
| 任务中心 | 可见任务域的读权限任一：`downloads.read_own/read_all`、`follows.read_own/read_all`、`strm.runs.read`；转移/入库任务上线前补充其稳定 code |
| 下载管理 | 现有 `downloads.read_own` 或 `downloads.read_all` |
| 媒体整理 | 现有 `categories.read` 或未来 transfer/import read scope；只呈现获权子面板，组合操作再同时验证相关域权限 |
| STRM / 入库 | 现有 `strm.runs.read`；刷新结果上线前新增稳定的 media-server read code，刷新动作仍独立要求 `media_servers.refresh` |
| 文件管理 | 未来 `files.read`；移动/重命名/删除必须拆分高风险 write code |
| 连接与存储 | 已实现 `storages.read`，以及现有规划权限 `connections.read`、`destinations.read`、`categories.read` 按子面板分别判定 |
| 站点管理 | 未来 `sites.read`；Cookie/Passkey 不进入列表响应 |
| 插件 | 现有 `plugins.read` |
| 用户管理 | 现有 `users.read` 或 `roles.read`，以及未来 `sessions.read_all`；子页分别判定 |
| 设置 | 现有 `settings.read` |

页面内按钮必须与实际写 API 使用同一个生成 permission 常量。例如“添加连接”按钮和 `POST /api/v1/connections` 同为 `connections.create`；“运行 STRM”按钮和创建 run 的 API 同为 `strm.runs.create`；“刷新媒体服务”按钮和刷新 API 同为 `media_servers.refresh`。未来新增的转移重试、文件操作、通知确认和会话治理也必须先进入 catalog，再同时接入 route/button 与 Gin middleware/service policy。

### 7.3 典型按钮与写权限

| UI 动作 | permission code | API/service 额外约束 |
|---------|-----------------|----------------------|
| 添加/编辑/删除/测试连接 | 现有 `connections.create/update/delete/test` | 凭据写入继续加密；列表、错误和审计只返回脱敏值 |
| 添加/编辑/删除/测试 Storage | 已实现 `storages.create/update/delete/test` | 根路径由 service 规范化并拒绝 Reparse Point；测试只读；审计 metadata 不含绝对路径；删除只删配置 |
| 创建/编辑/删除存储目标 | 现有 `destinations.create/update/delete` | 路径和关联连接由 service 校验；删除前检查引用 |
| 创建/编辑/删除分类规则 | 现有 `categories.create/update/delete` | 目标、模板与策略由 service 校验 |
| 提交下载 | 现有 `downloads.create` | 创建者 ID 从 session 主体写入，不接受前端伪造 owner |
| 暂停/恢复/删除下载 | 全局操作用现有 `downloads.manage_all`；普通用户自管上线前新增 `downloads.manage_own` | service 同时校验任务 owner；不能用 `downloads.read_own` 代替写权限 |
| 创建追更 | 现有 `follows.create` | 创建者 ID 从 session 主体写入 |
| 编辑/暂停/恢复/删除/立即执行追更 | 上线前新增 `follows.manage_own` / `follows.manage_all` | service 同时执行 own/all scope 校验 |
| 启动/取消/清理 STRM | 现有 `strm.runs.create`、`strm.runs.cancel`、`strm.cleanup` | cleanup 限定 STRM 根且先支持 dry-run；高风险动作确认并审计 |
| 刷新 Emby/Jellyfin | 现有 `media_servers.refresh` | 只允许已配置媒体服务，错误不得泄露 API key |
| 转移重试、文件移动/重命名/删除 | 实现前按 read、write、delete 与 own/all 风险拆分稳定 code | 根目录约束、symlink 防逃逸、确认和审计仍由 service 执行 |
| 安装/更新插件 | 现有 `plugins.install`；启用、停用、卸载如需不同风险边界则先新增 code | 展示权限、禁止默认自动更新，不授予全局凭据 |
| 撤销他人会话 | 未来 `sessions.revoke_all` | 不返回 token/hash；owner 与当前管理员安全不变量保持有效 |

隐藏或禁用按钮只改善体验。Gin middleware 和 service policy 必须使用同一 code，并在资源级再次校验 owner、配置根、状态转换和事务不变量；前端不得用 read permission 推导 write permission。

## 8. 空状态与真实数据契约

所有卡片和业务页都必须区分以下状态：

| 状态 | 界面行为 | 禁止行为 |
|------|----------|----------|
| loading | 使用与真实结构匹配的骨架，保留布局稳定 | 先显示 0 再跳成真实值 |
| unconfigured | 说明缺哪类连接/目标/provider，给出有权限的配置入口 | 把“未配置”表示为“健康、0 异常” |
| empty | 明确数据已成功读取但暂无记录，提供合理的下一步 | 填入演示媒体、演示任务或随机曲线 |
| ready | 显示来源、数据范围和更新时间 | 隐藏数据已过期的事实 |
| stale | 可保留最后成功值，明确标记时间与刷新失败 | 继续标记为“实时” |
| error | 显示脱敏错误摘要、受影响范围、重试和诊断入口 | 清空其他已成功卡片，或回显凭据/签名 URL |
| forbidden | 省略受限数据并重排布局；必要时只给不含数量的权限说明 | 泄露“有几条看不到的记录” |

数字 `0` 只能在后端已成功查询、语义确实为零时展示。卡片不能仅根据 HTTP 200 判定数据可用；聚合响应需保留域级状态和更新时间。

## 9. 响应式与移动端规则

### 9.1 尺寸档位

| 档位 | 建议触发条件 | 导航 | 卡片网格 |
|------|--------------|------|------------|
| 宽屏桌面 | `>= 1280px` | 固定展开侧栏，用户可手动收起 | 12 列，使用 3/4/5/7/12 等跨度 |
| 窄屏/平板 | `768–1279px` | 默认紧凑侧栏或叠加抽屉，保留分组层级 | 6 列；概览卡通常 3 列，主任务卡 6 列 |
| 移动 | `<= 767px` 或实际窗口宽度属于 compact | 顶栏汉堡按钮打开分组抽屉，不把桌面侧栏压缩成一排图标 | 单列，按信息优先级线性排序 |

断点以实际 viewport 和容器宽度为准，不通过设备名或 User-Agent 推断。指针能力和布局宽度是两个独立维度：触摸屏不得依赖 hover 暴露操作。

### 9.2 移动布局线框

```text
┌───────────────────────────────┐
│ [菜单]  仪表盘       [搜索] [通知] [头像] │
├───────────────────────────────┤
│ Server 总状态 / 最后更新                    │
│ 连接健康                                      │
│ 存储空间                                      │
│ 活动任务                                      │
│ 流水线状态                                    │
│ 媒体概览                                      │
│ 近期入库 / 整理                               │
│ 后台任务队列                                  │
│ 下载速率 / 队列                               │
│ 订阅日历                                      │
│ 快捷操作                                      │
│ 发现 Hero（仍在运维与任务信息之后）             │
└───────────────────────────────┘
```

- 全局搜索在移动端打开全屏 surface，搜索筛选和结果都不依赖 hover。
- 日志入口在空间不足时收入“更多”工具面板，但不放回侧栏一级导航；通知和头像必须可直接触达。
- 侧栏抽屉关闭后恢复内容焦点；开启时需焦点限定、`Escape` 关闭和清晰的当前页标识。
- 表格在窄屏改用卡片行、分段详情或水平滚动容器；不缩小到无法阅读。破坏性操作仍需明确确认。
- 安全区、至少 44px 触摸目标和 `prefers-reduced-motion` 必须被支持。
- 桌面卡片的左右位置在单列下转换为上述固定语义顺序，不依赖 CSS 视觉排序造成键盘/读屏顺序偏离。

## 10. 后续实现约束

1. 实现导航前先扩充 canonical permission catalog，再生成 TypeScript 常量；禁止在 UI 中临时发明 permission code。
2. 为聚合卡片设计 API 时，先映射 `source → service/read model → API → card`，保留 own/all scope、域级错误和更新时间。
3. 每个新写操作继续遵守 session-bound CSRF、Origin/Referer、Fetch Metadata、精确 JSON media type、事务不变量与审计。
4. 日志、通知、仪表盘和 WebSocket 事件不得包含 Authorization、Cookie、API key、passkey、session token/hash、下载器密码、签名/CDN URL 或本地绝对路径。
5. 视觉实现需通过键盘、读屏、对比度、缩放、窄屏和触摸验证；状态不能只用颜色表达。
6. 业务 API 尚未实现的页面可展示明确的“规划中/未配置”状态，但不得返回伪成功、伪造统计或假运行任务。

## 11. 相关文档

### 任务中心已实现边界

`/automation/tasks` 现已接入持久化队列的真实 REST read model。页面提供状态、类型、优先级与 provider 筛选，显示 lane 位置、进度/处理量、速度/ETA、重试和安全错误摘要；未知遥测保持“未知”。仅在 `queued + 单一 job_type + 单一 priority` 视图中启用鼠标拖放和键盘上移/下移，保存冲突后刷新服务端真实顺序。详情抽屉读取持久化状态事件与 attempt 时间线，并支持版本化 ActionRequest 响应、暂停、恢复、取消和失败重试。

任务中心只是统一观察与控制面，不是跨类型串行执行器。MediaLibrary watcher、文件树 reconciliation、定时扫描和 STRM projection 继续由 LibrarySupervisor 独立并行运行，既不创建 Job 也不消耗队列槽。

- [Server 后端设计](02-server-design.md)
- [安全设计](07-security-design.md)
- [开发路线图](06-roadmap.md)
