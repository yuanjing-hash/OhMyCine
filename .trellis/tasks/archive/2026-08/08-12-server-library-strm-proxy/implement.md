# Implementation Plan

## Phase 1: Contracts, Schema and Policy Gates

1. 新增 additive v27 migration：媒体库产物策略/generation 状态、source assets、artifact runs、artifacts、proxy signing keys、Emby connection endpoint/config 和 Emby proxy gateway。
2. 扩展 cloud capability 与受限 `SmallFileUploadDriver` contract；加入 local/cloud/STRM/upload policy resolver，并让通用 Connection provider envelope 可区分 115 Cookie 与 Emby API key。
3. 打通媒体库目录选择 token：cloud + STRM 必须保存并验证 `strm_local_root`；local 不接受 STRM，cloud no-STRM 才允许旁挂上传。
4. 注册细分日志 operation/module，并为 API DTO、OpenAPI 和迁移补测试。

## Phase 2: Full Metadata Snapshot and Deterministic Artifacts

1. 扩展 TMDB client/detail mapping，持久化电影、剧集、季/集所需的完整、安全 snapshot。
2. 让扫描、qBit、115 离线、115 分享和 App 转存统一调用 snapshot commit，不再各自生成刮削产物。
3. 实现 deterministic NFO renderer、图片 identity resolver、大小/MIME 校验与原子文件 writer。
4. 覆盖未识别、详情不完整、图片失败、重试和敏感信息脱敏测试。

## Phase 3: Source Asset Tree and Persistent Artifact Jobs

1. 扫描层在不污染媒体 catalog 的前提下持久化 `srt/ssa/ass/jpg` source facts，并产生 generation diff。
2. 在 generation transaction 完成后创建 `MediaArtifactRun`，向现有 Job 队列提交只含 `artifact_run_id` 的 payload。
3. 实现同库 generation coalescing、superseded 停止、幂等恢复和运行状态/历史 API。
4. 保证所有 library watcher/supervisor 并行，115 生活事件只唤醒权威对账，artifact worker 不阻塞监听。

## Phase 4: Signed Proxy

1. 实现 32-byte key 生成、credential-manager 加密持久化、active/previous key rotation。
2. 实现 HMAC-SHA256 canonical signer/verifier、opaque media mapping、30 天签发和 7 天刷新窗口。
3. 实现仅 GET/HEAD 的 `/proxy/strm/{opaque}`，严格执行先验签、再解析、再请求 DirectURL。
4. 所有持久播放 URL 只使用启动时校验的 `config.PublicOrigin`；在 UI 显示 loopback origin 的跨设备不可达提示，禁止 Host/Forwarded 污染。
5. 实现 connection/file/User-Agent 隔离且带 singleflight 的内存 DirectURL cache，以及无法安全 302 的 provider failure path。
6. 覆盖篡改、过期、错库、错 scope、未知 key、HEAD、Host header 污染、缓存隔离和全链路泄密测试。

## Phase 5: STRM Projection and Source Sidecars

1. 实现 17 种视频扩展、排除词和最小大小 eligibility 到同结构 `.strm` 的 full reconciliation 和 generation diff；ISO 使用 `.iso.strm`，未识别视频仍可投影。
2. 将不可移除的默认 `srt/ssa/ass/jpg` 与媒体库追加扩展按相同结构受限下载到投影根；实现严格扩展校验、数量/长度限制、去重和视频扩展隔离，并加入 limiter、大小上限、临时文件和原子替换。
3. 在 cloud + STRM 投影中按统一 snapshot 生成 NFO/JPG；不触发 provider 上传。
4. 覆盖 create/update/move/delete、签名临近过期刷新、多库并行、重跑幂等和路径边界测试。

## Phase 6: Local Adjacent and 115 Sidecar Upload

1. 实现 local 媒体库 adjacent NFO/JPG，默认开启且尊重 unmanaged conflict。
2. 实现 115 `UploadSmallFile`：只允许 `.nfo/.jpg`，重新验证 parent ancestry，接入 rapid upload/OSS fallback、限流、熔断和目录对账。
3. 实现 cloud no-STRM + upload toggle 的 artifact target；默认 database-only，关闭时绝不上传。
4. 覆盖 ambiguous success、重复执行、风控错误、错误目标和敏感日志测试。

## Phase 7: Emby 302 Gateway

1. 实现 Emby connection 的 endpoint/API key 加密配置、受控连接测试和默认关闭的 gateway 设置，生成稳定不可枚举 public ID。
2. 实现固定 upstream 的 HTTP/WebSocket reverse proxy：客户端 Emby auth 原样转发，不注入 Server API key，不携带凭据跟随跨 origin redirect。
3. 实现 bounded PlaybackInfo inspect/rewrite：只接管已验签的本系统 STRM artifact，强制 DirectPlay，并签发绑定 gateway/item/source/artifact 的 10 分钟 playback ticket。
4. 实现 video/audio/download/file 路由：有效 ticket 调共享 ProxyResolver 返回最终 302，普通请求回源，无效 ticket 安全失败。
5. 以 fake Emby 覆盖登录/API/图片/WebSocket passthrough、GET/POST/HEAD PlaybackInfo、普通媒体、不同 source/ticket 隔离、upstream redirect 和敏感信息脱敏。
6. 将随机 gateway public ID 迁移为管理员可编辑的规范化唯一 alias，加入保留字/字符/长度校验、数据库唯一约束、revision CAS 和旧地址失效测试。
7. 修复 mount prefix 与 upstream base path 拼接，重点覆盖 `/emby/<alias>/emby/users/public`、根路径/脚本/API/图片/WebSocket 和同源 `Location`，确保 Emby Web 可越过 Logo 页。
8. 为 Emby Web 302 直链增加固定 allowlist 兼容补丁：移除 4.9.x `basehtmlplayer.js` 与 `plugin.js` 的远程 DirectPlay `crossOrigin=anonymous`，并在固定 Web HTML 壳优先加载网关同源兜底脚本以覆盖模块缓存；限制正文/编码/缓存，保留 CSP，其它 HTML/静态资源保持透明代理。
9. 以 additive v31 migration 为每个网关增加默认开启的外部播放器/Fanart 策略；扩展 revision CAS API 和播放器管理卡片。固定同源脚本按策略提供安全短票据外部播放器入口，以及无第三方依赖的 Emby 背景图横向图库/全屏预览；普通媒体和无票据 source 不显示外部播放器。

