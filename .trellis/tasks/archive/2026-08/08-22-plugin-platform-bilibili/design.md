# 插件平台与 Bilibili 在线媒体源技术设计

## 1. 设计目标

建立一套由 Server 托管、Player 按标准能力原生呈现、Hub 分发的通用插件协议。在线内容站点是首个落地场景，Bilibili 是首个真实参考插件，用来验证登录、站点首页、推荐刷新、专区、个人内容、搜索、详情、播放、多清晰度、弹幕和真实下载，同时补齐 OhMyCine 现有作品/分集/媒体版本模型；它不是平台唯一允许的插件类型。

PT 站点继续属于 Server 内建发现与下载能力，不进入本插件运行时。

### 1.1 通用扩展范围

插件不使用互斥的 `type` 决定用途，而是组合声明稳定 capability。长期允许扩展：

| 领域 | 典型 capability | 宿主集成面 |
|---|---|---|
| 在线内容 | `site.feed`、`site.search`、`media.playback` | Player 声明式页面、搜索、播放 |
| 元数据 | `metadata.provider`、`metadata.enrich` | 识别与刮削流水线 |
| 通知 | `notification.send` | 通知中心与自动化任务 |
| 下载器 | `download.plan`、`download.observe` | 下载任务队列；插件不直接决定最终路径 |
| 云盘/存储 | `storage.list`、`storage.transfer` | 连接与存储目标；高风险文件动作单独授权 |
| 媒体服务器 | `media_server.catalog`、`media_server.refresh` | 播放器管理与入库通知 |
| 自动化 | `event.subscribe`、`scheduled.task` | 受控事件总线与宿主调度器 |
| 整理规则 | `recognition.transform`、`category.match`、`naming.render` | 识别、分类与命名扩展点 |
| UI 贡献 | `ui.page_schema`、`home.contribution` | JSON Schema/DTO 驱动的宿主组件，不注入代码 |
| 高权限工具 | `external.tool` | 未来隔离进程运行时，显式高风险授权 |

同一插件可以组合多项能力，例如 Bilibili 同时组合站点浏览、媒体播放、主页贡献、下载计划和账号动作。PT 发现仍是核心内建边界，不能通过伪造 capability 绕过。

## 2. 当前实现证据与缺口

### 已有基础

- Player `DataSource` 已提供媒体列表、首页栏目、搜索、详情和播放请求边界，`ServerDataSource` 可继续作为插件在线库的唯一 Player 入口。
- Player 已能聚合各来源 Hero、最新和其它栏目，并对单来源失败进行隔离。
- Player 已有 `MediaDetail.mediaSources`、电影详情“版本”列表、跨来源 `playbackTargets`，以及播放器中的播放队列/选集弹层。
- Server Player DTO 已有 `PlayerMediaDetail.Versions`，并能为本地文件流与 115 302 返回不同交付类型。
- Server 识别成功后使用 `movie:tmdb:<id>` / `series:tmdb:<id>` 聚合作品；不匹配条目保持文件级身份，避免标题误合并。
- 入库链路已有 `releaseversion.Parse`，能够保留剪辑版、分辨率、UHD/BluRay/WEB-DL、REMUX、HDR 和 Dolby Vision 等版本标签。
- Server 已有加密凭据、持久任务队列、审计和 `plugin_id` 日志筛选基础。

### 需要补齐

- 当前电影多版本已经能返回，但 DTO 主要只有标题、大小、时间和来源，缺少结构化版本规格。
- 当前剧集版本是平铺的媒体条目；同一集多个文件可能显示成重复集数，而不是“一集下面多个版本”。
- Player 的详情版本列表与播放器队列菜单尚未形成统一的“选集 → 版本 → 清晰度”交互。
- 当前 `MediaStreamRequest` 只有单一 URL/Headers，无法完整表达 DASH 音视频分离、多个清晰度、刷新期限和安全代理策略。
- 当前 Hub 目标文档仍包含 Go 动态插件、插件注册 Gin 路由和任意 Vue UI 注入，需要迁移。
- Server 没有可复用的 FFmpeg/媒体合流工具管理，需要为 Bilibili 下载增加宿主媒体工具边界。

## 3. 核心领域模型

### 3.1 插件定义与运行实例

