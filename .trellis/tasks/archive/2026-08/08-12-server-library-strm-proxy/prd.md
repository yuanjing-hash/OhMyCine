# Server 媒体产物、STRM 与 signed 302

## Goal

把文件扫描、qBittorrent 下载、115 离线下载、115 分享转存和 115 App 手工转存统一收敛到同一条媒体产物流水线：识别并持久化完整 TMDB 元数据快照，再按媒体库策略生成 NFO/JPG、STRM 或云盘旁挂文件，并通过安全的 signed 302 提供 STRM 播放。

数据库中的统一识别结果和完整 TMDB 快照是元数据唯一真相；不同存储类型只改变产物落点，不复制识别逻辑。

## In Scope

### 1. 完整元数据快照

- 在统一识别结果中保存本地化标题、原始标题、发行/首播日期、简介、TMDB/IMDb ID、类型、评分、时长、国家/地区、原始语言、导演、编剧、演员，以及海报、背景图、季海报和单集图片的稳定身份。
- 快照不得保存 provider 凭据、API key、绝对本地路径、带 token 的 URL 或未裁剪的原始 API 响应。
- 没有可靠识别结果时不生成 NFO/JPG；任务保留为未识别并输出可筛选日志，不用错误候选生成产物。

### 2. 媒体库产物策略

- 本地媒体库：默认在媒体文件旁生成 NFO/JPG，用户可以关闭。
- 云盘媒体库 + STRM：必须开启 signed proxy 并选择可写的本地投影根；在本地投影目录生成 STRM/NFO/JPG，不向云盘上传这些产物。
- 云盘媒体库无 STRM：默认仅保存数据库快照；可选“上传 NFO/JPG 到云盘”，默认关闭，仅在 provider 支持受限小文件上传时提供。
- local 媒体库不显示 STRM/302 选项；cloud 媒体库不能绕过 driver capability gate。
- `strm_local_root` 由用户通过 Server 目录选择器选择，不要求创建第二个 local Storage；缺失、相对路径、文件路径、Reparse Point、不可写或边界校验失败时拒绝保存。

### 3. STRM 与源伴随文件

- 固定识别 `mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,flv,m2ts,tp,f4v` 17 种视频扩展，并在投影根保留远端目录结构。普通视频替换为 `.strm`，ISO 保留为 `basename.iso.strm`，避免媒体服务器丢失镜像语义。
- STRM eligibility 先经过媒体库/规则管理提供的排除词和最小文件大小策略，过滤广告、样片与无关小视频；未识别但通过过滤的视频仍可生成 STRM，只有 NFO/JPG 要求可靠元数据匹配。
- 将源目录中的 `srt,ssa,ass,jpg` 作为不可移除的默认 source asset 扩展，并允许每个媒体库在默认集合上追加严格校验的自定义扩展；source asset facts 独立持久化并按相同目录结构同步到本地投影，不得混入媒体目录 catalog。
- 自定义扩展只保存标准化的小写后缀，不接受路径、通配符、MIME、点号、空白或控制字符；限制单项长度、总数并去重，且不能把 17 种视频扩展混入伴随文件下载集合。
- 源伴随文件下载受 provider 限速、大小上限、临时文件 + 原子替换、路径边界与日志脱敏保护。

### 4. NFO/JPG 产物约定

- 电影：`basename.nfo`、`basename-poster.jpg`、`basename-fanart.jpg`。
- 剧集根：`tvshow.nfo`、`poster.jpg`、`fanart.jpg`；季海报为 `seasonNN-poster.jpg`。
- 单集：`basename.nfo`、`basename-thumb.jpg`。
- NFO 与图片由完整 TMDB 快照确定性生成/下载；同一快照重复执行应得到相同 manifest。

### 5. signed 302

