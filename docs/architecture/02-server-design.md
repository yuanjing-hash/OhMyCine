# OhMyCine Server — 后端设计文档

## 1. 概述

OhMyCine Server 是一个**以媒体流水线为核心**的自托管后端，负责：
- **发现** — 聚合PT站点搜索，自动匹配元数据
- **下载** — 管理qBittorrent/Transmission下载任务
- **转移** — 下载完成后自动转移到分类目录（本地/网盘）
- **入库** — 自动生成STRM文件，通知Emby/Jellyfin刷新媒体库
- **追更** — 定时追踪剧集更新，自动下载缺少的集数
- **代理** — 302直连播放，零带宽消耗

**核心设计理念**：设置好媒体分类 → 发现页选择视频下载/追更 → 程序自动下载 → 自动转移到指定目录 → 自动通知媒体服务器刷新 → Player客户端展示新媒体。

### 1.1 当前实现状态（2026-08）

下载预分类完成后 Server 不只设置 qBittorrent Category，还必须显式调用 `setLocation(暂存目录/分类)` 后才恢复下载；因为用户关闭 Automatic Torrent Management 时，单独修改 Category 不会改变保存位置。入库源解析仅对旧任务兼容查找暂存根目录，新任务的正常路线始终是分类目录。`copy|symlink` 入库后进入独立做种管理，按任务快照的时长/分享率条件采样；`copy` 达标后删任务与暂存源数据，`symlink` 只删任务并永久保留链接源，`move` 入库后以 `deleteData=false` 清理 qBittorrent 任务。自动清理默认关闭。

下载完成后自动生成的 TransferTask 现已拥有独立 `/automation/organization` 媒体整理工作区。`GET /api/v1/transfers` 提供 own/all 范围内的稳定分页、`active|history|all` 范围、状态/媒体库/分类/方式/标题筛选、完整可见范围的筛选选项和真实统计；管理端默认“进行中”，终态记录进入“历史记录”，详情复用 transfer Job 的 attempts、timeline、ActionRequest 与阶段重试。失败、已取消和已完成的整理记录可通过 `DELETE /api/v1/transfers/{id}` 清理，操作复用 `jobs.control_own/all` 并二次确认；它只删除 TransferTask 和对应 transfer Job 执行历史，不删除 DownloadTask、下载器任务、暂存/源文件、媒体库文件或做种记录。Transfer worker 仅保存最多 100 项、48 KiB 内的目标相对命名结果摘要，读写两端均拒绝绝对路径、遍历与控制字符；私有 manifest、暂存/Storage 根、provider task ID 和原始错误不进入 API。这里不创建手动整理任务，手动选择文件与操作归后续“文件管理”。

Server 已完成管理基础与 Web UI v0.2 壳层：Go/Gin + SQLite/GORM、显式版本迁移、首次 owner 设置、opaque HttpOnly Cookie 会话、CSRF/Origin 防护、登录限速、用户/角色/permission catalog、多角色权限并集、审计基础，以及 Vue 3 管理端的分组导航、统一顶栏、用户管理二级路由、日志中心入口、响应式抽屉和混合型仪表盘。生产方向使用 `webui` build tag 将 Vite `dist` 嵌入 Go 二进制；默认 `go test` / `go run` 不要求 `dist` 存在。

当前版本已实现 local 与 115 数据源基础：管理员从统一“数据源”页面先选择本地目录或 115 网盘；本地根继续执行绝对路径、Reparse Point 和只读探测校验，115 Cookie 由 Connection AES-GCM 加密保存并可被多个 provider root 复用。115 云目录选择使用绑定 actor、Connection、Storage、Storage 根、provider directory ID、用途和过期时间的 opaque token，Storage 保存稳定 file ID 与显示路径，不保存 Cookie、pickcode 或临时直链。MediaLibrary 可继续从 Storage 根选择任意下级媒体目录，私有保存稳定 provider root ID，并从该 ID 执行 bulk-tree 全量扫描；创建数据源本身不会扫描媒体，删除配置不会修改真实文件或网盘内容。Storage Destination、STRM、302 和媒体服务器刷新业务 API 仍按路线图继续实现。

Emby 不作为文件数据源展示，而进入独立“播放器管理”工作区。页面复用通用 Connection 记录和既有权限，以卡片展示真实探测状态、受控的服务器版本/媒体数量聚合摘要，以及默认关闭的签名 STRM 302 gateway。API Key 加密保存且永不回填；gateway 与 Web/API/STRM 共用 Server 主端口，复制地址只使用全局 `OMC_PUBLIC_ORIGIN`。默认监听 `0.0.0.0:3000` 与默认 advertised origin `http://127.0.0.1:3000` 明确分离，wildcard 地址不能进入持久 URL。为兼容不返回 CORS Header 的 115 CDN，网关修补 Emby Web 固定播放器资源中的远程 DirectPlay `crossOrigin` 赋值，并在固定 HTML 壳优先加载一个网关同源、不可配置的兜底脚本以覆盖旧模块缓存；同一固定脚本还可按网关开关提供设备适配的外部播放器入口和 Emby 背景图横向图库。外部播放器只接受本系统 PlaybackInfo 返回的短时 ticket，不传递 Emby/115 持久凭据或最终 CDN 地址；图库只使用当前 Emby 会话可见图片且无第三方前端依赖。其它 HTML/静态内容仍透明代理，不提供任意脚本注入。

Player 现已通过独立 `ServerDataSource` 接入 Server 媒体目录。用户首次在 Player 输入 Server 用户名和密码，Server 只在该次验证后签发可撤销的 `omc_player_` device token；数据库仅保存 token 与设备 ID 的不可逆摘要，Player 密码不持久化，device token 进入 Player provider-specific 安全凭据库。`/api/v1/player/*` 使用独立 Bearer 中间件并注册在浏览器 Origin/CSRF 边界之外，Bearer 不能进入 Cookie 管理 API；停用用户、重置密码、同设备重新登录、登出或显式撤销都会使对应令牌失效。Player 专用 DTO 只返回安全媒体库、作品、版本和身份投影，不返回绝对路径、115 provider ID、Cookie、Emby API Key、signed STRM URL 或上游临时地址。

Player 通过同一个受 Bearer 保护的 entry stream endpoint 播放 Server 媒体，但 Server 按 Storage 类型安全分流：本地条目从注册 Storage 根和媒体库相对根逐段校验，拒绝越界、symlink、junction/Reparse Point 与目录后直接提供 GET/HEAD/Range 文件流，绝对路径不进入 DTO、错误或日志；已生成 STRM 的 115 条目不读取 `.strm` 文本，也不经过 Emby，而是再次校验 active managed STRM artifact，复用 `SignedProxyService.ResolveArtifactForClient` 解析当前设备的短期 115 地址并返回 302。Player Windows/Android 原生播放桥只把 Bearer 发给 Server origin，跨 origin 跳转前删除 Authorization、Cookie 和其它私有 Header，同时保留 Range。Player 媒体 DTO 还从持久化 TMDB 快照投影原始标题、评分、时长、类型、导演、编剧、演员、IMDb/TMDB ID 与有界背景图身份；旧快照没有图片数组时回退现有单张背景。Server 与 Player 直连 Emby 的聚合去重使用 TMDB 作品身份及 `MediaArtifact.OpaqueID` 精确版本身份；Emby 实例仅以规范化 `SystemId` 的不可逆指纹判断，同名、同地址或不同认证方式均不作为相等依据。配置同步和多设备设置/进度同步不属于这一接入切片，连接 Server 不会自动导入或上传 Player 数据源配置。

Server 同时已实现独立的 `MediaClassificationProfile`：它保存版本化的 movie/tv 逻辑分类规则，提供受控管理页、严格校验、复制、乐观 revision 和纯 Go matcher，供 `MediaLibrary` 选择。内置 `default-v1` 与 Player v1 默认分类语义一致，但 Server 不读取或执行 Player 配置。Profile 自身不选择 Storage 或执行文件写入；MediaLibrary 将 Profile 与最终存储边界、排序、转移/冲突策略及命名模板组合起来。下载任务选择媒体库后快照整套路由，先用该 Profile 给 qBittorrent 预分类，完成后再由独立 Transfer Job 入库。

Profile 的预识别现已内置用户指定的 MoviePilot-Help `TV.txt` 与 `anime.txt` 固定离线快照，默认按 TV → anime → 用户自定义规则执行。快照固定到明确 commit，随源码保存来源、SHA-256、同步日期和 MIT 许可证；运行时不访问 GitHub。322 条有效规则完整支持屏蔽、替换、集数偏移、捕获替换、前后查找和直接 TMDB ID 提示，并对兼容回溯正则设置输入、单次匹配、总耗时和应用次数上限。直接 ID 仍必须由 Server 向 TMDB 按类型/ID 验证后才能分类。每个 Profile 可以关闭任一只读内置词包，复制保留选择；下载任务会把词包、用户规则、分类规则和命名模板一起快照，后续修改不会改变在途任务。

`MediaLibrary` 基础现已落地为只读索引边界：每个库引用一个 Storage、一个经目录选择令牌校验并持久化为 provider-relative 的根、一个分类 Profile，以及独立的全量/增量计划、扩展名、忽略规则、metadata 匹配与 provider 限速配置。新建并启用后自动执行首次全量基线，成功后才挂接该库独立的 watcher，并立即执行一次 catch-up reconciliation 覆盖交接窗口；初始化失败时保留配置、显示安全错误码与下次指数退避时间，也可仅唤醒该库“立即重试”。115 Connection 另有独立生活事件轮询：首次只锚定最新事件，后续将白名单化的创建、移动、重命名和删除事件连同 `(update_time,event_id)` 游标原子持久化，再同时唤醒关联媒体库和同 Connection 的 115 离线下载任务；监听不占额外 Job 队列，单连接失败不影响其它连接，媒体库周期 reconciliation 与离线任务低频状态查询分别负责补漏。文件 Entry 继续作为扫描事实层，并持久化 `work_key/series_title`；用户可见 catalog 在 SQLite 中先按作品聚合再分页，电视剧按 Series -> Season -> Episode 按需展开，原始 `/entries` 保留为分页诊断接口。管理端 `/system/media-libraries` 提供真实 total、标题搜索、类型筛选、20/50/100 页大小和取消过期请求的作品清单。local 与 115 已接入只读扫描；受控 STRM 输出目录仍由后续纵向切片接入。

媒体库扫描和下载完成现在共用同一 provider-neutral 识别核心：local、115 以及未来 OpenList/Alist、CloudDrive2 adapter 只枚举文件事实；统一分组层负责根目录电影、剧集季目录和 BDMV/VIDEO_TS 发行目录；随后才执行 Profile 预处理、文件名/父目录/包名候选、TMDB 验证和分类。TMDB 请求不在数据库事务中进行，提交前重新校验来源、Profile revision 和 generation。匹配结果默认缓存 30 天，无匹配缓存 30 分钟，临时网络失败缓存 5 分钟，凭据缺失/认证失败不做长期负缓存。扫描事件仍按媒体库独立并发且不占任务队列；当前 local watcher 与 115 生活事件会合并唤醒同一完整只读 reconciliation，未变化识别单元通过指纹/缓存避免重复 TMDB，请求；周期扫描继续补漏。

