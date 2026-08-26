# Design: Unified Media Artifacts, STRM and Signed Redirect

## Architecture

```text
scanner / qBit / 115 offline / 115 share / 115 App transfer
                         │
                         ▼
       source facts + recognition + full TMDB snapshot
                 (single database truth)
                         │ generation commit
                         ▼
               persistent artifact Job
                         │
        ┌────────────────┼──────────────────┐
        ▼                ▼                  ▼
local adjacent     cloud + STRM       cloud, no STRM
NFO/JPG            STRM/NFO/JPG       database-only or
                   local projection    explicit sidecar upload
```

`LibrarySupervisor` 和 provider 事件监听器只负责并行发现变化、合并 generation，不等待磁盘产物或云上传。generation 的 source facts、识别结果和 metadata snapshot 提交后，创建 `MediaArtifactRun`，再向现有持久 Job 队列提交只含 `artifact_run_id` 的任务。

## MoviePilot Plugin Research Baseline

本设计已对照 `DDSRem-Dev/MoviePilot-Plugins` commit `ffcb5e9cdec2045ea0edfd256a2faaa9a4b354c1`（2026-08-19），重点检查 `p115strmhelper` 的 full/increment/life STRM、`Redirect` 直链解析与并发缓存，以及 `embyreverseproxy` 的 PlaybackInfo/stream 改写。

采用并改写的成熟思路：

- 全量用 provider 树与本地投影树收敛，增量用事件唤醒并最终对账；create/move/rename/delete 都必须有对应投影语义。
- 视频扩展、最小大小、排除词先过滤；ISO 使用 `.iso.strm`；字幕/图片等媒体伴随文件独立下载。
- 直链按文件身份 + User-Agent 隔离缓存，并用 singleflight/每 key 锁抑制并发击穿，TTL 早于 provider expiry。
- 生成失败时跳过清理；完整成功的全量/增量在新 manifest 应用后自动清理旧 generation 的失效托管产物，并分别输出细分计数日志；partial/异常结果进入人工预览。
- Emby 网关拦截 PlaybackInfo，远程 STRM 强制 DirectPlay，再拦截 stream/download/file 路由返回 302。

明确不照搬的部分：

- 插件把 pickcode、share code、路径或 receive code 直接写进 STRM URL，且可接受 GET/POST；OhMyCine 只写 opaque signed GET/HEAD URL。
- 插件会在日志/响应中出现 pickcode、路径和最终 URL；OhMyCine 全链路脱敏。
- 插件用本地树猜测清理对象；OhMyCine 以 manifest ownership 为删除边界，并保留 preview + confirmation。
- 插件的 Emby 代理使用客户端 IP/User-Agent 做 PlaybackInfo→stream 关联，并通过修改 Emby Web 播放器的 `crossOrigin` 行为解决 302 CDN 无 CORS Header 的问题。OhMyCine 使用短时 signed playback ticket，不放宽管理 API CORS；仅对固定播放器资源与固定 Web HTML 壳应用确定性、限长、不可配置的兼容补丁，不提供通用脚本注入。
- 插件可无界复制并延迟删除 115 文件来规避多端播放限制；OhMyCine 只允许共享 115 playback coordinator 在专属目录创建一个持久化持有的短命副本，并按同一精确 item ID 清理，普通代理与 Emby 网关没有任意云端文件修改权。

## Schema v27

在 `media_libraries` 追加：

- `signed_proxy_enabled`
- `metadata_artifacts_enabled`：local 默认 `true`，cloud 默认按 STRM 策略解释
- `upload_sidecars`：cloud no-STRM 默认 `false`
- 复用现有 `strm_enabled`、`strm_local_root`
- `artifact_generation`、`artifact_applied_generation`
- `artifact_status`、`artifact_error`、`artifact_updated_at`
- `strm_asset_extra_extensions`：只保存用户追加的标准化小写扩展；默认 `srt/ssa/ass/jpg` 由程序常量提供且不可移除，迁移默认回填空数组。