- STRM 内容仅写 `/proxy/strm/{opaque}?kid=&exp=&sig=` 形式的 Server URL，不出现 provider file ID、真实路径或上游 token URL。
- 绝对 URL 的 origin 只取经过启动校验的 `OMC_PUBLIC_ORIGIN`，不能根据请求 `Host`/`Forwarded` 头推断；若仍是只对 Server 本机有效的 loopback origin，UI 必须明确提示其它 Emby/Player 设备无法访问。
- 使用 HMAC-SHA256；签名覆盖 opaque token、key ID、expiry、library scope 和 `media-read` method class，使 GET/HEAD 可共享播放签名而其它方法不可用。
- 默认有效期 30 天，剩余 7 天时刷新 STRM；支持 active/previous key ID 平滑轮换。
- 每次请求必须先验签和校验媒体库/文件有效性，再向 driver 获取临时 DirectURL。
- 上游 URL 只允许内存缓存，并按 connection、file identity、User-Agent hash 隔离；缓存 TTL 不得超过 provider 的真实过期时间。
- 如果 provider 播放依赖无法安全由 302 表达的敏感上游 header，必须安全失败，不能把 header 或凭据塞入 URL。

### 6. 115 旁挂上传

- 115 实现受限 `UploadSmallFile` 能力，仅接受系统生成的 `.nfo` 和 `.jpg`。
- 上传前重新验证目标 parent ancestry 属于媒体库根；优先秒传，必要时走 OSS fallback，并遵循现有 115 限速、熔断与重试策略。
- 返回结果不明确时通过目标目录对账判断成功，不盲目重复上传。
- API、日志和事件不得暴露 OSS token、endpoint、local path、cookie 或 provider file ID。

### 7. 并发、队列与恢复

- 所有媒体库 supervisor/watcher 并行运行；本地 watcher 与 115 生活事件唤醒不进入全局任务队列。
- 扫描/Transfer 完成后，在同一 generation 内提交 source facts、识别结果和完整 TMDB 快照，然后创建或合并持久化 artifact Job。
- artifact Job payload 只保存 `artifact_run_id`；同一媒体库只执行最新 generation，过时任务可安全跳过。
- 产物失败不能回滚已完成的下载/转存或阻塞其它媒体库；保留可重试状态和细分模块日志。

### 8. 所有权与清理

- manifest 必须区分 Server-managed artifact 与用户原有文件。
- 默认不覆盖或删除同名但非本系统管理的 NFO/JPG/STRM/字幕；记录冲突并等待用户处理。
- 每次完整成功的全量或增量扫描完成对应 STRM 产物生成后，自动清理上一代已失效的 manifest-owned artifact；先生成/更新新投影，再删除失效产物，不得跟随 symlink/reparse point 越界。
- 任一 generation 存在扫描失败、partial、产物生成失败、superseded、投影根变化或路径/所有权校验异常时不执行自动清理，保留到 STRM 管理页人工预览处理。
- 关闭 STRM、变更投影根或人工执行清理时，仍先生成 preview，再通过短时 confirmation token 执行；人工和自动路径必须复用同一 manifest ownership、canonical root、symlink/reparse 与审计边界。
- 下载包中未被安全入库清单选中的明确非媒体垃圾只能在识别、转移和目标对账全部成功后清理；未选视频和未关联字幕默认保护，不进入自动删除集合，也不得被 qBittorrent 整包 `deleteData` 绕过。清理限定于 OhMyCine 创建的本地/云端精确下载包目录；普通媒体源和用户目录仅过滤不删除，qBittorrent 做种结束前不破坏完整性。

### 9. Emby 302 网关