v25 新增媒体库识别单元和安全缓存持久化，Entry 关联识别投影，扫描记录显示匹配、未识别、缓存命中和识别失败数。缺少 TMDB、认证/网络失败、无匹配或低置信不会让文件枚举失败，而是进入“未识别”。管理端媒体清单提供全部、已识别、未识别和人工匹配分页；管理员可单项重试、搜索有限 TMDB 候选、只提交 TMDB ID/type 进行服务端复验，并可清除人工覆盖。更换 Storage/媒体库根/provider root 时，旧 Entry、识别单元、人工覆盖和扫描记录在同一事务内清空，再自动建立新基线；扫描和人工识别都不会移动、重命名、上传或删除来源文件。

Server 运行日志已经形成独立基础设施：zerolog 事件在统一脱敏后同时写入 stdout 与本地 JSONL，默认按 20 MiB 切割、gzip 压缩，并以 10 个分片、30 天和 500 MiB 三项上限清理最旧历史。管理端顶栏日志中心通过 `logs.read` 查询并组合筛选模块、组件、插件和业务关联 ID；导出和策略修改分别由 `logs.export`、`logs.configure` 控制。运行日志与 SQLite 审计日志分域、分权、分开保留，日志文件故障时 Server 降级为 stdout 而不退出。

下载器纵向切片现已接入真实持久队列：管理员可创建、编辑、测试和删除 qBittorrent 连接；qBittorrent 下载目录不再属于某个下载器或 Storage，而是在 `/system/settings` 通过全局 Server 目录选择器配置一份统一绝对暂存目录，支持 Server 可见 Windows 盘符/UNC 与 Linux 挂载点。115 原生离线下载器则受 provider 约束：复用一个 115 数据源的加密 Cookie，并通过 Storage-scoped 目录令牌选择该数据源根内任意子目录；数据库私有保存稳定 provider directory ID，管理 API 只显示数据源名和 Storage-relative 路径。115 离线任务以生活事件广播作为低延迟完成检查信号，事件到达后立即重新读取任务状态和输出清单；20 秒低频查询继续作为漏事件补偿，等待期间只刷新本地 Job lease，不把生活事件直接当作完成事实。管理端通过顶部页签切换进行中、历史记录、新建下载、做种管理和下载器管理，不再把所有区域纵向平铺；`GET /api/v1/downloads?scope=active|history|all` 按 download→transfer→seeding 完整流水线判定范围，失败或未收口的后续仍留在进行中。完整成功历史可只清理 OhMyCine 下载/整理/做种记录及 Job 执行历史，不调用 qBittorrent、不删除暂存或媒体库文件；失败/取消任务仍使用 provider-first 的破坏性删除。保存和执行均重新校验路径、symlink 与 Reparse Point，下载任务入队时快照绝对路径和媒体分类 Profile revision；旧 Storage-relative 任务保持兼容。下载完成会同时持久化 provider 完整清单与安全入库清单，只有识别、转移和目标对账全部成功后才精确清理二者差集；任何 partial、非子集、路径/文件变化都会保留数据。qBittorrent `copy|symlink` 将该清理延后到做种收口，115 只按已验证稳定 item ID 送回收站，不递归猜测目录内容。TMDB 凭据按“用户 AES-GCM 自定义凭据 → 部署凭据 → 正式构建内置应用凭据”解析，并显式区分 v4 Read Access Token/Bearer 与 v3 API Key/`api_key` query；管理 API 只显示来源和类型，清除自定义凭据自动回到下一级。v11 前的 Token 密文不重写并继续按 Read Access Token 使用。默认 API 优先短域名且只在网络错误时回退旧域名，401/403 或其它 HTTP 响应不回退；自定义 API 与图片 HTTPS 前缀分别通过固定真实请求测试成功后才独立保存。裸磁力先获取 metadata、暂停并复用 `ParseFilename + TMDB + classification.Classify` 做轻量刮削，再把结果安全映射为暂存根内的 qBittorrent category 后恢复下载；缺少凭据、认证/网络失败、无结果或低置信时自动归入 `未识别`，不阻塞后续任务，完成后再次复核。取消是经确认的破坏性操作：qBittorrent 删除任务与下载数据后，Server 事务清理 DownloadTask、Job 及依赖；failed/cancelled 也可单独删除，provider 已手动删除时幂等收口，provider 失败时保留本地记录。qBittorrent 新旧 API 与 OMC tag 幂等接管和 115 原生离线下载均已覆盖；115 离线完成后可按目标 MediaLibrary 快照，在同一 Connection 内执行云端 `move|copy`、模板改名、四种冲突策略和 dirty-generation 对账。Transmission、跨账号/跨网盘传输、STRM 与媒体服务器通知仍由后续切片实现。

115 MediaLibrary 现在还可独立启用“分享与转存接管”：绑定同 Connection 的 115 原生下载器，并在 Storage 内选择一个与最终媒体库根及其它中转根互不重叠的中转目录。下载页可提交 `115_share`，Server 将分享链接和提取码按 DownloadTask AES-GCM 加密，转存到稳定的 `omc-<task-id>` 子目录；成功响应丢失或重启后先按该目录事实对账，避免重复转存。用户通过 115 App 手工放入中转根的非 `omc-*` 直接子项，由生活事件安静窗口唤醒的权威目录 sweep 接管；启动及周期 reconciliation 同时补漏，数据库部分唯一索引保证重复事件只创建一条内部 adopted DownloadTask。分享与手工转存不另建识别/整理实现，而是继续经过统一包过滤、Profile/TMDB 识别门禁和 TransferService；不识别时保留来源且不写最终目录。分享链接、提取码、provider item ID、Cookie、完整 provider 路径和上游正文均不进入 Job payload、API、WebSocket、日志或审计。

元数据产物仍遵循统一“识别/TMDB 快照是数据库唯一真相，按媒体库策略投影”的后续设计：本地媒体库在媒体文件旁生成 NFO/JPG；云盘启用 STRM 时在本地 STRM 投影目录生成 STRM/NFO/JPG；云盘未启用 STRM 时默认只保存数据库元数据，并允许媒体库显式开启旁挂文件上传。这个开关必须和真实 NFO/JPG/STRM worker 同时交付，当前分享接管切片不提供无执行效果的占位设置。

管理端通过 Server 目录选择器注册本地 Storage，而不是使用浏览器原生文件选择器或自由文本路径。`storages.browse` 单独保护 Server 进程可见根与单层子目录枚举；Windows 显示当前服务账户可见盘符/映射盘，Linux/NAS/Docker 只显示进程命名空间中的 `/` 与实际挂载点。导航和选择使用短期、签名、用途隔离的令牌，UI 不拼接路径；保存时仍重新执行 `CanonicalizeRoot`、Reparse Point/symlink 拒绝、唯一性和只读探测。该浏览器不递归扫描、不返回普通文件，也不提供创建、改名、移动、删除、上传或预览。

这里的 `Storage` 表示“Server 能安全访问的提供方根和能力快照”，而 `StorageDestination` 表示“媒体最终放在哪里、使用什么放置/STRM 策略”。二者不能合并：一个 Storage 可在后续被多个 Destination 引用，Storage 本身不做媒体分类、扫描或写入决策。

Server 管理端的目标导航、顶栏、12 列仪表盘、用户管理内部层级、权限可见性和响应式规则见 [Server Web UI 设计](08-server-web-ui-design.md)。本文仍负责后端业务、API 与数据模型，不在此复制界面设计全文。

## 2. 技术栈

| 组件 | 技术 | 说明 |
|------|------|------|
| 语言 | Go 1.22+ | 并发模型好，交叉编译简单 |
| Web框架 | Gin | 高性能HTTP |
| ORM | GORM | 数据库操作 |
| 数据库 | SQLite (默认)；PostgreSQL (未来可选部署) | 轻量默认，可扩展 |
| 任务调度 | robfig/cron | 定时任务（追更、STRM同步） |
| 配置管理 | Viper | YAML配置 |
| 日志 | zerolog | 结构化日志 |
| CLI框架 | Cobra | 命令行参数 (仅CLI组件) |
| 容器化 | Docker + docker-compose | 部署 |

## 3. 项目结构

```
ohmycine-server/
├── cmd/
│   └── server/              # 主服务入口
│       └── main.go
│
├── internal/
│   ├── config/              # 配置管理
│   ├── database/            # 数据库连接与迁移
│   ├── models/              # 数据模型 (GORM)
│   ├── handlers/            # HTTP Handlers (API层)
│   │   ├── connection.go    # 连接管理 API
│   │   ├── destination.go   # 存储目标 API
│   │   ├── category.go      # 分类规则 API
│   │   ├── site.go          # 站点管理 API
│   │   ├── download.go      # 下载管理 API
│   │   ├── discovery.go     # 发现页 API
│   │   ├── transfer.go      # 转移任务 API
│   │   ├── strm.go          # STRM管理 API
│   │   ├── file.go          # 文件管理 API
│   │   ├── user.go          # 用户管理 API
│   │   ├── media.go         # 媒体库 API
│   │   ├── sync.go          # Player↔Server同步 API
│   │   └── settings.go      # 系统设置 API
│   ├── services/            # 业务逻辑层
│   │   ├── connection.go    # 连接管理服务
│   │   ├── destination.go   # 存储目标服务
│   │   ├── category.go      # 分类规则服务
│   │   ├── site.go          # 站点管理服务
│   │   ├── download.go      # 下载服务
│   │   ├── discovery.go     # 发现页服务
│   │   ├── transfer.go      # 转移引擎
│   │   ├── strm.go          # STRM管理服务
│   │   ├── scraper.go       # 元数据刮削
│   │   ├── follow.go        # 追更服务
│   │   ├── user.go          # 用户服务
│   │   └── notify.go        # 通知服务 (Emby/Player)
│   ├── middleware/           # HTTP中间件
│   │   ├── auth.go          # Cookie Session 认证、CSRF 与权限中间件
│   │   ├── cors.go          # CORS
│   │   └── logger.go        # 请求日志
│   └── scheduler/           # 定时任务调度
│       ├── follow.go        # 追更定时任务
│       └── strm.go          # STRM定时同步
│
├── pkg/
│   ├── cloud/               # 网盘驱动抽象层
│   │   ├── driver.go        # 驱动接口定义
│   │   ├── registry.go      # 驱动注册
│   │   ├── aliyun/          # 阿里云盘
│   │   ├── pan115/          # 115网盘
│   │   ├── quark/           # 夸克网盘
│   │   ├── baidu/           # 百度网盘
│   │   ├── tianyi/          # 天翼云盘
│   │   ├── uc/              # UC网盘
│   │   ├── xunlei/          # 迅雷网盘
│   │   └── webdav/          # WebDAV通用
│   ├── mediaserver/         # 媒体服务器API客户端
│   │   ├── client.go        # 通用接口
│   │   ├── emby.go          # Emby REST API
│   │   └── jellyfin.go      # Jellyfin REST API
│   ├── downloader/          # 下载客户端抽象层
│   │   ├── client.go        # 下载器接口
│   │   ├── qbittorrent/     # qBittorrent API
│   │   └── transmission/    # Transmission API
│   ├── scraper/             # PT站点刮削器
│   │   ├── site.go          # 站点接口定义
│   │   ├── mteam/           # M-Team
│   │   ├── hdsky/           # HDSky
│   │   ├── ourbits/         # OurBits
│   │   └── ...
│   ├── metadata/            # 元数据刮削
│   │   ├── tmdb.go          # TMDB API
│   │   └── parser.go        # 文件名解析
│   ├── proxy/               # 302代理引擎
│   │   ├── engine.go        # 代理核心
│   │   └── cache.go         # URL缓存
│   └── strm/                # STRM文件生成
│       ├── generator.go     # 生成器
│       └── cleaner.go       # 无效STRM清理
│
├── api/
│   └── openapi.yaml         # OpenAPI 3.0 规范
├── configs/
│   └── config.example.yaml
├── docker/
│   ├── Dockerfile
│   └── docker-compose.yaml
├── scripts/
│   └── build.sh
├── go.mod
└── go.sum
```