新增 `media_library_source_assets`：保存 `library_id`、generation、provider opaque identity、parent identity、relative path、name、extension、size、mtime/hash hint 和 active 状态。它只记录允许同步的 `srt/ssa/ass/jpg`，不进入 `media_library_entries`。

新增 `media_artifact_runs`：记录 library/generation、policy snapshot、状态、计数、错误摘要、开始/结束时间和重试信息。唯一约束保证同库同 generation 只有一个 run。

新增 `media_artifacts`：记录 run/library/source identity、artifact kind、target kind、relative path、content fingerprint、managed ownership、状态和 provider/local target identity。清理与幂等判断只依赖该表，不把普通同名文件认作 managed。

新增 `proxy_signing_keys`：只保存 key ID、加密后的 secret、active/previous 状态、创建/停用时间。32-byte 随机 secret 通过现有 credential manager 加密落库。

扩展通用 `connections` provider contract 以支持 `emby`：保存经过验证的 endpoint、加密 API key 和脱敏健康状态；115 Cookie envelope 保持兼容。新增 `emby_proxy_gateways`，一对一引用 Emby connection，保存 enabled、规范化且唯一的用户 gateway alias、外部播放器/Fanart 开关、policy revision 与健康状态，不保存客户端 Emby Token。首次启用可由 Server 生成短且可编辑的安全默认 alias；alias 不是认证秘密。策略变更统一推进 revision，使已经签发的旧播放票据立即失效。

迁移必须 additive，并为既有 library 回填安全默认值；不因升级立即生成或上传产物。

## Metadata Snapshot and Artifact Rendering

扩展 metadata provider 的详情模型，落地结构化 snapshot：标题、日期、简介、外部 ID、类型、评分、时长、国家、语言、主创、演员及图片 identity。provider 原始 payload、绝对路径和临时图片 URL 不持久化。

NFO renderer 是纯函数：`snapshot + media identity + naming policy -> bytes + expected artifacts`。图片 resolver 根据 snapshot 的稳定 image identity 在任务执行时获取内容，经过 MIME、尺寸和大小限制后原子写入。未匹配或 snapshot 不完整时产物任务返回 `unrecognized`/`metadata_incomplete`，不猜测标题。

命名约定：

- movie：`basename.nfo`、`basename-poster.jpg`、`basename-fanart.jpg`
- show：`tvshow.nfo`、`poster.jpg`、`fanart.jpg`
- season：`seasonNN-poster.jpg`
- episode：`basename.nfo`、`basename-thumb.jpg`
- movie transfer：从原文件名提取受控的 edition、分辨率、片源、REMUX、HDR 和 Dolby Vision 标记，按 Emby `片名 (年份) - Version.ext` 约定保留多版本；规则可使用 `{version}`，不得把完整发布名、站点广告或 release group 原样带入目标。

## Driver Contracts

现有 `Driver`/`DirectURL` contract 继续负责读取。旁挂上传采用显式窄接口，避免把任意文件写能力暴露给所有流程：

```go
type SmallFileUploadDriver interface {
    Driver
    UploadSmallFile(
        ctx context.Context,
        parentID, name, contentType string,
        size int64,
        src io.ReadSeeker,
    ) (Item, error)
}
```

公共服务层先限制扩展名、MIME、大小和目标 ancestry；driver 再做 provider 校验。115 adapter 使用 `RapidUploadOrByOSS`，接入现有 limiter、熔断和错误分类；响应不确定时重新列出目标目录，以文件名/大小/校验提示确认结果。

## STRM Projection

视频集合固定为 `mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,flv,m2ts,tp,f4v`，不由普通设置扩张。普通视频输出同相对目录、同 basename 的 `.strm`，ISO 输出 `.iso.strm`。先应用媒体库所选分类规则的排除词与最小大小策略；未识别视频仍生成 STRM，NFO/JPG 则等待可靠 snapshot。源 `srt,ssa,ass,jpg` 加每库 `strm_asset_extra_extensions` 的并集作为 source asset facts，保持路径与文件名同步；统一生成的 NFO/JPG 按命名约定写入相应目录。