- Server 增加 Emby connection 和可选 302 网关。管理员启用后获得稳定的 gateway base URL，Emby 官方客户端可以把它当作 Server 地址；普通 Emby API、图片和 WebSocket 透明转发到固定的已配置 Emby endpoint。
- 网关不向客户端请求注入 OhMyCine 保存的 Emby API key。客户端仍使用自己的 Emby 用户/Token，网关保留 Emby 的用户权限边界；Server 端 API key 仅用于连接测试和未来明确授权的刷新功能。
- Emby 创建、编辑、测试、删除、安全聚合摘要与网关开关统一位于独立顶级“播放器管理”页面；“数据源”只管理 local/cloud Storage source。两页复用同一 Connection model 和权限，不复制凭据或 endpoint。
- 302 gateway 与 Server Web/API/STRM 共用主监听端口，不提供每连接端口或 origin 输入。`0.0.0.0`/`::` 仅允许作为 listen address；所有可复制、持久化地址只取全局 `OMC_PUBLIC_ORIGIN`。
- 管理员必须可把随机 public ID 改为易记且唯一的 gateway alias，例如 `/emby/home`；alias 不是密码，Emby 自身登录/Token 仍负责认证。alias 采用大小写规范化、字符/长度白名单、保留字校验、唯一约束和 revision 乐观并发控制，修改后旧地址立即失效。
- Emby Web 产生的带应用基路径请求（例如 `/emby/<alias>/emby/users/public`）必须正确剥离且只剥离一次 gateway 前缀，并转发到固定 upstream 的 `/emby/users/public`；HTML、脚本、API、图片、WebSocket 与同源 `Location` 重写均需覆盖带 `/emby` upstream 基路径的情况，不能停在 Emby Logo 页。
- Emby Web 的 HTML5 播放器会为远程 DirectPlay 设置 `crossOrigin=anonymous`，而 115 CDN 不返回浏览器 CORS 许可。网关必须对固定 allowlist 的 Emby `basehtmlplayer.js` / `plugin.js` 静态资源执行有大小上限的确定性兼容修补，移除该赋值并强制重新验证缓存；为覆盖已被 Service Worker/Cache Storage 复用而不再请求的播放器模块，固定 `/web/index.html`、`/web`、`/web/` HTML 壳可在 `<head>` 起始处注入一个由网关固定同源路径提供的不可配置兼容脚本。不得开放任意/用户脚本或通用内容改写。
- 每个网关提供默认开启、可独立关闭的“外部播放器”和“显示同人图”策略。外部播放器只为本系统 signed STRM 的 PlaybackInfo 短时票据生成设备适配入口，协议链接不得包含 Emby API key、115 Cookie、provider file ID 或最终 CDN URL；普通 Emby 媒体不显示入口。同人图仅使用当前 Emby 用户可读取的 `BackdropImageTags` 和同源图片 API，提供轻量横向浏览与全屏预览，不加载第三方 CDN 脚本或图标。
- 拦截 GET/POST/HEAD `Items/{itemId}/PlaybackInfo`。仅当 MediaSource 指向本 Server 合法的 signed STRM proxy artifact 时，才强制 DirectPlay、禁用该 source 的转码字段，并返回带短时 playback ticket 的相对 stream URL；普通本地媒体或其它远程 URL 保持原响应。
- 拦截 Emby video/audio stream、download 和 file 路由。存在合法 playback ticket 时直接复用 signed proxy resolver 获取 provider 临时 URL 并返回 302；ticket 缺失或不匹配时透明回源 Emby，不能把普通媒体误导向云盘。
- playback ticket 绑定 gateway、item、MediaSource、artifact 和过期时间，只包含 opaque identity；不以客户端 IP 或单纯 User-Agent 作为授权依据，不把 Emby Token、signed STRM URL 或 CDN URL写入 ticket。
- Emby reverse proxy 只连接管理员保存并验证的固定 endpoint，禁用跨 origin 凭据重定向，过滤 hop-by-hop headers，并对 PlaybackInfo 请求/响应体设限。日志不得输出 Emby token、API key、MediaSource.Path、playback ticket、signed URL 或最终 CDN URL。

### 10. 115 多设备播放与精确回收站清理

- 115 signed 302 默认具备双设备播放能力；第一个活动设备使用原文件，同一媒体出现第二个活动设备时，在 OhMyCine 专属临时目录中创建受控副本并用副本 pickcode 获取独立直链。
- 设备身份只用于路由并发播放，不参与鉴权；数据库只保存不可逆设备摘要、opaque artifact 关联、临时副本身份、lease/过期时间和清理状态，不保存 IP、User-Agent、直链或 pickcode。
- 默认最多两个同时设备，超过时安全拒绝并输出 `115多设备播放` 模块日志，避免无界副制触发 115 风控。
- 副本直链签发后延迟送入回收站，并只按 OhMyCine 持久化的精确副本 `tid` 永久清理；启动和定时对账只处理本系统创建的副本。
- 默认自动任务不允许无 `tid` 清空整个 115 回收站。若未配置或无法使用 115 回收站安全码，保留精确待清理记录并重试，不扩大删除范围。

## Out of Scope