## 4. 核心架构 — 三层设计

```
┌─────────────────────────────────────────────────────────────┐
│  ③ 分类规则 (Category Rules)                                 │
│  定义: 什么类型 → 去哪个存储目标                              │
│  电影 → 电影库(网盘)  |  剧集 → 剧集库(本地)  |  ...         │
│  含: 命名规则、目录模板、转移策略(硬链接/移动/复制)           │
├─────────────────────────────────────────────────────────────┤
│  ② 存储目标 (Storage Destinations)                           │
│  定义: 文件最终存放的位置                                     │
│  电影库 → OpenList/Alist:/media/movies (网盘, 开启STRM)      │
│  剧集库 → /nas/disk1/tv        (本地)                        │
│  纪录片 → 115:/docs            (网盘, 开启STRM)              │
│  含: 网盘目标可开启STRM → 配置STRM本地路径/策略              │
├─────────────────────────────────────────────────────────────┤
│  ① 连接管理 (Connections)                                    │
│  定义: 纯粹的"我能连上这个服务"                               │
│ Emby: URL+APIKey | OpenList/Alist: URL+认证 | 115: Cookie | ...│
│  含: 连接测试、状态监控、配额查询                             │
└─────────────────────────────────────────────────────────────┘
```

**数据流闭环**：

```
发现页(聚合搜索) ──→ 选择下载/追更
       │
       ▼
下载器(qBit/Transmission) 下载到本地下载目录
       │
       ▼ 下载完成触发
分类规则 判断媒体类型 → 找到对应存储目标
       │
       ├──→ 目标是本地 → 硬链接/移动/复制到本地目录
       │
       └──→ 目标是网盘 → 上传到网盘目录
              │
              └──→ 开启了STRM？ → 生成STRM到指定本地目录
       │
       ▼
通知 Emby/Jellyfin 刷新媒体库 (REST API)
       │
       ▼
通知 Player 客户端刷新 (WebSocket)
       │
       ▼
Player 展示新媒体
```

## 5. 连接管理 (Connections)

### 5.1 Emby/Jellyfin 连接

通过 Emby/Jellyfin 原生 REST API 管理：

```go
// pkg/mediaserver/client.go

package mediaserver

type MediaServerClient interface {
    // 测试连接
    TestConnection(ctx context.Context) error
    // 获取系统信息
    GetSystemInfo(ctx context.Context) (*SystemInfo, error)
    // 触发媒体库扫描
    RefreshLibrary(ctx context.Context, libraryID string) error
    // 获取媒体库列表
    GetLibraries(ctx context.Context) ([]*Library, error)
    // 获取媒体项目
    GetItems(ctx context.Context, libraryID string, query ItemQuery) ([]*Item, error)
    // 搜索媒体
    Search(ctx context.Context, keyword string) ([]*Item, error)
}
```

```go
// pkg/mediaserver/emby.go

type EmbyClient struct {
    baseURL string
    apiKey  string
    client  *http.Client
}

func (c *EmbyClient) TestConnection(ctx context.Context) error {
    resp, err := c.get(ctx, "/System/Info")
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return fmt.Errorf("emby connection failed: %d", resp.StatusCode)
    }
    return nil
}

func (c *EmbyClient) RefreshLibrary(ctx context.Context, libraryID string) error {
    _, err := c.post(ctx, fmt.Sprintf("/Items/%s/Refresh", libraryID), nil)
    return err
}
```

**连接配置**：

```yaml
# 连接管理中的 Emby 配置
name: "家庭Emby"
type: emby                    # emby / jellyfin
url: "http://nas:8096"
api_key: "xxxxxxxxxxxxxxx"
# 自动刷新: 转移完成后自动调用 RefreshLibrary
auto_refresh: true
```

### 5.2 网盘/OpenList/Alist/CloudDrive2 连接

```go
// pkg/cloud/driver.go

package cloud

type Driver interface {
    Name() string
    Init(config map[string]string) error
    List(ctx context.Context, path string) ([]*File, error)
    Get(ctx context.Context, path string) (*File, error)
    Upload(ctx context.Context, localPath string, remotePath string) error
    GetDownloadURL(ctx context.Context, path string) (*DownloadURL, error)
    Search(ctx context.Context, keyword string) ([]*File, error)
    Delete(ctx context.Context, path string) error
    IsAlive(ctx context.Context) bool
    GetQuota(ctx context.Context) (*Quota, error)
}

type File struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    Path      string    `json:"path"`
    Size      int64     `json:"size"`
    IsDir     bool      `json:"is_dir"`
    MimeType  string    `json:"mime_type"`
    Hash      string    `json:"hash"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type DownloadURL struct {
    URL       string            `json:"url"`
    Headers   map[string]string `json:"headers"`
    ExpiresAt time.Time         `json:"expires_at"`
}
```

**OpenList/Alist 连接配置**：

```yaml
name: "NAS OpenList/Alist"
type: alist
url: "http://nas:5244"
username: "admin"
password: "xxx"
```

**115网盘连接配置**：

```yaml
name: "115网盘"
type: "115"
# 115使用Cookie认证
cookie: "xxxx"
# 115需要代理API（社区维护的115 API服务）
api_proxy: ""
```

### 5.3 连接状态管理

```
┌──────────────────────────────────────────────────────────┐
│ 连接管理                                                  │
├──────────┬──────────┬──────────┬──────────┬──────────────┤
│ 名称     │ 类型     │ 状态     │ 配额     │ 操作         │
├──────────┼──────────┼──────────┼──────────┼──────────────┤
│ 家庭Emby │ Emby     │ ● 在线   │ —        │ 测试 │ 编辑  │
│ NAS OpenList/Alist│ OpenList/Alist │ ● 在线 │ 1.2T/2T │ 测试 │ 编辑 │
│ 115网盘  │ 115      │ ● 在线   │ 8T/15T   │ 测试 │ 编辑  │
│ CloudDrv │ CD2      │ ○ 离线   │ —        │ 测试 │ 编辑  │
└──────────┴──────────┴──────────┴──────────┴──────────────┘
```

## 6. 存储目标 (Storage Destinations)

存储目标定义了"文件最终放在哪里"。每条记录对应一个物理存储位置。

### 6.1 存储目标模型

```go
type StorageDestination struct {
    ID           int64  `json:"id"`
    Name         string `json:"name"`          // 显示名称: "电影库", "剧集库"
    Type         string `json:"type"`          // "local" / "cloud"
    ConnectionID int64  `json:"connection_id"` // 关联的连接 (网盘类型必填)
    RemotePath   string `json:"remote_path"`   // 网盘路径 (网盘类型) 或 本地路径 (本地类型)

    // STRM 配置 (仅网盘类型可开启)
    StrmEnabled    bool   `json:"strm_enabled"`      // 是否开启STRM生成
    StrmOutputPath string `json:"strm_output_path"`  // STRM文件输出目录
    StrmBaseURL    string `json:"strm_base_url"`     // STRM中的代理URL前缀
}
```

### 6.2 存储目标示例

```
┌──────────────────────────────────────────────────────────────────────┐
│ 存储目标管理                                                         │
├──────────┬────────┬──────────┬─────────────────┬─────────────────────┤
│ 名称     │ 类型   │ 关联连接 │ 路径            │ STRM配置            │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 电影库   │ 网盘   │ OpenList/Alist │ /media/movies   │ ● 开启              │
│          │        │          │                 │ 输出: /strm/movies  │
│          │        │          │                 │ 代理: http://s:3000 │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 剧集库   │ 网盘   │ 115      │ /tv             │ ● 开启              │
│          │        │          │                 │ 输出: /strm/tv      │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 纪录片   │ 本地   │ —        │ /nas/disk1/docs │ — (本地无需STRM)    │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 综艺     │ 网盘   │ OpenList/Alist │ /media/variety  │ ○ 关闭              │
└──────────┴────────┴──────────┴─────────────────┴─────────────────────┘
```

### 6.3 STRM 管理器

STRM 管理有独立的管理页面，当前以媒体库 generation 和 manifest 为中心提供真实运行状态；定时周期仍由媒体库扫描设置负责，入库任务继续位于媒体整理页面：

```
┌────────────────────────────────────────────────────────────┐
│ STRM 管理                                                  │
├────────────────────────────────────────────────────────────┤
│                                                            │
│  媒体库运行状态                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ 已应用 / 当前 generation、最新 Run 与细分计数        │  │
│  │ 运行历史、失败重试和 manifest-owned 产物分页         │  │
│  │ 清理预览 + 短时确认令牌，不提供任意裸文件删除         │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                            │
│  同步状态                                                   │
│  ┌──────────┬────────┬────────┬────────┬────────────────┐  │
│  │ 目标     │ 总文件 │ 已同步 │ 待同步 │ 上次同步       │  │
│  ├──────────┼────────┼────────┼────────┼────────────────┤  │
│  │ 电影库   │ 1,234  │ 1,230  │ 4      │ 5分钟前        │  │
│  │ 剧集库   │ 5,678  │ 5,678  │ 0      │ 2小时前        │  │
│  └──────────┴────────┴────────┴────────┴────────────────┘  │
│                                                            │
│  操作: [立即增量刷新] [全量重建] [失败重试] [清理预览]      │
└────────────────────────────────────────────────────────────┘
```

媒体视频扩展固定为 `mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,flv,m2ts,tp,f4v`。投影伴随文件默认包含不可移除的 `srt,ssa,ass,jpg`，每个媒体库可以追加经过严格校验的小写字母/数字扩展；追加集合进入 generation policy 快照，扫描和 worker 使用同一有效集合。

完整成功且非 partial 的权威扫描会把 scan run ID/kind、generation、投影根 canonical identity 和清理资格写入不可变 policy。产物 worker 在同一事务内完成新 manifest 应用、旧 manifest 失效和 applied generation 推进，然后才执行自动清理。失败、partial、superseded、未知扫描类型或投影根变化都不自动删除。

自动与人工清理共用同一个 manifest primitive：持有同库扫描互斥锁，每个文件删除前持久化 `cleanup` claim，并重新校验 generation、root identity、manifest snapshot、ownership、kind/扩展名和 symlink/junction/reparse 边界。自动路径只认当前投影根；投影根更换后，人工预览/确认路径可仅根据每条 artifact owner 的不可变 policy 解析旧根，并把完整根身份集合哈希进确认令牌。只会删除 inactive + managed + `local_projection` 的 STRM/NFO/JPG/字幕/已快照伴随文件，绝不删除 unmanaged 同名文件。删除 manifest 与累计计数同事务提交；Server 中断后可通过 `pending|running|failed` 状态和文件 claim 重放收敛，不回滚已完成产物 generation。

v29 迁移以 additive 列加入 run 清理状态/错误/时间和媒体库最近清理摘要；历史 completed/superseded run 回填为 `skipped`，避免升级后对旧投影发生意外删除。运行历史展示清理状态、时间、计数和安全错误码。

**STRM 生成逻辑**：

```go
// pkg/strm/generator.go