扩展名 validator 只接受 1-10 个 ASCII 小写字母或数字（输入可含前导点但保存时移除），总追加数量设硬上限并做大小写无关去重；拒绝视频扩展、默认扩展、路径分隔符、通配符、空白和控制字符。配置扩张只影响下一次 scan/reconcile 的 source asset eligibility，不绕过 provider 下载大小限制、限速、原子写入、投影根校验或 manifest ownership。

投影根必须来自媒体库显式配置并在每次运行时重新验证：绝对目录、可写、非 reparse point，且所有目标经 `filepath.Rel` 后仍在根内。所有写入使用临时文件 + flush/close + rename；manifest commit 只在文件成功后发生。

同库新 generation 到达时合并未开始任务；运行中的旧任务在文件边界检查 superseded 状态并停止。全量 reconciliation 从当前 source facts 和 metadata snapshot 推导 expected manifest；增量只改变受影响条目，但结果必须与全量一致。provider 生活事件只负责缩小待对账范围并唤醒 generation，不能直接把不完整事件当作删除或移动的唯一真相。

## Signed 302

路由：

```text
GET|HEAD /proxy/strm/{opaque}?kid=<id>&exp=<unix>&sig=<base64url>
```

签名 canonical input 包含版本、`media-read` method class、opaque token、library scope、key ID 和 expiry。GET/HEAD 映射为同一 method class；其它方法在验签前拒绝。比较使用 constant-time HMAC 校验，expiry 有严格上限和小幅时钟偏差容忍。

opaque token 只映射到 Server 内部 artifact/media identity，不可反推出 connection ID、provider file ID 或相对路径。请求流程为：解析并规范化参数 → 查 key → 验签/过期/scope → 查 active library/media mapping → capability check → `DirectURL` → 302。

DirectURL 仅内存缓存，key 至少包含 connection、opaque media identity 和 User-Agent hash，TTL 取 Server 上限与 provider expiry 的较小值。相同 key 使用 singleflight 抑制并发击穿；缓存值不进入日志、数据库或 WebSocket。115 adapter 只把获取直链时使用的同一个 User-Agent 作为播放约束；`115driver` 返回的 Cookie、Content-Type、Referer 等直链接口请求头属于获取阶段元数据，必须在 adapter 边界丢弃，不能误判为 CDN 请求头或泄露给客户端。resolver 仍拒绝 User-Agent 不匹配以及真正依赖 Cookie、Authorization、Referer 或其它 302 无法表达 header 的 provider。

默认签名有效 30 天；artifact worker 在剩余 7 天内重写 `.strm`。active key 签发，active/previous key 验证，轮换后逐步刷新现有投影。

STRM absolute URL 和 Emby gateway base URL 只使用启动时已验证的 `config.PublicOrigin`（来源为 `OMC_PUBLIC_ORIGIN`），绝不信任请求 `Host`、`X-Forwarded-Host` 或 `Forwarded` 生成持久 URL。loopback origin 合法但只能供本机使用，Web UI 必须显示跨设备不可达提示。

Server 默认把 listen address 与 advertised origin 分离：监听 `0.0.0.0:3000`，默认 `PublicOrigin` 仍为 `http://127.0.0.1:3000`。Windows 启动器可读取 Git 忽略的 `.runtime/windows/config/server.json`，但只允许 `listen_host`、`port`、`public_origin`，且 `OMC_*` 环境变量优先；配置文件和 `PublicOrigin` 都拒绝凭据字段与 wildcard origin。

Web UI 以独立“播放器管理”路由呈现 Emby。卡片读取固定 `/System/Info`、`/Library/VirtualFolders`、`/Items/Counts` 后生成的安全聚合 DTO，只展示服务器名、版本和数量；可选统计失败显示 unknown/partial，不回显库名、item、路径、用户、session 或原始 payload。数据源页只呈现 local/115，底层仍复用 `connections` 记录。