- Emby/Jellyfin 刷新和 OhMyCine Player 通知；本任务只实现 Emby 302 网关并保留产物完成事件接口。
- Jellyfin 302 网关、任意/用户自定义 Emby Web UI 脚本注入和任意“路径前缀 → URL”替换规则。固定的 302 CORS、外部播放器与同人图模块属于 Emby 网关协议适配，不属于通用脚本注入。
- 默认定时清空用户整个 115 回收站；自动清理仅限 OhMyCine 精确持有的临时副本。
- OpenList/Alist、CloudDrive2 的实际旁挂上传 adapter；本轮完成公共 capability 与 115 实现。
- 本地视频上传云盘、跨网盘传输，以及由 OhMyCine 中继云盘媒体字节/Range；Emby 控制面/API 的透明网关与最终 CDN 302 属于本任务。
- 用户自定义 NFO XML 模板和图片命名模板；在 STRM 管理页对投影文件进行脱离 manifest/规则的任意裸重命名。

### 10. STRM 管理工作区

- 将规划中的“STRM / 入库”导航收敛为独立“STRM 管理”页面；入库任务继续由媒体整理工作区负责。
- 页面按媒体库展示所有启用 STRM 的媒体库、当前/最近运行状态、generation、创建/更新/跳过/冲突/失败计数、错误摘要和历史记录。
- 管理员可以对单个媒体库执行立即增量刷新、全量重建、失败重试和失效产物清理预览；任务异步执行，不阻塞其它媒体库 watcher。
- 页面展示 manifest-owned 产物的分页列表和状态。删除必须先 preview 并使用短时 confirmation token；只删除 manifest-owned 文件，禁止把普通目录浏览器变成裸文件删除器。
- 文件命名由识别结果、分类规则和命名模板决定。发现命名错误时，引导用户修正识别/规则并重建；本轮不提供会被下一次 reconcile 改回的任意文件重命名按钮。

## Acceptance Criteria

- [ ] qBittorrent、115 离线、115 分享/App 转存和既有扫描均复用同一 metadata/artifact 入口，不各写一套刮削逻辑。
- [ ] 电影、剧集和单集的完整 TMDB 快照不包含凭据、绝对路径或原始响应；未识别条目不生成错误 sidecar。
- [ ] 本地媒体库默认在媒体旁生成正确命名的 NFO/JPG，关闭后不再生成新产物。
- [ ] cloud + STRM 必须配置 signed proxy 和有效本地投影根，并生成同结构 STRM/NFO/JPG；不向云盘上传这些产物。
- [ ] cloud 无 STRM 默认 database-only；开启上传后，115 只上传 `.nfo/.jpg`，并遵循边界校验、限速、熔断与目录对账。
- [ ] fake cloud 文件树中的全部 17 种视频扩展生成 `.strm`，`srt/ssa/ass/jpg` 同结构同步，其它扩展名不落地。
- [ ] 每个媒体库可在默认 `srt/ssa/ass/jpg` 上追加合法伴随扩展；非法、重复、超限和视频扩展被拒绝，追加扩展仍受下载大小/限速/边界/所有权约束。
- [ ] ISO 输出为 `.iso.strm`；排除词/最小大小能过滤广告小视频，未识别视频仍可生成 STRM 但不会生成伪造 NFO/JPG。
- [ ] 合法 GET/HEAD signed URL 返回 302；篡改、过期、错库、错 scope、未知 key、无效 opaque token 均拒绝，且任何输出不泄露上游 URL/凭据。
- [ ] STRM 和 Emby gateway URL 仅使用启动时校验的 `OMC_PUBLIC_ORIGIN`，恶意 Host/Forwarded 请求不能污染已生成文件或响应；loopback origin 在 UI 有清晰可达性提示。
- [ ] 全量重跑幂等，generation diff 能处理 create/update/move/delete；多个媒体库监听并行，产物工作通过持久队列合并且不会阻塞 watcher。
- [ ] 同名 unmanaged 文件不被覆盖或删除；成功且非 partial 的全量/增量扫描在新 generation 完成后自动删除失效的 manifest-owned artifact，失败/partial/边界异常时不自动删除。
- [ ] Web UI 的策略开关均有真实后端 worker/capability 支撑，并显示最近产物状态、错误和可筛选模块日志。
- [ ] fake Emby 验证普通 API/WebSocket 透明转发、STRM PlaybackInfo 强制 DirectPlay、带 ticket 的 GET/HEAD stream 返回 302、普通媒体回源，以及不同 gateway/item/source/ticket 之间无法串用。
- [ ] Emby Web 4.9.x 的固定播放器静态资源经网关移除远程 DirectPlay `crossOrigin=anonymous`；固定 HTML 壳还会优先加载同源兜底脚本，使旧模块缓存不能绕过修补。其它 HTML/JavaScript 保持透明代理，浏览器可跟随 302 播放不提供 CORS Header 的 115 CDN 媒体。（自动化路由测试已覆盖，等待真实 Emby Web 播放复验。）
- [ ] Emby gateway alias 可创建、修改、校验唯一性并并发安全更新；fake Emby 的 `/emby/users/public` 及带 upstream `/emby` 基路径场景通过网关返回成功，不再停在 Logo 页。
- [ ] Emby 网关不注入 Server 保存的 API key，不跟随携带凭据的跨 origin 重定向，响应、日志、缓存和审计均不泄露 Emby Token、MediaSource.Path、signed STRM URL 或 CDN URL。
- [ ] 外部播放器按 Windows/macOS/iOS/Android 显示合适入口，只接受带唯一短时 `omc_ticket` 的同源 gateway stream；普通 Emby 媒体不显示入口，协议链接不暴露持久凭据或最终直链。
- [ ] Emby 电影、剧集、人物和视频详情页可展示当前用户可见的背景图，支持横向浏览、点击全屏和 Escape/遮罩关闭；功能无第三方前端依赖，并可在播放器管理页独立关闭。
- [ ] “STRM 管理”可按库触发立即刷新/全量重建/重试，查看运行与历史及 managed artifact；自动清理状态与计数可见，人工清理继续通过 preview + confirmation token 且不会删除 unmanaged 文件。
- [x] 同一 115 STRM 在两个活动设备上播放时第二设备使用受控副本直链，第三设备被安全限流；副本在崩溃重启后仍能被对账、送入回收站并按精确 `tid` 清理。
- [x] 系统专属暂存中的明确非媒体垃圾只在完整成功后清理；未选视频/未关联字幕、做种中、识别/转移失败或普通用户目录不会被自动删除。