package strm

type Generator struct {
    driver      cloud.Driver
    baseURL     string // Server代理URL前缀
    outputPath  string // STRM输出目录
}

// IncrementalSync 增量同步 — 只处理新增/修改的文件
func (g *Generator) IncrementalSync(ctx context.Context, remotePath string, lastSync time.Time) error {
    files, err := g.listMediaFiles(ctx, remotePath)
    if err != nil {
        return err
    }

    for _, file := range files {
        // 跳过未修改的文件
        if file.UpdatedAt.Before(lastSync) {
            continue
        }

        if err := g.generateOne(file); err != nil {
            log.Warn().Err(err).Str("file", file.Path).Msg("STRM生成失败")
            continue
        }
    }
    return nil
}

// FullSync 全量扫描 — 重新生成所有STRM
func (g *Generator) FullSync(ctx context.Context, remotePath string) error {
    files, err := g.listMediaFiles(ctx, remotePath)
    if err != nil {
        return err
    }

    for _, file := range files {
        if err := g.generateOne(file); err != nil {
            log.Warn().Err(err).Str("file", file.Path).Msg("STRM生成失败")
            continue
        }
    }
    return nil
}

// CleanInvalid 清理无效STRM — 删除指向不存在文件的STRM
func (g *Generator) CleanInvalid(ctx context.Context) (int, error) {
    cleaned := 0
    err := filepath.Walk(g.outputPath, func(path string, info os.FileInfo, err error) error {
        if filepath.Ext(path) != ".strm" {
            return nil
        }

        content, err := os.ReadFile(path)
        if err != nil {
            return nil
        }
        remotePath := g.parseSTRMPath(string(content))

        // 检查远端文件是否存在
        _, err = g.driver.Get(ctx, remotePath)
        if err != nil {
            os.Remove(path)
            cleaned++
        }
        return nil
    })
    return cleaned, err
}

// generateOne 生成单个STRM文件 + NFO + 海报
func (g *Generator) generateOne(file *cloud.File) error {
    // 解析文件名获取媒体信息
    info := parseMediaFilename(file.Name)

    // 构建本地目录结构
    localDir := g.buildOutputPath(info)
    os.MkdirAll(localDir, 0755)

    // 生成STRM文件 (内容为302代理URL)
    strmURL := fmt.Sprintf("%s/proxy/%s/%s", g.baseURL, g.driver.Name(), strings.TrimPrefix(file.Path, "/"))
    strmPath := filepath.Join(localDir, info.FileName+".strm")
    os.WriteFile(strmPath, []byte(strmURL), 0644)

    return nil
}
```

**生成的目录结构**：

```
/strm/movies/
  Inception (2010)/
    Inception (2010).strm      → http://server:3000/proxy/alist/media/movies/Inception.2010.mkv
    Inception (2010).nfo       → TMDB元数据
    poster.jpg                 → 海报
    fanart.jpg                 → 背景图
    Inception (2010).zh.srt    → 中文字幕(如果有)
```

## 7. 分类规则 (Category Rules)

分类规则定义了"下载完成后，这个文件属于什么类型，应该放到哪个存储目标"。

### 7.1 分类规则模型

```go
type CategoryRule struct {
    ID              int64  `json:"id"`
    Name            string `json:"name"`             // 规则名称: "电影", "国产剧", "纪录片"
    MediaType       string `json:"media_type"`       // "movie" / "tv" / "documentary" / "variety"
    DestinationID   int64  `json:"destination_id"`   // 关联的存储目标
    TransferMode    string `json:"transfer_mode"`    // "move" / "hardlink" / "copy" / "symlink"

    // 目录结构模板
    // 电影: "{title} ({year})/{title} ({year})"
    // 剧集: "{title} ({year})/Season {season:02d}/{title} S{season:02d}E{episode:02d}"
    DirTemplate     string `json:"dir_template"`

    // 命名规则模板
    // 电影: "{title} ({year}) - {resolution}"
    // 剧集: "{title} S{season:02d}E{episode:02d}"
    NamingTemplate  string `json:"naming_template"`

    // 自动匹配规则 (JSON)
    // 用于判断下载的文件属于哪个分类
    MatchRules      string `json:"match_rules"`      // {"category": ["Movie"], "keywords": []}
}
```

### 7.2 分类规则示例

```
┌──────────────────────────────────────────────────────────────────────────┐
│ 分类规则管理                                                             │
├──────────┬────────┬──────────┬──────────┬────────────────────────────────┤
│ 名称     │ 类型   │ 存储目标 │ 转移策略 │ 目录模板                       │
├──────────┼────────┼──────────┼──────────┼────────────────────────────────┤
│ 电影     │ movie  │ 电影库   │ 移动     │ {title} ({year})               │
│ 国产剧   │ tv     │ 剧集库   │ 移动     │ {title} ({year})/Season {S}    │
│ 纪录片   │ doc    │ 纪录片库 │ 硬链接   │ {title} ({year})               │
│ 综艺     │ var    │ 综艺库   │ 移动     │ {title}/Season {S}             │
└──────────┴────────┴──────────┴──────────┴────────────────────────────────┘
```

### 7.3 自动分类匹配

下载完成后，系统根据以下信息自动判断媒体类型：

1. **站点分类** — PT站点返回的种子分类 (Movie/TV/Documentary等)
2. **文件名解析** — parse-torrent-name 解析出 season/episode → 剧集，否则 → 电影
3. **TMDB 查询** — 通过标题查询TMDB，返回的 media_type 确认最终分类

```go
func (s *CategoryService) AutoClassify(torrent *Torrent, parsed *ParsedFilename) *CategoryRule {
    // 1. 优先用站点分类
    if rule := s.matchBySiteCategory(torrent.Category); rule != nil {
        return rule
    }

    // 2. 文件名解析判断
    if parsed.Season > 0 {
        return s.getRuleByMediaType("tv")
    }

    // 3. TMDB 查询确认
    tmdbResult, _ := s.tmdb.Search(parsed.Title, parsed.Year)
    if tmdbResult != nil && tmdbResult.MediaType == "tv" {
        return s.getRuleByMediaType("tv")
    }

    // 4. 默认归为电影
    return s.getRuleByMediaType("movie")
}
```

## 8. 站点管理 (PT Sites)

### 8.1 站点接口

```go
// pkg/scraper/site.go

type Site interface {
    Name() string
    Init(config SiteConfig) error
    Search(ctx context.Context, req *SearchRequest) ([]*Torrent, error)
    GetDetail(ctx context.Context, torrentURL string) (*TorrentDetail, error)
    GetCategories() []Category
}

type SiteConfig struct {
    Cookie    string `json:"cookie"`
    Passkey   string `json:"passkey"`
    UserID    string `json:"user_id"`
    BaseURL   string `json:"base_url"`
    UserAgent string `json:"user_agent"`
}

type SearchRequest struct {
    Keyword   string `json:"keyword"`    // 关键词搜索
    IMDBID    string `json:"imdb_id"`    // IMDB ID搜索
    DoubanID  string `json:"douban_id"`  // 豆瓣ID搜索
    Category  string `json:"category"`   // 分类过滤
    SortBy    string `json:"sort_by"`    // 排序: size/seeders/upload_time
    PageSize  int    `json:"page_size"`
    Page      int    `json:"page"`
}

type Torrent struct {
    SiteName    string    `json:"site_name"`
    Title       string    `json:"title"`
    IMDBID      string    `json:"imdb_id"`
    Size        int64     `json:"size"`
    Seeders     int       `json:"seeders"`
    Leechers    int       `json:"leechers"`
    DownloadURL string    `json:"download_url"`
    DetailURL   string    `json:"detail_url"`
    Category    string    `json:"category"`     // Movie/TV/Documentary
    UploadTime  time.Time `json:"upload_time"`
    Team        string    `json:"team"`          // 制作组
    Resolution  string    `json:"resolution"`    // 2160p/1080p/720p
    Codec       string    `json:"codec"`         // H265/AV1
    Source      string    `json:"source"`        // BluRay/WEB-DL
    Tags        []string  `json:"tags"`          // 标签: 中字, 国语, HDR...
}
```

### 8.2 站点管理 UI

```
┌──────────────────────────────────────────────────────────────┐
│ 站点管理                                                     │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────┬──────────┬────────┬────────┬────────────────┐  │
│  │ 站点     │ 状态     │ 用户   │ 上传量 │ 操作           │  │
│  ├──────────┼──────────┼────────┼────────┼────────────────┤  │
│  │ M-Team   │ ● 正常   │ VIP    │ 12.5TB │ 测试│编辑│日志 │  │
│  │ HDSky    │ ● 正常   │ PU     │ 3.2TB  │ 测试│编辑│日志 │  │
│  │ OurBits  │ ○ 过期   │ —      │ —      │ 测试│编辑│日志 │  │
│  └──────────┴──────────┴────────┴────────┴────────────────┘  │
│                                                              │
│  [+ 添加站点]  [批量导入]                                     │
└──────────────────────────────────────────────────────────────┘
```

## 9. 下载器管理 (Download Clients)

### 9.1 下载器接口

```go
// pkg/downloader/client.go

type Client interface {
    Test(ctx context.Context) (Health, error)
    Submit(ctx context.Context, req SubmitRequest) (Task, error)
    Get(ctx context.Context, taskID string) (Task, error)
    Pause(ctx context.Context, taskID string) error
    Resume(ctx context.Context, taskID string) error
    Cancel(ctx context.Context, taskID string, deleteData bool) error
}