```text
PluginPackage
  ├─ Manifest / signature / checksum
  ├─ immutable package version
  └─ runtime entry

PluginInstallation
  ├─ selected version
  ├─ granted permissions
  ├─ enabled / failed / rollback state
  └─ active runtime generation

PluginConnection
  ├─ plugin_id
  ├─ display name
  ├─ encrypted credential references
  ├─ non-secret config
  └─ connection health

OnlineLibrary
  ├─ connection_id
  ├─ provider library key
  ├─ navigation/feed configuration
  └─ home contribution policy
```

插件包、安装状态、用户账号连接和在线媒体库必须分表/分对象，避免升级插件时覆盖账号或库配置。

### 3.2 内容身份层级

```text
MediaWork                    七武士 / 某部番剧 / 某个 B 站视频
  └─ MediaSegment            电影本体，或 S01E01，或 B 站分 P
       └─ MediaVersion       剧场版/导演剪辑版、文件版本、来源版本
            └─ StreamVariant 480p/1080p/4K、不同码率/编码的临时流
```

约束：

- `MediaWork` 只有在 TMDB、提供方稳定 ID 或其它可靠身份相等时聚合。
- `MediaSegment` 对电影为单段；剧集使用季/集身份；Bilibili 使用 AV/BV + CID/ep_id 等稳定分段身份。
- `MediaVersion` 表示可独立存在的媒体资产或提供方播放线路，拥有精确身份。
- `StreamVariant` 是同一版本的临时清晰度，不参加作品去重，也不写入长期媒体目录身份。
- 播放进度绑定 Work+Segment；上次版本选择绑定 Segment；字幕/音轨偏好绑定精确 Version。

### 3.3 版本规格

`MediaVersionDescriptor` 使用可选结构化字段：

- edition/cut：剧场版、导演剪辑版、加长版、IMAX 等；
- resolution、frame_rate、bitrate；
- source_medium：UHD BluRay、BluRay、WEB-DL、WEBRip 等；
- remux/encode、video_codec、audio_codec；
- dynamic_range：SDR、HDR、HDR10+、Dolby Vision；
- audio_languages、release_group、size；
- provider/source label、delivery kind；
- exact identity、availability 和安全不可用原因。

旧数据通过文件名保守解析补充已知字段，无法证明的字段留空。

## 4. 插件 Manifest 与能力协议

Manifest 至少包含：

```text
id, name, version, api_version, min/max_server_version
entry, runtime, capabilities, permissions
config_schema, author, license, homepage, source
package_sha256, signature, changelog
```

首期站点能力：

- `site.navigation`
- `site.feed`
- `site.search`
- `site.detail`
- `site.user_library`
- `site.interaction`
- `media.playback`
- `media.quality_switch`
- `media.subtitle`
- `media.danmaku`
- `media.download_plan`
- `playback.progress_sync`
- `site.history`
- `home.contribution`
- `feed.refresh`

PT 相关 capability 不开放给第三方插件。

## 5. 插件运行时与权限

### 5.1 默认运行时

- 第一安全运行时采用 WASM 沙箱；宿主协议与运行时适配器分离，避免未来被某个 WASM 引擎锁死。
- 插件通过版本化 JSON 请求/响应 ABI 调用能力；载荷大小、调用时长、内存和并发均受限。
- WASM 不直接获得主机文件系统、环境变量、Socket、数据库或系统时间能力。
- 受信任外部进程运行时仅作为后续高级能力，不属于首版。
- Go `plugin` 不作为正式运行时。

### 5.2 宿主能力

- 受控 HTTP：仅 Manifest 授权域名，限制 scheme、DNS/私网目标、重定向、超时、响应体和并发。
- 凭据句柄：插件请求宿主附加指定凭据，不能读取全局凭据或把明文返回 Player。
- 插件私有 KV/Blob：按 plugin installation + connection 隔离并限制容量。
- 结构化日志：宿主强制绑定 plugin_id、connection_id 和 operation，并统一脱敏。
- 事件：只允许订阅 Manifest 声明且用户批准的宿主事件。
- 下载计划：插件只能描述远端媒体资产和合流要求，不能选择任意本地路径。

### 5.3 风险分层与运行边界

插件能力按风险分三层演进，而不是一次性把宿主权限暴露给 WASM：