## Emby 302 Gateway

网关基址为：

```text
https://<public-origin>/emby/<gateway-alias>
```

`gateway-alias` 是管理员可编辑的稳定路径名但不是授权凭据；Emby 自身的用户认证仍是访问控制。alias 统一为小写，限制为 URL-safe 字母数字与单个连字符、固定长度区间，拒绝首尾连字符、连续连字符、路由保留字和大小写冲突，并以数据库唯一索引防止竞态；更新必须携带当前 policy revision，成功后推进 revision 并使旧 alias 立即失效。网关默认关闭，只绑定一个管理员已创建并测试的 Emby connection。所有 upstream URL 都由该 connection 固定决定，请求路径不得构造新 scheme/host，outbound client 不携带凭据跟随跨 origin redirect。

网关路由解析把 `/emby/<alias>` 视为唯一外层 mount prefix，剩余路径保持原样转发。因此 Emby Web 发出的 `/emby/<alias>/emby/users/public` 必须命中 upstream `/emby/users/public`，而不能再次把 upstream 的 `/emby` 当作 gateway prefix。endpoint 本身允许固定的安全 base path；路径拼接使用已解析 URL 与规范化 escaped path，不能用字符串 TrimPrefix/Join 产生双重剥离或吞掉 `/emby`。同源 `Location`、HTML base path、WebSocket upgrade 和 query 均保留 gateway mount 语义，并用 fake Emby 覆盖根路径及 `/emby` base path。

普通 HTTP API、图片与 WebSocket 透明代理；过滤 hop-by-hop headers，保留必要 Emby auth/device headers。网关不把 connection 中的服务 API key 注入客户端请求。上游 `Location` 若仍指向同一 Emby endpoint，可改写回 gateway base；跨 origin Location 原样返回客户端且不由 Server 代为携带凭据访问。

PlaybackInfo 流程：

```text
client → POST/GET Items/{item}/PlaybackInfo through gateway
       → forward with the client's Emby auth
       → inspect bounded JSON MediaSources
       → verify source is this Server's active signed STRM artifact
       → set DirectPlay, remove transcoding fields
       → replace Path/DirectStreamUrl with Emby API-relative stream URL
          carrying short-lived opaque playback ticket
```

这里的 API-relative 明确指 `/videos/{item}/stream?...` 或 `/audio/{item}/stream?...`。Emby Web 会在已配置的 `/emby/<alias>` Server base 后自行追加 `/emby` API prefix；返回值不得再次包含外层 gateway mount，否则会形成重复路径并使 ticket route binding 失败。

仅以 `IsRemote/Protocol=Http` 不足以触发改写，必须验证 URL origin/path、HMAC、expiry、library/artifact active 状态。这样普通公网视频源、其它插件 URL 和本地媒体不会被误接管。

playback ticket 使用 HMAC-SHA256，绑定 gateway public ID、Emby item ID、MediaSource ID、artifact opaque identity、`media-read` scope 与短 expiry（默认 10 分钟）。它不包含 Emby credential、原 signed STRM URL 或 CDN URL，也不依赖 IP/UA 猜测关联。ticket 只授权这一次 source resolution；GET/HEAD stream 都映射到同一 method class。

带有效 ticket 的 `/videos`、`/audio`、`/items/*/download|file` 请求直接调用共享 `ProxyResolver`，按当前播放器 User-Agent 获取 provider DirectURL 并返回最终 302。ticket 缺失/过期/错 gateway/item/source 时不得降级成另一个 artifact；缺失 ticket 的普通媒体请求透明回源 Emby，无效 ticket 返回安全 401/403。