## Phase 8: Cleanup, Web UI and Observability

1. 实现关闭 STRM、切换投影根和失效 artifact 清理的 preview + confirmation token API。
2. 清理只作用于 manifest-owned artifact，不跟随 symlink/reparse point，不删除 unmanaged 文件。
3. 抽取自动/人工共用的 cleanup primitive；完整成功且非 partial 的全量/增量在 artifact generation 应用完成后自动删除 inactive managed local-projection 产物，失败、partial、superseded、根变化或边界异常时跳过并记录安全错误。
4. 更新媒体库 UI：能力驱动的策略开关、投影目录选择、过滤规则、真实状态/错误/重试和清理预览。
5. 新增独立顶级“播放器管理”工作区，以卡片展示 Emby 真实健康状态、安全聚合摘要、连接编辑、302 gateway、外部播放器与 Fanart 开关；数据源页只保留 local/115，不复制底层 Connection 数据模型。不提供任意/用户自定义脚本注入或任意 pin rule；固定播放器资源的 Web 增强属于网关内部协议适配。
6. 更新任务与历史页展示 artifact runs，并修复/验证日志抽屉交互；所有新增模块可筛选。
7. 默认监听 `0.0.0.0:3000`，但持久 URL 仍只使用全局 `OMC_PUBLIC_ORIGIN`；Windows `.runtime/windows/config/server.json` 只承载非敏感 listen/origin 设置，环境变量优先。
8. 将“STRM / 入库”导航调整为“STRM 管理”，实现启用库概览、当前状态/计数/错误、运行历史、managed artifact 分页，以及单库立即增量刷新、全量重建和失败重试。
9. STRM 清理 UI 严格复用 preview + confirmation token；显示自动清理计数/错误，不开放裸重命名，命名修正通过识别/规则后重建。
10. 在媒体库设置加入伴随文件追加扩展编辑器，清楚展示不可移除默认集合与最终有效集合，并覆盖保存/回填/错误反馈。

## Phase 9: 115 Multi-device Playback and Owned Cleanup

1. 新增 additive v30 migration，持久化 115 playback lease/临时副本状态和可选的加密回收站安全码；全部字段保持 API 脱敏。
2. 扩展 115 driver 的精确回收站清理能力，仅接受非空 owned item ID，不暴露无 ID 全量清空入口。
3. 在 signed proxy 中实现最多两个设备的 lease：primary 用原文件，secondary 在唯一 OhMyCine 临时目录复制、有界对账并获取副本直链，third fail-closed。
4. 实现延迟副本删除、精确 recycle purge、启动/定时恢复、指数退避和分模块日志，并覆盖崩溃点、幂等与风控测试。
5. 从 HTTP 直连和 Emby gateway 都传递仅用于路由的客户摘要；确保它不进入 ticket/signature 授权、日志或公开 DTO。
6. 将下载包未选中项固化为 cleanup manifest；只在系统暂存、完整成功且非做种保护状态下清理明确非媒体垃圾，始终保护未选视频和未关联字幕，并在 qBittorrent 整包删除边界重新校验；115 必须收紧到任务的精确输出目录。
7. 在 115 连接编辑中提供可选回收站安全码配置与“只清理 OhMyCine 临时副本”说明，不提供默认全回收站清空开关。

## Phase 10: Documentation and Quality Gate

1. 更新 architecture、security、roadmap、OpenAPI 和 server package specs，记录唯一真相、签名格式、所有权、provider capability 与 Emby gateway 权限边界。
2. 执行 `gofmt`、目标包测试、`go test ./...`、`go vet ./...`、普通与 embedded build、Web UI lint/typecheck/test/build 和 `server/test.ps1`。
3. 检查测试进程已关闭、无凭据/绝对路径泄漏、无未解释的 schema drift，并完成 Trellis quality review。

## Completion Conditions

- PRD acceptance criteria 全部由自动化测试或可重复的 Windows 手工测试步骤覆盖。
- UI 中不存在没有真实 worker 支撑的空开关。
- 所有清理路径均具有 ownership 和边界测试；人工路径另有 preview/confirmation，自动路径只能由完整成功 generation 触发并覆盖 failure/partial/superseded/root-change 跳过测试。
- Emby 官方客户端可通过 gateway 完成 STRM DirectPlay→302；普通 Emby 媒体行为不回归。
- 不实现 Emby/Jellyfin refresh 或 Player 通知消费者，但产物完成事件 contract 已稳定。