1. **低权限 WASM 能力**：纯计算、DTO 转换、私有 KV、受限日志和小型配置，默认无 WASI、Socket、文件系统、环境变量和系统时钟。
2. **受控宿主能力**：HTTP、凭据附加、事件订阅、调度、下载计划、元数据查询等由 Server 校验权限和参数后代执行；插件获得句柄或结果，不获得全局对象。
3. **高权限外部进程**：未来确需原生 SDK、媒体工具或复杂驱动时使用独立进程、最小 OS 权限和独立凭据授权；不得退化为进程内 Go plugin 或任意命令执行。

所有层级共同禁止：任意 Vue/JavaScript 注入、注册裸 Gin 路由、读取全局数据库/所有凭据、任意文件删除和绕过审计。管理端与 Player 页面只接受版本化 JSON Schema 和声明式 DTO。

### 5.4 首个参考插件策略

- 不再开发用户可见的固定内容演示站点；现有最小 WASM fixture 仅用于 ABI、安装、启停、升级和故障回归。
- Bilibili 直接作为第一个真实插件推动 Host API、在线媒体库和播放契约，站点请求使用脱敏固定响应 fixture 进行确定性测试，在线接口只作为手工验收。
- Bilibili 的 API 路径、签名、登录态、分页和响应解析全部位于插件包；Server/Player 核心只认识标准 operation、capability 和 DTO。
- 后续第二个插件应能复用相同 SDK 和 Host API 接入其它领域，以此验证平台没有被 Bilibili 特化。

## 6. Server API 与数据流

### 6.1 管理 API

```text
/api/v1/plugins                 安装状态、可用版本、权限、健康
/api/v1/plugins/{id}/install    校验并原子安装
/api/v1/plugins/{id}/enable     启用
/api/v1/plugins/{id}/disable    停用
/api/v1/plugins/{id}/upgrade    权限差异确认后升级
/api/v1/plugins/{id}/rollback   回滚上一可用版本
/api/v1/plugins/{id}            卸载
/api/v1/plugin-connections      连接与凭据配置
/api/v1/online-libraries        选择插件发布的在线媒体库
```

所有写操作要求管理权限、CSRF/Origin 防护和审计。

### 6.2 Player API

Player 继续只调用 Bearer 保护的 `/api/v1/player/*`：

```text
/sources                         Server 发布的物理库、外部库、插件在线库
/online-libraries/{id}/navigation
/online-libraries/{id}/feeds/{feed}
/online-libraries/{id}/search
/online-libraries/{id}/items/{item}
/online-libraries/{id}/items/{item}/playback
/online-libraries/{id}/actions/{action}
/home-contributions
```

插件私有 ID 必须封装为有界 opaque token，普通 DTO 不返回 Cookie、签名参数或站点私有请求头。

### 6.3 页面与 Feed DTO

- Navigation 只包含宿主已知页面类型、图标键、标题和 opaque route key。
- Feed 包含 Hero、横向卡片、海报墙、紧凑视频列表等声明式布局，不包含 HTML。
- Feed 使用不透明 cursor 和 refresh session；旧 cursor 只能在所属会话中继续使用。
- 插件失败以来源级错误返回，Server 不把插件栈、路径或敏感响应暴露给 Player。

## 7. Player 设计

### 7.1 DataSource 演进

- 保持一个用户配置的 `ServerDataSource`，Server 下的在线库作为子来源/子库，不要求用户在 Player 重复登录插件账号。
- 扩展 DataSource 契约以支持动态导航、Feed、刷新、站点动作、结构化版本和播放方案。
- 所有外部 DTO在 `ServerDataSource` 边界进行运行时校验；旧 Server 字段缺失时安全降级。

### 7.2 原生站点页

- Player 使用现有 Cinema OS 组件渲染插件声明的 Hero、栏目、网格、详情和操作。
- Bilibili 页面包含推荐、热门、排行、影视/番剧/纪录片、收藏、稍后再看、历史、关注和追更。
- 插件不得注入 DOM、脚本、样式或访问 Player 状态。

### 7.3 首页聚合

- Server 返回候选 HomeContribution；Player 保存每设备启用状态、顺序和位置。
- 顶部 Hero 可以混合受信来源；下方栏目按来源分组并显示来源标识。
- Bilibili 默认仅贡献个性化主页推荐。
- 全局刷新并行刷新各支持来源；单栏目刷新只更新对应 refresh session。
- 超时、失败和取消均按来源隔离。

### 7.4 选集/版本菜单与独立清晰度按钮