## Key Decisions

- 数据库的完整 metadata snapshot 是唯一真相，NFO/JPG/STRM 都是可重建产物。
- watcher 负责并行发现和 generation 提交，artifact generation/upload 使用现有持久 Job 队列。
- STRM 模式的 sidecar 只落本地投影；云盘无 STRM 才允许显式开启旁挂上传。
- signed proxy 使用 opaque identity + HMAC，不在 STRM 中持久化 provider URL 或文件身份。
- Emby 302 是 signed proxy 上方的可选协议适配层：它透明代理 Emby，仅对本系统生成的 signed STRM source 改写 PlaybackInfo 和 stream；不把 Emby 协议逻辑放进 cloud driver。
- 清理按 manifest 所有权执行，不以目录扫描猜测“哪些文件是系统生成的”。
- 115 多设备播放采用“原文件 + 一个短命受控副本”的有界双设备模型，而不是无上限创建副本。
- 回收站自动清理默认只使用 OhMyCine 持有的精确项目 ID，永不以空 ID 触发整库清空。

## Risks and Mitigations

- TMDB 图片或详情暂时不可用：保留快照/产物失败状态，独立重试，不生成半成品 manifest。
- 115 风控或 ambiguous success：沿用 limiter/circuit breaker，以目录对账替代盲目重传。
- 签名泄露或 key 轮换：短 scope、expiry、active/previous key、日志脱敏和可撤销 opaque mapping。
- 用户文件被误覆盖/误删：manifest ownership、原子写入、冲突状态、preview + confirmation token。
- generation 风暴：同库合并到最新 generation，运行中的旧 generation 在安全点停止。
- Emby 网关扩大外部暴露面：固定 upstream、保留客户端 Emby auth、短时 ticket、请求限流/大小限制、无跨 origin 凭据跳转，并默认关闭。
- 115 副本创建或清理受到风控：限制为最多一个活动副本，复用现有 limiter/熔断，以持久化 lease 与定时对账恢复。
- 回收站安全码缺失或错误：仅保留 OhMyCine 精确待清理记录并显示错误，不回退为清空整个回收站。