浏览器中的 `<video crossorigin="anonymous">` 会要求 302 链最终的 115 CDN 响应提供 CORS Header；网关无法替第三方 CDN 添加该响应头。参考 MediaWarp 与 MoviePilot `EmbyReverseProxy`，网关对 `/web/modules/htmlvideoplayer/basehtmlplayer.js` 和 `plugin.js` 两个固定资源移除远程 DirectPlay 的 `crossOrigin` 赋值。播放器模块可能已经被 Service Worker/Cache Storage 复用而完全不再请求，因此网关还只对 `/web/index.html`、`/web`、`/web/` 三个固定 HTML 壳在 `<head>` 起始处注入一个同步、同源的 `/web/ohmycine-directplay.js`；该脚本在应用启动前锁定 `HTMLMediaElement.prototype.crossOrigin` 并用 MutationObserver 清除后续属性。脚本内容不可配置、不含用户数据或凭据，CSP 原样保留，固定脚本路径由网关本地响应。所有补丁请求固定使用 identity encoding、删除条件缓存头，响应限长、重算 Content-Length、清除 ETag/Last-Modified/SourceMap 并设为 no-store，避免旧 304 或压缩正文绕过。其它 HTML/脚本完全透明转发；模式缺失只记录稳定脱敏错误，不做宽泛路径或任意注入。

网关不提供任意 Emby Web UI JavaScript 注入、不开放任意 pin rule、不使用服务 API key 提升用户权限。固定同源脚本可以按网关策略启用两个内建模块：外部播放器模块通过当前 Emby 会话请求 PlaybackInfo，只接受带唯一短时 `omc_ticket` 的同源网关 stream URL，并根据 UA 提供 PotPlayer/VLC/MPV/IINA/Infuse/MX Player/弹弹Play 入口；Fanart 模块只读取当前详情 item 的 `BackdropImageTags`，通过 `ApiClient.getImageUrl` 显示最多 30 张图。两者不引入第三方 CDN、不记录 URL/ticket，也不处理普通非 OhMyCine STRM source。115 并发播放只委托给共享 signed-proxy playback coordinator 的有界双设备 lease；网关本身不接收 provider item ID，也没有任意复制/删除能力。

## Policy Resolution

| Library kind | STRM | NFO/JPG target | Upload |
|---|---:|---|---:|
| local | no | media adjacent when enabled | no |
| cloud | yes | local projection root | no |
| cloud | no | database-only by default | optional provider adjacent |

cloud + STRM 必须同时满足 stable identity、temporary direct URL、signed proxy 和有效本地投影根。`upload_sidecars` 只在 `SmallFileUploadDriver` 可用且 STRM 关闭时有效；后端始终重新验证，不能只依赖 UI 隐藏。

## 115 Bounded Multi-device Playback

115 signed 302 在鉴权之后、DirectURL 解析之前为同一 artifact 维护有界 playback lease。客户 IP 与 User-Agent 只在内存边界标准化后生成不可逆摘要；摘要用于并发路由而不参与授权，不保存原始 IP/UA。

同一 artifact 的第一个活动摘要获得 primary lease，使用原文件 pickcode。第二个获得 secondary lease，在账号根下的 `/OhMyCine/.playback-copies/<lease>` 创建唯一目录，复制原文件，对副本进行有界的 `0.5s/1s/2s` 列表对账，再用副本 pickcode 解析直链。第三个同时摘要安全失败，不继续复制。

lease 和副本事实持久化，保存 connection/artifact/source/copy-directory/copy-item 的内部身份、role、状态、过期与 cleanup-after，不保存 pickcode、直链或客户原始标识。直链返回后异步将唯一副本目录送入回收站，再通过可选的加密回收站安全码按精确 ID 永久清理。启动恢复和定时 sweeper 只查询这些持有事实；空 ID 和“全部清空”调用不在自动路径中。

## Cleanup and Conflict Handling

如果目标路径存在但没有匹配的 managed artifact 记录，worker 不覆盖并记录 `unmanaged_conflict`。如果已管理文件 fingerprint 不同，使用原子替换并更新 manifest。