- 复用现有 PlayerControls 的队列/选集 popover 视觉和交互基础，作为内容身份选择器。
- 剧集：选集菜单选择集数；某集存在多版本时，在该集下面继续选择媒体版本。
- 电影：同一按钮直接显示媒体版本，不伪装成剧集队列。
- Bilibili 多分 P：同一按钮选择分 P；某分 P 存在多个提供方版本时继续选择版本。
- PlayerControls 新增独立清晰度按钮，仅展示当前 Version 的 StreamVariant；它不能改变集数、分 P 或媒体版本。
- 当前版本没有两个以上实际可用清晰度时，独立清晰度按钮自动隐藏。
- 切换版本/清晰度先准备新播放方案；成功后才替换当前流，失败保留当前流。
- 切换成功恢复当前时间、暂停状态、音量和弹幕状态；字幕/音轨按新版本重新匹配。

## 8. 播放方案

`PlaybackPlan` 包含：

- work/segment/version identity；
- 当前 variant 和可选 variants；
- progressive、HLS 或 DASH 类型；
- 视频 URL、可选独立音频 URL、临时请求头引用；
- 字幕和弹幕轨道描述；
- expires_at、refresh token/opaque selection token；
- direct/server_gateway/loopback_bridge 交付策略。

Player 不持久化 URL、Header 或 refresh token。跨 origin 请求继续删除 Server Bearer、Cookie 和提供方私有 Header。

### 8.1 弹幕来源优先级

`PlaybackPlan.danmaku` 是与当前 Work/Segment/Version 精确绑定的短时轨道。Server 将站点弹幕转换为 Player 已知的标准 JSON 轨道，或通过受保护的在线资产路由输出；Player 只看到 Server 同源地址并使用设备 Bearer。加载顺序为：

1. 当前播放方案的插件弹幕轨道；
2. 当前来源未提供或明确不可用时，才调用 Player 已配置的通用弹幕匹配服务；
3. 用户手动搜索始终保留，用于覆盖自动选择。

插件轨道失败可以显式让用户重试/回退，但不能静默把错误弹幕绑定到同名作品。

### 8.2 观看历史与进度回传

Player 本地 SQLite 历史是设备侧播放恢复的可靠底座，新增基于 `(updated_at, identity_key)` 游标或等价稳定排序的分页命令；“继续观看”仍是该历史的过滤视图，不再冒充完整历史。

在线插件可另外实现：

- `site.history`：读取提供方历史，保留 opaque cursor；
- `playback.progress_sync`：接收标准 started/progress/paused/resumed/stopped/completed 事件；
- `playback.history.remove`：未来按提供方能力开放显式删除，不默认联动本地删除。

Player 每次先保存本地历史，再通过现有 DataSource `syncPlaybackProgress` 边界把事件交给 Server。Server 校验设备权限、在线库归属、事件频率和幂等键后调用插件；远端失败不回滚本地历史，也不打断播放。历史聚合只依赖可靠 provider identity，不用标题/年份猜测去重。

## 9. Bilibili 插件

### 9.1 登录与浏览

- Bilibili 实现位于独立官方插件包/插件库中，消费公开 SDK、Manifest 和 Host API；Server/Player 核心不得导入 Bilibili 站点客户端或包含站点 API 特判。
- 首期采用扫码登录，Server 保存加密会话并展示登录状态、账号摘要和重新认证入口。
- 插件实现推荐、热门、排行、专区、搜索、详情、分 P/剧集、收藏、稍后再看、历史、关注和追更。
- Bilibili 实现游标化历史读取、观看进度回传和 CID 精确绑定的弹幕轨道；弹幕上游地址与 Cookie 始终停留在 Server。
- 所有请求仅访问声明的 Bilibili 域名，遵守限速、退避和会话失效处理。

### 9.2 播放

- 插件按账号权限返回实际可用清晰度，不伪造大会员或地区受限档位。
- DASH 音视频分离通过 PlaybackPlan 表达；Player/libmpv 播放桥负责安全加载。
- 字幕和弹幕通过标准轨道协议进入 Player。

### 9.3 真实下载

```text
用户选择媒体/分P/清晰度和目标媒体库
  → 插件解析 DownloadPlan
  → Server 快照目标库、分类、命名、转移与冲突策略
  → 下载视频/音频/字幕/弹幕到任务专属暂存区
  → 校验大小、类型和完整性
  → 宿主媒体工具执行 DASH 合流
  → 输出清单进入现有识别、整理、入库和历史流程
```