type SubmitRequest struct {
    Source   Source // magnet/HTTP(S) URL 或受限内存 torrent bytes
    SavePath string // 由任务入队时快照的统一暂存设置解析
    Tag      string // omc-<download-task-id>
}

type Task struct {
    ID             string
    Status         string
    Progress       *float64 // unknown 使用 nil
    BytesCompleted *int64
    BytesTotal     *int64
    DownloadSpeed  *int64
    UploadSpeed    *int64
    ETASeconds     *int64
    Completed      bool
    Failed         bool
}
```

### 9.2 下载器配置

下载器配置保存在 SQLite `downloaders`，不再以包含明文密码的 YAML 作为运行事实。`base_url` 只允许无 userinfo、path、query、fragment 的 HTTP(S) origin；username/password 分字段加密。qBittorrent 配置只描述连接和能力，API 只返回 `*_configured` 布尔值而不回显凭据。统一暂存目录保存在 singleton `download_settings`，由 `settings.read/update` 控制。发起下载时直接选择目标 MediaLibrary；选择 `0` 时按媒体库顺序取第一条真正可用的库。媒体库负责 Profile、最终路径、转移方式、冲突策略和命名模板，不再引入一层重复的 DownloadRule。

每个新任务会同时快照暂存目录和目标媒体库路由，之后修改全局设置或媒体库都不会重定向在途任务。下载完成并复核真实 manifest 后创建单独的 `transfer` Job；无目标库的历史兼容任务只下载和刮削，不自动写入媒体库。

### 9.3 下载器管理 UI

```
┌──────────────────────────────────────────────────────────────┐
│ 下载器管理                                                   │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────┬──────────┬──────────┬────────────────────┐  │
│  │ 名称         │ 类型     │ 状态     │ 操作               │  │
│  ├──────────────┼──────────┼──────────┼────────────────────┤  │
│  │ 主下载器     │ qBit     │ ● 在线   │ 测试│编辑│任务列表 │  │
│  │ 备用下载器   │ Trans    │ 规划中   │ 后续 adapter       │  │
│  └──────────────┴──────────┴──────────┴────────────────────┘  │
│                                                              │
│  [+ 添加下载器]                                               │
│                                                              │
│  新建任务: [磁力/URL] [上传种子] [选择下载器] [确认并入队]      │
│  Telemetry: 进度 / 下载速度 / 上传速度 / ETA / 安全错误        │
└──────────────────────────────────────────────────────────────┘
```

## 10. 发现页 (Discovery)

发现页是用户与系统交互的核心页面，聚合多个PT站点的资源。

### 10.1 聚合搜索

```go
// internal/services/discovery.go

type DiscoveryService struct {
    siteMgr    *SiteManager
    tmdb       *TmdbScraper
    downloader *DownloadService
    category   *CategoryService
}

// Search 聚合搜索 — 同时查询所有已配置站点
func (d *DiscoveryService) Search(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
    sites := d.siteMgr.GetActiveSites()

    // 并发搜索所有站点
    type siteResult struct {
        siteName string
        torrents []*Torrent
        err      error
    }

    ch := make(chan siteResult, len(sites))
    for _, site := range sites {
        go func(s Site) {
            torrents, err := s.Search(ctx, req)
            ch <- siteResult{s.Name(), torrents, err}
        }(site)
    }

    var allResults []*SearchResult
    for range sites {
        res := <-ch
        if res.err != nil {
            continue
        }
        for _, t := range res.torrents {
            allResults = append(allResults, &SearchResult{
                Torrent:  t,
                SiteName: res.siteName,
            })
        }
    }

    // 自动匹配TMDB元数据
    for _, r := range allResults {
        if r.Torrent.IMDBID != "" {
            r.TMDBInfo, _ = d.tmdb.GetByIMDBID(ctx, r.Torrent.IMDBID)
        } else {
            parsed := parseFilename(r.Torrent.Title)
            r.TMDBInfo, _ = d.tmdb.Search(parsed.Title, parsed.Year)
        }
    }

    // 按相关度排序
    sortResults(allResults, req)

    return allResults, nil
}
```

### 10.2 一键下载

```go
// 一键下载: 选择种子 → 自动分类 → 下载到正确目录
func (d *DiscoveryService) Download(ctx context.Context, userID int64, torrent *Torrent) (*DownloadTask, error) {
    // 1. 自动匹配媒体类型和分类规则
    parsed := parseFilename(torrent.Title)
    rule := d.category.AutoClassify(torrent, parsed)

    // 2. 确定下载目录
    dest, _ := d.category.GetDestination(rule.DestinationID)
    downloadPath := getDownloadPath(dest, rule)

    // 3. 提交到下载器
    task, err := d.downloader.AddTorrent(ctx, &AddRequest{
        TorrentURL: torrent.DownloadURL,
        SavePath:   downloadPath,
        Category:   rule.MediaType,
        Name:       torrent.Title,
    })

    // 4. 记录下载任务 (关联用户ID)
    d.saveDownloadTask(userID, task, torrent, rule)

    return task, nil
}
```

### 10.3 发现页 UI

```
┌──────────────────────────────────────────────────────────────────────────┐
│ 发现页                                                                   │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  搜索: [盗梦空间__________________________] [搜索]  筛选: [全部 ▼]        │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │ 盗梦空间 Inception (2010)                                          │  │
│  │ TMDB: 8.8/10 | 科幻/冒险/动作 | 诺兰 | 莱昂纳多                    │  │
│  │ ┌────────────────────────────────────────────────────────────────┐ │  │
│  │ │ 来源   │ 标题                        │ 大小    │ 做种 │ 操作  │ │  │
│  │ ├────────┼─────────────────────────────┼─────────┼──────┼───────┤ │  │
│  │ │ M-Team │ Inception.2010.2160p.UHD... │ 45.2GB  │ 128  │ ⬇下载 │ │  │
│  │ │ HDSky  │ 盗梦空间.2010.BluRay.1080p  │ 12.8GB  │ 56   │ ⬇下载 │ │  │
│  │ │ OurBits│ Inception.2010.1080p.x265   │ 8.5GB   │ 23   │ ⬇下载 │ │  │
│  │ └────────┴─────────────────────────────┴─────────┴──────┴───────┘ │  │
│  │                                                                    │  │
│  │ 星际穿越 Interstellar (2014)                                       │  │
│  │ TMDB: 8.7/10 | 科幻/冒险/剧情 | 诺兰 | 马修·麦康纳                 │  │
│  │ ...                                                                │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                                                                          │
│  追更列表:                                                               │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────────────────┐   │
│  │ 剧名     │ 当前进度 │ 站点     │ 下次检查 │ 操作                 │   │
│  ├──────────┼──────────┼──────────┼──────────┼──────────────────────┤   │
│  │ 三体     │ S01E08   │ M-Team   │ 明天3:00 │ [暂停] [编辑] [删除] │   │
│  │ 庆余年2  │ S02E05   │ HDSky    │ 明天3:00 │ [暂停] [编辑] [删除] │   │
│  └──────────┴──────────┴──────────┴──────────┴──────────────────────┘   │
└──────────────────────────────────────────────────────────────────────────┘
```

### 10.4 追更 (Follow/Subscribe)

```go
// internal/services/follow.go

type FollowService struct {
    siteMgr    *SiteManager
    tmdb       *TmdbScraper
    downloader *DownloadService
    category   *CategoryService
    scheduler  *cron.Cron
}

// Follow 创建追更任务
func (f *FollowService) Follow(ctx context.Context, userID int64, req *FollowRequest) error {
    // 1. 查询TMDB获取剧集信息
    tmdbInfo, err := f.tmdb.GetDetail(ctx, req.TMDBID)
    if err != nil {
        return err
    }

    // 2. 创建追更记录
    follow := &FollowTask{
        UserID:       userID,
        TMDBID:       req.TMDBID,
        Title:        tmdbInfo.Title,
        IMDBID:       tmdbInfo.IMDBID,
        Season:       req.Season,
        TotalEpisodes: tmdbInfo.NumberOfEpisodes,
        // 匹配规则
        SiteFilter:   req.SiteFilter,    // 只在指定站点搜索
        QualityFilter: req.QualityFilter, // 质量偏好: "1080p以上, H265"
        GroupFilter:  req.GroupFilter,    // 制作组偏好
        // 搜索间隔
        CronExpr:     req.CronExpr,      // 默认每天3:00
        // 状态
        Status:       "active",
        LastEpisode:  req.StartEpisode,   // 从第几集开始追
    }

    return f.saveFollowTask(follow)
}

// ExecuteFollow 执行追更 — 定时调用
func (f *FollowService) ExecuteFollow(ctx context.Context, task *FollowTask) error {
    // 1. 在指定站点搜索剧名
    searchReq := &SearchRequest{
        IMDBID:   task.IMDBID,
        Keyword:  task.Title,
        Category: "TV",
    }

    sites := f.getSites(task.SiteFilter)
    var allTorrents []*Torrent
    for _, site := range sites {
        torrents, _ := site.Search(ctx, searchReq)
        allTorrents = append(allTorrents, torrents...)
    }

    // 2. 过滤: 只要缺少的集数
    missing := f.getMissingEpisodes(task)
    matched := f.filterTorrents(allTorrents, missing, task)

    // 3. 匹配质量偏好
    best := f.selectBest(matched, task.QualityFilter, task.GroupFilter)

    // 4. 下载
    for _, torrent := range best {
        rule := f.category.AutoClassify(torrent, parseFilename(torrent.Title))
        f.downloader.AddTorrent(ctx, &AddRequest{
            TorrentURL: torrent.DownloadURL,
            SavePath:   getDownloadPath(rule),
            Category:   "tv",
            Name:       torrent.Title,
        })
    }

    // 5. 更新追更状态
    task.LastCheck = time.Now()
    f.saveFollowTask(task)

    return nil
}

// getMissingEpisodes 获取缺少的集数
// 对比 TMDB 上的总集数和本地已有集数
func (f *FollowService) getMissingEpisodes(task *FollowTask) []int {
    // 查询本地已有集数 (从媒体库记录中获取)
    local := f.getExistingEpisodes(task.TMDBID, task.Season)

    // TMDB 上的总集数
    total := task.TotalEpisodes

    var missing []int
    for ep := 1; ep <= total; ep++ {
        if !local[ep] {
            missing = append(missing, ep)
        }
    }
    return missing
}
```

## 11. 文件转移引擎 (Transfer Engine)

下载完成后自动触发独立、可恢复的转移流程。当前本地闭环以 MediaLibrary 为路由事实：下载时已经选定目标库并快照配置，转移阶段不再重新猜测目标。扫描器/监听器保持只读，只有 TransferService 能在经过边界校验后写入媒体库根目录。

本地实现支持 `move`、`copy`、`symlink`；115 云端实现支持同一 Connection 内的 `move`、`copy`，两者都复用 `ask`、`overwrite`、`skip`、`rename` 冲突策略。`ask` 会产生 ActionRequest 并释放 worker slot；115 覆盖先把已验证位于目标媒体库根内的冲突项送入回收站，复制结果无法唯一确认时保留数据并失败。成功后递增媒体库 `dirty_generation`，交给并发监听/对账机制刷新索引。跨账号/跨网盘传输、STRM、媒体服务器通知和 hardlink 仍按后续切片接入。

### 11.1 转移引擎

```go
// internal/services/transfer.go