完整且非 partial 的全量/增量扫描先提交新 generation，artifact worker 成功应用 expected manifest 后，再调用共享 cleanup primitive 自动删除旧 generation 中 inactive、managed、local-projection 产物。自动清理开始前和每个删除边界都重新验证当前 library generation、artifact run 完成状态、投影根 canonical identity、manifest ownership 与 symlink/reparse；任一条件变化即停止并保留记录，不把产物成功回滚为扫描失败。

关闭 STRM、变更投影根、失败/partial generation 或人工清理失效产物时仍先返回 preview（数量、类型、相对路径摘要和不可删除冲突），并签发绑定 library、generation、operation 和 expiry 的 confirmation token。执行阶段复用同一 cleanup primitive，只删除 manifest-owned 文件；不跟随 symlink/reparse point，不递归删除非空的用户目录。自动与人工清理都记录结构化模块日志和审计摘要，不能记录绝对路径。

下载包清理使用与安全入库选择同源的 cleanup manifest，不重新猜测文件。只有 OhMyCine 独占的暂存根且识别、transfer、目标对账均完整成功时，才删除严格差集中的明确非媒体垃圾并清理空目录；未选视频与未关联字幕始终作为受保护残留保留。115 逐项清理必须落在下载任务持久化的精确输出目录内，不能只证明位于更宽的 Storage 根。qBittorrent copy/symlink 任务将清理延后到做种策略完成；存在受保护残留时 copy 也必须关闭整包 `deleteData`，并在真正删除边界重新校验。普通用户目录、已有媒体源、partial manifest 和任一失败路径均只保留并记录原因。

## STRM Management Workspace

`/automation/strm` 是 STRM 的唯一管理工作区，列表 API 聚合启用 STRM 的媒体库、最新 run、当前 generation、计数和错误摘要；历史与 artifact 列表使用稳定分页和权限过滤。立即刷新提交增量 reconcile，全量重建提交 full reconcile，重试只针对可重试的失败 run，均复用持久 Job 队列并以 library/generation 去重。

清理 API 分为 preview 和 execute：preview 返回受控计数与相对路径摘要并签发绑定 actor、library、generation、operation、manifest snapshot 和 expiry 的 confirmation token；execute 重新检查权限、revision、根边界、ownership 与 symlink/reparse 状态。自动清理由已完成的 artifact worker 以 system actor 调用同一底层删除器，不接受客户端 token，也不能扩大候选集合。页面展示最近自动清理计数/错误。页面不提供任意裸重命名；命名修改通过 metadata override/规则模板生成新 expected manifest，再由 reconcile 和受控清理完成迁移。

## Logging and Events

至少提供可筛选模块：`元数据识别`、`元数据快照`、`媒体产物`、`STRM生成`、`增量STRM生成`、`302代理`、`Emby302网关`、`115多设备播放`、`115临时副本清理`、`115旁挂上传`、`下载暂存清理`、`产物清理`。日志包含 library/run/generation/gateway/lease 的内部关联 ID 和计数，不包含 cookie、Authorization、Emby/TMDB key、MediaSource.Path、playback ticket、设备 IP/UA、OSS 信息、上游 URL、provider file ID 或绝对本地路径。

artifact run 完成后发布结构化完成事件，供后续 Emby/Jellyfin refresh 和 Player 通知消费，但本任务不实现这些消费者。

## Failure Semantics

- 识别/TMDB 失败：保持未识别或 metadata incomplete，不生成 sidecar。
- 图片失败：NFO 与图片分别记录状态，run 可重试且不会覆盖 unmanaged 文件。
- 115 限流/熔断：任务进入可恢复失败或延迟重试，watcher 不受阻塞。
- signed proxy 失败：统一安全错误，不回显 provider 原因或敏感值。
- Emby upstream 不可用：普通代理返回安全 502；PlaybackInfo 不做猜测改写，已签发 ticket 解析失败时不回退到其它 source。
- 任务 superseded：安全停止并由最新 generation 收敛，状态不计为用户错误。