`DownloadPlan` 只允许 HTTPS 资产、受控 Header 引用、预期类型/大小、合流拓扑和输出建议名，不允许绝对路径或任意命令。

### 9.4 媒体工具

- Server 增加 `MediaTool` 抽象，首个实现使用固定版本 FFmpeg。
- Windows 开发脚本优先发现兼容 FFmpeg；未安装时可把经过版本与 SHA-256 固定校验的工具安装到 Server 隔离、gitignored 的工具目录，不写入系统 PATH。
- 发布包/部署可显式配置工具路径；日志只记录工具版本、阶段和安全退出码，不记录带凭据的命令行。
- FFmpeg 只接收 Server 创建的受管暂存路径和固定参数模板，不执行插件提供的任意参数。

## 10. 下载与多版本入库

- 下载任务选择目标媒体库的行为与现有手动磁力/URL 下载一致。
- 任务快照目标媒体库、分类 Profile、命名模板、转移方式和冲突策略。
- 合流输出进入现有 Transfer pipeline，不由插件直接复制到媒体库。
- 输出名保留站点来源、清晰度和解析出的版本标签；合法多版本使用不同最终文件名。
- 冲突判断基于目标版本身份与最终路径，不能把“同作品”直接当作覆盖条件。
- NFO 描述作品；版本特有的流媒体规格进入对应 fileinfo/streamdetails；STRM 与精确版本一一对应。

## 11. Hub 与 SDK

- Hub 保持静态 Registry + GitHub Release 分发。
- Server 插件页是唯一安装入口，并提供“已安装 / 插件市场 / 仓库设置”分页；仓库设置接受 GitHub 仓库主页 URL，不要求用户手填 raw 索引地址。
- 仓库根目录发布 `ohmycine-plugin-registry.v1.json`。Server 先经 GitHub API 解析默认分支与提交 SHA，再读取该提交上的 Registry；Registry 条目引用 GitHub Release 资产、SHA-256、Manifest URL、图标和版本摘要。
- 借鉴 MoviePilot `package.v2.json + plugins.v2 + GitHub Release` 的发现/发布结构，但不复用其 Python 进程内插件加载：OhMyCine 的 Registry 仅用于发现，安装时仍单独校验 Manifest、WASM、摘要、签名、兼容性和权限。
- 多仓库按用户顺序合并；相同插件 ID 默认展示优先仓库，来源和冲突必须可见，不能静默跨仓库替换已安装插件。
- SDK 提供 Manifest 类型、DTO、测试夹具、Host mock、打包和签名/摘要工具。
- 官方 Bilibili 插件作为完整示例，但站点请求实现与 SDK 分离。
- Registry CI 校验 Manifest Schema、版本兼容、权限域名、包摘要、签名、许可证和危险文件。
- 插件详情展示权限、能力、兼容版本、来源、校验状态和升级权限差异。

## 12. 数据迁移与兼容

- 现有物理媒体库和 `ServerDataSource` 配置不迁移为插件。
- 新版本 DTO 字段均为可选；新 Player 能读取旧 Server，旧 Player 继续使用原有媒体目录接口。
- Server 为现有条目保守回填版本字段，无法确定的不填；不因迁移重命名或移动用户文件。
- 现有剧集平铺条目通过作品+季+集身份在响应层分组，数据库迁移不删除条目。
- 插件升级采用旁路解包、校验、启动探测和原子切换；失败保留旧版本。

## 13. 风险与回滚

- Bilibili 非公开接口变化：所有站点逻辑留在插件包，协议保持稳定；错误按插件隔离。
- WASM ABI 设计不足：首版冻结最小能力并以 `api_version` 演进，不直接暴露内部 Go 类型。
- DASH/FFmpeg 失败：保留任务暂存状态与安全重试点，不把半成品入库。
- 多版本误合并：只使用可靠身份，无法证明时宁可分开。
- 首页推荐风控：使用缓存、会话 cursor、限速和手动刷新，不后台高频轮询。
- 回滚插件不回滚用户账号和在线库配置；不兼容配置需保留并标记待迁移。
- 官方插件发布故障：核心只保留通用协议和 fixture；Bilibili 插件可独立停用、回滚或从插件库移除，不影响 PT、本地、115、Emby/Jellyfin 等核心能力。