type TransferService struct {
    category   *CategoryService
    metadata   *MetadataService
    strm       *STRMService
    notifier   *NotifyService
}

// OnDownloadComplete 下载完成回调 — 核心转移流程
func (t *TransferService) OnDownloadComplete(ctx context.Context, task *DownloadTask) error {
    // 1. 自动分类
    parsed := parseFilename(task.TorrentName)
    rule := t.category.AutoClassify(task.Torrent, parsed)

    // 2. 查询TMDB元数据
    tmdbInfo, _ := t.metadata.Search(parsed.Title, parsed.Year)

    // 3. 构建目标路径
    dest, _ := t.category.GetDestination(rule.DestinationID)
    targetPath := buildTargetPath(dest, rule, parsed, tmdbInfo)

    // 4. 执行转移 (根据策略)
    var err error
    switch rule.TransferMode {
    case "move":
        err = moveFile(task.SavePath, targetPath)
    case "hardlink":
        err = os.Link(task.SavePath, targetPath)
    case "copy":
        err = copyFile(task.SavePath, targetPath)
    case "symlink":
        err = os.Symlink(task.SavePath, targetPath)
    }
    if err != nil {
        return fmt.Errorf("transfer failed: %w", err)
    }

    // 5. 如果目标是网盘且开启了STRM → 生成STRM
    if dest.Type == "cloud" && dest.StrmEnabled {
        t.strm.GenerateOne(ctx, dest, targetPath)
    }

    // 6. 通知媒体服务器刷新
    t.notifier.RefreshMediaServers(ctx, dest)

    // 7. 通知Player客户端
    title := parsed.Title
    if tmdbInfo != nil {
        title = tmdbInfo.Title
    }
    t.notifier.NotifyPlayer(ctx, &PlayerNotification{
        Type:      "media_added",
        Title:     title,
        MediaType: rule.MediaType,
    })

    return nil
}

// buildTargetPath 构建目标路径
// 电影: /movies/Inception (2010)/Inception (2010).mkv
// 剧集: /tv/三体 (2023)/Season 01/三体 S01E08.mkv
func buildTargetPath(dest *StorageDestination, rule *CategoryRule, parsed *ParsedFilename, tmdb *TmdbResult) string {
    title := parsed.Title
    year := parsed.Year
    if tmdb != nil {
        title = tmdb.Title
        year = tmdb.Year
    }

    dirName := rule.DirTemplate
    dirName = strings.ReplaceAll(dirName, "{title}", title)
    dirName = strings.ReplaceAll(dirName, "{year}", fmt.Sprintf("%d", year))
    dirName = strings.ReplaceAll(dirName, "{season:02d}", fmt.Sprintf("%02d", parsed.Season))

    fileName := rule.NamingTemplate
    fileName = strings.ReplaceAll(fileName, "{title}", title)
    fileName = strings.ReplaceAll(fileName, "{year}", fmt.Sprintf("%d", year))
    fileName = strings.ReplaceAll(fileName, "{season:02d}", fmt.Sprintf("%02d", parsed.Season))
    fileName = strings.ReplaceAll(fileName, "{episode:02d}", fmt.Sprintf("%02d", parsed.Episode))
    fileName = strings.ReplaceAll(fileName, "{resolution}", parsed.Resolution)
    fileName += filepath.Ext(parsed.FileName) // 保留原始扩展名

    return filepath.Join(dest.RemotePath, dirName, fileName)
}
```

### 11.2 转移流程图

```
下载完成
  │
  ▼
自动分类 (站点分类 + 文件名解析 + TMDB查询)
  │
  ▼
查询TMDB元数据 (标题、年份、海报、简介)
  │
  ▼
构建目标路径 (根据分类规则的模板)
  │
  ▼
执行转移策略
  │
  ├── move (默认) ──→ 移动文件到目标目录
  │                    原下载目录清空
  │
  ├── hardlink ──→ 创建硬链接
  │                 保留原文件 (保种需求)
  │
  ├── copy ──→ 复制文件
  │             保留原文件
  │
  └── symlink ──→ 创建软链接
  │
  ▼
目标是网盘 + 开启STRM？
  │
  ├── 是 → 生成STRM文件到指定目录 + NFO + 海报
  │
  └── 否 → 跳过
  │
  ▼
通知 Emby/Jellyfin 刷新媒体库 (REST API)
  │
  ▼
通知 Player 客户端 (WebSocket)
```

## 12. 302代理引擎 (302 Proxy)

播放网盘上的STRM文件时，302代理将请求重定向到云盘CDN：

115 的签名 STRM 默认采用有界双设备策略：第一台活跃设备使用原文件 pickcode，第二台在 OhMyCine 专属临时目录创建一个短命副本并使用副本 pickcode，第三台返回明确的并发上限。设备路由键只保存 `Remote IP + User-Agent` 的 SHA-256，不保存原始值。副本 lease 持久化以支持崩溃恢复；直链签发后只将该 lease 持有的精确目录送入回收站，再使用可选的 AES-GCM 回收站安全码按同一 item ID 永久删除。自动流程永不以空 ID 清空用户整个 115 回收站，安全码缺失或错误时保留待清理事实并指数退避重试。

```go
// pkg/proxy/engine.go

type Engine struct {
    drivers map[string]cloud.Driver
    cache   *URLCache
    logger  *zerolog.Logger
}

func (e *Engine) HandlePlayback(w http.ResponseWriter, r *http.Request) {
    driverName, filePath := parsePlaybackPath(r.URL.Path)

    driver, ok := e.drivers[driverName]
    if !ok {
        http.Error(w, "Driver not found", http.StatusNotFound)
        return
    }

    // 检查缓存
    cacheKey := driverName + ":" + filePath
    if cached, ok := e.cache.Get(cacheKey); ok {
        http.Redirect(w, r, cached.URL, http.StatusFound)
        return
    }

    // 获取真实下载URL
    downloadURL, err := driver.GetDownloadURL(r.Context(), filePath)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // 缓存URL
    e.cache.Set(cacheKey, downloadURL, downloadURL.ExpiresAt)

    // 302重定向
    for key, value := range downloadURL.Headers {
        w.Header().Set(key, value)
    }
    http.Redirect(w, r, downloadURL.URL, http.StatusFound)
}
```

**302播放流程**：

```
Emby/Jellyfin 扫描STRM库
  │
  ▼ 播放请求
GET /proxy/alist/media/movies/Inception.2010.mkv
  │
  ▼
OhMyCine Server (302 Proxy)
  │ 1. 查找alist驱动
  │ 2. 调用 GetDownloadURL()
  │ 3. 获取真实CDN URL
  │ 4. 缓存URL
  │
  ▼ HTTP 302 Found
  Location: https://cdn.example.com/real-url?token=xxx
  │
  ▼
客户端直接从CDN串流 (不经Server，零带宽消耗)
```

## 13. 用户管理

### 13.1 用户与 RBAC 模型

首版已经采用关系型 RBAC，不再使用 `users.role + permissions JSON 页面列表`：

```text
users ──< user_roles >── roles ──< role_permissions >── permissions
  │
  └──< sessions
```

- `users` 保存规范化唯一用户名、bcrypt 密码哈希、状态、唯一 owner 标记和 `authz_version`。
- `roles` 分为受保护的系统角色和可编辑/停用的自定义角色。
- `permissions.code` 是稳定安全契约，格式为 `<resource>.<action>`；由代码内 canonical catalog 维护，管理员不能创建任意权限码。
- 用户可分配多个角色，有效权限是所有 active 角色 allow 集合的并集；首版不支持 deny、继承、ABAC 或资源实例 ACL。
- 浏览器 Cookie 中只保存高熵随机 session token，数据库只保存 SHA-256 哈希及 idle/absolute 过期、撤销状态。

### 13.2 权限设计

- `administrator`：受保护系统角色，授权器把它解析为完整 permission catalog。
- `operator`：面向后续连接、目标、STRM Run 和媒体服务器刷新，不含用户/角色/秘密导出权限。
- `viewer`：只读状态与后续脱敏业务摘要。
- 自定义角色：只能组合 canonical permission code；非系统管理员只能授予自己已经拥有的权限，阻止委派式权限提升。
- Router route meta、导航、按钮和 Gin API 使用相同 permission code。例如用户页面和 `GET /api/v1/users` 都要求 `users.read`，创建按钮和 `POST /api/v1/users` 都要求 `users.create`。
- owner 不能被删除、停用或失去 `administrator`；所有用户/角色写操作在事务内校验变更后仍存在有效管理员。
- 首版禁止账户修改自己的角色、停用或删除自己，避免误锁死。普通用户任务的 own/all 数据范围在业务 API 实现时继续由 service policy 强制执行。

### 13.3 用户管理 UI

```
┌──────────────────────────────────────────────────────────────────┐
│ 用户管理                                                         │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────┬────────┬──────────────────────────┬──────────────┐ │
│  │ 用户名   │ 角色   │ 可访问页面               │ 操作         │ │
│  ├──────────┼────────┼──────────────────────────┼──────────────┤ │
│  │ admin    │ 管理员 │ 全部                     │ 编辑         │ │
│  │ 张三     │ 用户   │ 发现页,媒体库,设置       │ 编辑│删除    │ │
│  │ 李四     │ 用户   │ 媒体库                   │ 编辑│删除    │ │
│  └──────────┴────────┴──────────────────────────┴──────────────┘ │
│                                                                  │
│  [+ 添加用户]                                                    │
└──────────────────────────────────────────────────────────────────┘
```

## 14. 文件管理

文件管理页面让用户浏览和管理各个连接中的文件。

```
┌──────────────────────────────────────────────────────────────────┐
│ 文件管理                                                         │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  数据源: [OpenList/Alist ▼]                                      │
│                                                                  │
│  /media/movies/                                                  │
│  ├── Inception (2010)/                                           │
│  │   ├── Inception.2010.2160p.UHD.BluRay.x265.mkv  45.2GB      │
│  │   ├── Inception.2010.zh.srt                      128KB      │
│  │   └── poster.jpg                             2.1MB          │
│  ├── Interstellar (2014)/                                        │
│  │   └── ...                                                    │
│  └── ...                                                         │
│                                                                  │
│  操作: [上传] [新建文件夹] [刷新] [返回上级]                      │
└──────────────────────────────────────────────────────────────────┘
```

## 15. 系统设置

### 15.1 设置页面结构

```
┌──────────────────────────────────────────────────────────────────┐
│ 设置                                                             │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  基础设置                                                        │
│  ├─ 服务器: 端口/主机/HTTPS                                      │
│  ├─ 数据库: SQLite (默认；PostgreSQL 为未来可选部署)              │
│  └─ 日志: 级别/路径/轮转                                         │
│                                                                  │
│  元数据                                                          │
│  ├─ TMDB API Key: [________________________]                     │
│  ├─ 语言偏好: [中文 ▼]                                           │
│  └─ 图片质量: [原始 ▼]                                           │
│                                                                  │
│  AI 助手                                                         │
│  ├─ Provider: [OpenAI ▼]                                         │
│  ├─ API Key: [________________________]                          │
│  ├─ Model: [gpt-4o ▼]                                           │
│  └─ Base URL: [https://api.openai.com] (可选自定义)              │
│                                                                  │
│  302代理                                                         │
│  ├─ 监听端口: [3000]                                             │
│  ├─ URL缓存时间: [30分钟]                                        │
│  └─ CORS: [✓] 启用                                              │
│                                                                  │
│  [保存设置]                                                      │
└──────────────────────────────────────────────────────────────────┘
```

## 16. REST API 设计

### 16.1 API 端点总览

```yaml
# ====== 认证 ======
GET    /api/v1/setup/status                  # 首次设置/安全恢复状态
POST   /api/v1/setup/owner                   # 仅无用户时创建唯一 owner
POST   /api/v1/auth/login                    # 登录并设置 opaque HttpOnly Cookie
POST   /api/v1/auth/logout                   # 登出
GET    /api/v1/auth/me                       # 当前用户信息
GET    /api/v1/auth/csrf                     # 获取 session-bound CSRF token

# ====== 连接管理 ======
GET    /api/v1/connections                   # 连接列表
POST   /api/v1/connections                   # 添加连接
PUT    /api/v1/connections/{id}              # 更新连接
DELETE /api/v1/connections/{id}              # 删除连接
POST   /api/v1/connections/{id}/test         # 测试连接
GET    /api/v1/connections/{id}/quota        # 获取配额 (网盘)

# ====== 存储目标 ======
GET    /api/v1/destinations                  # 存储目标列表
POST   /api/v1/destinations                  # 添加存储目标
PUT    /api/v1/destinations/{id}             # 更新存储目标
DELETE /api/v1/destinations/{id}             # 删除存储目标

# ====== 分类规则 ======
GET    /api/v1/categories                    # 分类规则列表
POST   /api/v1/categories                    # 添加分类规则
PUT    /api/v1/categories/{id}              # 更新分类规则
DELETE /api/v1/categories/{id}              # 删除分类规则

# ====== 站点管理 ======
GET    /api/v1/sites                         # 站点列表
POST   /api/v1/sites                         # 添加站点
PUT    /api/v1/sites/{id}                    # 更新站点
DELETE /api/v1/sites/{id}                    # 删除站点
POST   /api/v1/sites/{id}/test               # 测试站点连接
GET    /api/v1/sites/{id}/categories         # 获取站点分类

# ====== 下载器管理 ======
GET    /api/v1/downloaders                   # 下载器列表（脱敏）
POST   /api/v1/downloaders                   # 添加下载器
PATCH  /api/v1/downloaders/{id}             # 更新下载器/凭据
DELETE /api/v1/downloaders/{id}             # 删除无活跃任务引用的配置
POST   /api/v1/downloaders/{id}/test        # 测试下载器连接
GET    /api/v1/downloads                     # owner/all 范围下载事实
POST   /api/v1/downloads                     # 提交 magnet/URL/torrent

# ====== 发现页 ======
POST   /api/v1/discovery/search              # 聚合搜索
POST   /api/v1/discovery/download            # 一键下载
GET    /api/v1/discovery/trending            # 热门资源
GET    /api/v1/discovery/latest              # 最新资源

# ====== 追更 ======
GET    /api/v1/follows                       # 追更列表 (用户隔离)
POST   /api/v1/follows                       # 创建追更
PUT    /api/v1/follows/{id}                  # 更新追更
DELETE /api/v1/follows/{id}                  # 删除追更
POST   /api/v1/follows/{id}/pause            # 暂停追更
POST   /api/v1/follows/{id}/resume           # 恢复追更
POST   /api/v1/follows/{id}/execute          # 立即执行追更

# ====== 下载任务 ======
GET    /api/v1/downloads                     # 下载任务列表 (用户隔离)
POST   /api/v1/downloads                     # 添加下载任务
GET    /api/v1/downloads/{id}                # 任务详情
DELETE /api/v1/downloads/{id}                # 删除任务
POST   /api/v1/downloads/{id}/pause          # 暂停
POST   /api/v1/downloads/{id}/resume         # 恢复

# ====== 转移任务 ======
GET    /api/v1/transfers                     # 转移任务列表
GET    /api/v1/transfers/{id}                # 转移任务详情
DELETE /api/v1/transfers/{id}                # 删除终态整理记录，不删除真实文件
POST   /api/v1/jobs/{transferJobID}/retry     # 仅重试失败的转移 Job

# ====== STRM管理 ======
GET    /api/v1/strm/status                   # STRM同步状态
POST   /api/v1/strm/sync/incremental         # 立即增量同步
POST   /api/v1/strm/sync/full                # 立即全量同步
POST   /api/v1/strm/clean                    # 清理无效STRM
GET    /api/v1/strm/config                   # STRM定时任务配置
PUT    /api/v1/strm/config                   # 更新STRM定时任务配置

# ====== 302代理 (非REST，播放用) ======
GET    /proxy/{driver}/{path...}             # 302重定向播放

# ====== 元数据 ======
POST   /api/v1/metadata/search               # 搜索元数据
POST   /api/v1/metadata/match                # 自动匹配
GET    /api/v1/metadata/{tmdb_id}            # 获取元数据

# ====== 媒体库 ======
GET    /api/v1/media-libraries                         # 媒体库列表
GET    /api/v1/media-libraries/{id}                    # 媒体库详情
POST   /api/v1/media-libraries/{id}/scan               # 立即扫描
GET    /api/v1/media-libraries/{id}/entries            # 文件事实分页
GET    /api/v1/media-libraries/{id}/catalog            # 作品聚合分页
GET    /api/v1/media-libraries/{id}/runs               # 扫描记录
GET    /api/v1/media-libraries/{id}/recognitions       # 识别单元分页
POST   /api/v1/media-libraries/{id}/recognitions/{token}/retry
GET    /api/v1/media-libraries/{id}/recognitions/{token}/tmdb-candidates
PUT    /api/v1/media-libraries/{id}/recognitions/{token}/override
DELETE /api/v1/media-libraries/{id}/recognitions/{token}/override

# ====== 文件管理 ======
GET    /api/v1/files/{connection_id}/list    # 浏览文件
POST   /api/v1/files/{connection_id}/upload  # 上传文件
DELETE /api/v1/files/{connection_id}/delete  # 删除文件

# ====== 用户管理 ======
GET    /api/v1/users                         # users.read
POST   /api/v1/users                         # users.create
PATCH  /api/v1/users/{id}                    # users.update
POST   /api/v1/users/{id}/disable            # users.disable
POST   /api/v1/users/{id}/enable             # users.update
POST   /api/v1/users/{id}/reset-password     # users.update
PUT    /api/v1/users/{id}/roles              # roles.assign
DELETE /api/v1/users/{id}                    # users.delete

# ====== 角色、权限与审计 ======
GET    /api/v1/roles                         # roles.read
POST   /api/v1/roles                         # roles.create
PATCH  /api/v1/roles/{id}                    # roles.update
PUT    /api/v1/roles/{id}/permissions        # roles.update
DELETE /api/v1/roles/{id}                    # roles.delete
GET    /api/v1/permissions                   # roles.read
GET    /api/v1/audit                         # audit.read

# ====== 系统设置 ======
GET    /api/v1/settings                      # 获取设置
PUT    /api/v1/settings                      # 更新设置
GET    /api/v1/settings/metadata             # 元数据设置（凭据来源与非敏感路由）
PATCH  /api/v1/settings/metadata             # 加密保存/清除显式类型的 TMDB 凭据
POST   /api/v1/settings/metadata/test        # 使用有效凭据和当前 API 路由测试
POST   /api/v1/settings/metadata/test-token  # 候选 API Key/Read Token 测试成功后 CAS 保存
POST   /api/v1/settings/metadata/test-api    # 测试成功后启用 API HTTPS 前缀
POST   /api/v1/settings/metadata/test-image  # 测试成功后启用图片 HTTPS 前缀

# ====== 配置同步 (Player ↔ Server) ======
POST   /api/v1/sync/push                     # Player推送数据源配置
GET    /api/v1/sync/pull                     # Player拉取Server配置
GET    /api/v1/sync/status                   # 同步状态

# ====== WebSocket ======
WS     /ws/events                            # 实时事件推送
```

### 16.2 WebSocket 事件

```json
// 下载进度
{"type": "download.progress", "data": {"task_id": "xxx", "progress": 45.2, "speed": "12MB/s"}}

// 下载完成
{"type": "download.completed", "data": {"task_id": "xxx", "title": "Inception"}}

// 转移进度
{"type": "transfer.progress", "data": {"task_id": "xxx", "progress": 80.0}}

// 转移完成
{"type": "transfer.completed", "data": {"title": "Inception", "destination": "电影库"}}

// STRM同步进度
{"type": "strm.progress", "data": {"total": 1000, "current": 567}}

// 新媒体入库
{"type": "media.added", "data": {"id": "xxx", "title": "Inception", "type": "movie"}}

// 追更发现新集
{"type": "follow.new_episode", "data": {"title": "三体", "episode": "S01E09"}}

// 站点状态变化
{"type": "site.status_changed", "data": {"site": "mteam", "status": "connected"}}
```

## 17. 数据库设计

```sql
-- ========================================
-- 连接管理
-- ========================================

CREATE TABLE connections (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,                    -- 显示名称
    type        TEXT NOT NULL,                    -- emby/jellyfin/alist/clouddrive2/115/quark/...
    config      TEXT NOT NULL,                    -- JSON配置 (加密存储认证信息)
    status      TEXT DEFAULT 'unknown',           -- online/offline/error
    quota_total INTEGER DEFAULT 0,
    quota_used  INTEGER DEFAULT 0,
    last_check  DATETIME,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- 存储目标
-- ========================================

CREATE TABLE storage_destinations (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL,               -- "电影库", "剧集库"
    type             TEXT NOT NULL,               -- "local" / "cloud"
    connection_id    INTEGER,                     -- 关联的连接 (网盘类型必填)
    remote_path      TEXT NOT NULL,               -- 网盘路径或本地路径
    strm_enabled     BOOLEAN DEFAULT false,       -- 是否开启STRM生成
    strm_output_path TEXT,                         -- STRM文件输出目录
    strm_base_url    TEXT,                         -- STRM代理URL前缀
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (connection_id) REFERENCES connections(id)
);

-- ========================================
-- 分类规则
-- ========================================

CREATE TABLE category_rules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,                -- "电影", "国产剧"
    media_type      TEXT NOT NULL,                -- "movie"/"tv"/"documentary"/"variety"
    destination_id  INTEGER NOT NULL,             -- 关联的存储目标
    transfer_mode   TEXT DEFAULT 'move',          -- "move"/"hardlink"/"copy"/"symlink"
    dir_template    TEXT NOT NULL,                -- 目录模板
    naming_template TEXT NOT NULL,                -- 命名模板
    match_rules     TEXT,                         -- JSON: 自动匹配规则
    sort_order      INTEGER DEFAULT 0,            -- 排序 (匹配时从前往后)
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (destination_id) REFERENCES storage_destinations(id)
);

-- ========================================
-- PT站点
-- ========================================

CREATE TABLE sites (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    site_type   TEXT NOT NULL,                    -- 站点标识: mteam/hdsky/ourbits/...
    config      TEXT NOT NULL,                    -- JSON: Cookie/Passkey等 (加密)
    status      TEXT DEFAULT 'unknown',           -- online/offline/expired
    user_info   TEXT,                             -- JSON: 用户等级/上传量等
    last_check  DATETIME,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- 下载器
-- ========================================

CREATE TABLE downloaders (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    type                TEXT NOT NULL,             -- fake/qbittorrent；Transmission 后续
    base_url            TEXT NOT NULL,
    username_ciphertext TEXT NOT NULL,
    password_ciphertext TEXT NOT NULL,
    storage_id          INTEGER,                   -- v7 legacy compatibility; v8 后不再读写
    capabilities_json   TEXT NOT NULL,
    last_health_status  TEXT NOT NULL DEFAULT 'unknown',
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL
);

CREATE TABLE download_settings (
    id            INTEGER PRIMARY KEY CHECK(id = 1),
    storage_id    INTEGER,
    relative_path TEXT NOT NULL DEFAULT '/',
    revision      INTEGER NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL,
    FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE RESTRICT
);

-- ========================================
-- 下载任务
-- ========================================

CREATE TABLE download_tasks (
    id                TEXT PRIMARY KEY,
    owner_id          INTEGER NOT NULL,
    job_id            TEXT NOT NULL UNIQUE,
    downloader_id     TEXT,
    source_ciphertext TEXT NOT NULL,               -- magnet/URL/torrent 加密 envelope
    staging_absolute_path TEXT NOT NULL DEFAULT '',-- 新任务入队时快照，不进入公开 DTO
    staging_storage_id INTEGER,                    -- 旧任务兼容 fallback
    staging_relative_path TEXT NOT NULL DEFAULT '',-- 旧任务兼容 fallback
    provider_task_id  TEXT NOT NULL DEFAULT '',
    phase             TEXT NOT NULL,
    progress          REAL,                        -- unknown 保持 NULL
    bytes_completed   INTEGER,
    bytes_total       INTEGER,
    download_speed    INTEGER,
    upload_speed      INTEGER,
    eta_seconds       INTEGER,
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL,
    FOREIGN KEY (owner_id) REFERENCES users(id),
    FOREIGN KEY (job_id) REFERENCES jobs(id),
    FOREIGN KEY (downloader_id) REFERENCES downloaders(id) ON DELETE SET NULL
);

-- ========================================
-- 转移任务
-- ========================================

CREATE TABLE transfer_tasks (
    id                TEXT PRIMARY KEY,
    owner_id          INTEGER NOT NULL,
    job_id            TEXT NOT NULL UNIQUE,
    download_task_id  TEXT NOT NULL UNIQUE,
    library_id        INTEGER NOT NULL,
    library_name      TEXT NOT NULL,
    manifest_json     TEXT NOT NULL,               -- 私有 provider-relative 清单，永不直接序列化
    plan_summary_json TEXT NOT NULL DEFAULT '',    -- 有界、再次校验的目标相对结果摘要
    phase             TEXT NOT NULL,
    processed_files   INTEGER NOT NULL DEFAULT 0,
    total_files       INTEGER NOT NULL DEFAULT 0,
    last_error_code   TEXT NOT NULL DEFAULT '',
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL,
    finished_at       DATETIME,
    FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE RESTRICT,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
    FOREIGN KEY (download_task_id) REFERENCES download_tasks(id) ON DELETE CASCADE
);

-- ========================================
-- 追更任务
-- ========================================

CREATE TABLE follow_tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,             -- 创建者
    tmdb_id         INTEGER NOT NULL,
    title           TEXT NOT NULL,
    imdb_id         TEXT,
    season          INTEGER DEFAULT 1,
    total_episodes  INTEGER,
    last_episode    INTEGER DEFAULT 0,            -- 已追到第几集
    site_filter     TEXT,                         -- JSON: 指定站点列表
    quality_filter  TEXT,                         -- 质量偏好: "1080p+, H265"
    group_filter    TEXT,                         -- 制作组偏好
    cron_expr       TEXT DEFAULT '0 3 * * *',     -- 默认每天3:00
    status          TEXT DEFAULT 'active',        -- active/paused/completed
    last_check      DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- ========================================
-- 媒体库
-- ========================================

CREATE TABLE media (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title           TEXT NOT NULL,
    original_title  TEXT,
    year            INTEGER,
    type            TEXT NOT NULL,                -- movie/series/episode
    imdb_id         TEXT,
    tmdb_id         INTEGER,
    douban_id       TEXT,
    overview        TEXT,
    rating          REAL,
    genres          TEXT,                         -- JSON数组
    directors       TEXT,                         -- JSON数组
    cast_list       TEXT,                         -- JSON数组
    poster_url      TEXT,
    fanart_url      TEXT,
    local_path      TEXT,                         -- 本地文件路径 (转移后的目标路径)
    strm_path       TEXT,                         -- STRM文件路径 (网盘类型)
    cloud_path      TEXT,                         -- 云盘原始路径
    destination_id  INTEGER,                      -- 关联的存储目标
    status          TEXT DEFAULT 'active',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (destination_id) REFERENCES storage_destinations(id)
);

-- ========================================
-- 用户
-- ========================================

-- 实际实现使用显式 schema_migrations 维护以下认证/RBAC表：
-- users, roles, permissions, user_roles, role_permissions, sessions, audit_logs。
-- 完整约束以 server/internal/database/migrations.go 为准，包括唯一 owner 部分索引、
-- 复合主键、外键、session 过期/撤销索引和审计索引。

-- ========================================
-- STRM定时任务配置
-- ========================================

CREATE TABLE strm_schedules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    destination_id  INTEGER NOT NULL,
    incremental_cron TEXT DEFAULT '*/30 * * * *', -- 增量同步频率
    full_cron       TEXT DEFAULT '0 3 * * *',     -- 全量扫描频率
    clean_cron      TEXT DEFAULT '0 4 * * 0',     -- 无效清理频率 (每周日)
    last_incremental DATETIME,
    last_full       DATETIME,
    last_clean      DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (destination_id) REFERENCES storage_destinations(id)
);

-- ========================================
-- 搜索历史
-- ========================================

CREATE TABLE search_history (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER,
    keyword     TEXT NOT NULL,
    results     INTEGER DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- ========================================
-- 系统设置
-- ========================================

CREATE TABLE settings (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## 18. 通用插件平台

Server 插件系统按可组合 capability 扩展，而不是把插件限制成“站点适配器”这一种类型。Bilibili 是首个真实参考插件，但协议同时为元数据、通知、下载器、云盘/存储、媒体服务器、事件/调度、识别分类命名和声明式 UI 预留稳定扩展面。

```text
Hub/GitHub Registry → 校验并安装不可变插件包 → 隔离 WASM Runtime
                                                   ↓
                       受控 Host HTTP / Credential / KV / Log / Event
                                                   ↓
                OnlineLibrary / Metadata / Notification / DownloadPlan / ...
```

约束：

- capability 可组合，不能用互斥 `type` 把插件锁死在单一领域。
- PT 站点发现与下载保持 Server 内建，不允许第三方插件注册 PT adapter。
- 默认运行时是无 WASI 的低权限 WASM；HTTP、凭据、事件和调度由宿主代执行并逐项授权。
- 将来确需原生 SDK 的高权限能力使用独立进程，不回退到进程内 Go plugin。
- 插件不能注册裸 Gin 路由、读写全局数据库、读取全部凭据、执行任意命令或注入管理端/Player JavaScript。
- 插件 UI 只返回版本化 Schema 和声明式 DTO，由宿主自己的组件渲染。
- 固定内容 WASM 仅作为自动化 ABI fixture，不发布给用户；正式能力由独立安装的真实插件验证。

Bilibili 的站点 API、登录态、分页、签名、播放与下载解析全部位于插件包。Server 核心只处理标准 operation、权限、DTO、任务和安全网关，删除或停用 Bilibili 插件不能影响本地、115、Emby/Jellyfin、PT 等核心功能。

## 19. 配置文件格式

```yaml
# configs/config.example.yaml

server:
  host: 127.0.0.1               # 默认仅本机；部署时显式修改
  port: 3000
  mode: release                 # debug/release
  public_origin: "https://server.example.test"

database:
  driver: sqlite
  dsn: ./data/ohmycine.db
  # driver: postgres
  # dsn: "host=localhost user=omc password=xxx dbname=ohmycine port=5432"

proxy:
  # 302 URL缓存过期时间
  cache_ttl: 30m
  # 是否允许CORS
  cors: true

# 元数据
metadata:
  tmdb_api_key: ""
  language: "zh-CN"             # TMDB返回语言
  image_quality: "original"     # 海报质量: original/w500/w300

# 日志
log:
  level: info                   # debug/info/warn/error
  file: ./logs/ohmycine.log
  max_size: 100                 # MB
  max_backups: 3

# 不提供默认管理员密码。首次访问 /setup 通过事务创建唯一 owner。
```

## 20. Docker 部署

```yaml
# docker/docker-compose.yaml

services:
  ohmycine-server:
    image: ohmycine/server:latest
    container_name: ohmycine-server
    restart: unless-stopped
    ports:
      - "3000:3000"
    volumes:
      - ./data:/app/data                    # 数据库
      - ./configs:/app/configs              # 配置
      - ./strm:/app/strm                    # STRM输出目录
      - ./downloads:/app/downloads          # 下载临时目录
      - ./logs:/app/logs                    # 日志
    environment:
      - TZ=Asia/Shanghai

  # 可选: Emby
  emby:
    image: emby/embyserver:latest
    container_name: ohmycine-emby
    restart: unless-stopped
    ports:
      - "8096:8096"
    volumes:
      - ./emby/config:/config
      - ./strm:/media/library               # 共享STRM库
    environment:
      - TZ=Asia/Shanghai

  # 可选: qBittorrent
  qbittorrent:
    image: linuxserver/qbittorrent:latest
    container_name: ohmycine-qbittorrent
    restart: unless-stopped
    ports:
      - "8080:8080"
      - "6881:6881"
    volumes:
      - ./qbittorrent/config:/config
      - ./downloads:/downloads
```
