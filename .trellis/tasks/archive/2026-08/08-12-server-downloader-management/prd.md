# Server 下载器管理与任务监控

## Goal

提供独立下载器管理页，统一 qBittorrent/Transmission 和网盘原生离线下载器，并持续展示可靠的任务进度、速度、ETA 与状态。

## Requirements

1. Downloader provider 独立于 MediaLibrary；本地 downloader 只保存加密连接凭据、健康状态和 capability，不选择下载目录或最终媒体库。115 原生离线下载受 provider 输出约束，必须额外绑定一个 115 Storage 和该 Storage 范围内的目标目录。
2. 本地 downloader 输出到 Server 统一下载暂存目录；管理员在系统设置中通过短期目录令牌选择 Server 可见的绝对目录，不依赖 Storage。保存和执行时都重新校验完整路径、symlink 与 Windows Reparse Point。cloud native offline downloader 只能直接输出到所属 cloud Storage。
3. Downloader 只描述下载能力、输出约束与 telemetry，不直接保存最终目标 MediaLibrary；最终编排由 DownloadRule 引用。
4. 持久化任务 owner、provider id、phase/status、bytes total/completed、progress、download/upload speed、ETA、last sampled、error。
5. adapter 按 capability polling/event；WebSocket 推送实时变化，REST 可恢复事实。未知 telemetry 显示 unknown，不伪造 0。
6. 支持暂停/恢复/取消；取消是经二次确认的破坏性操作，必须让 provider 删除任务及已下载/临时数据，成功后同时删除本地 DownloadTask、Job 与依赖记录。
7. 下载器连接测试应返回安全且可操作的失败分类；Web UI 使用全局悬浮通知反馈操作结果，不受当前滚动位置影响并自动消失。
8. 下载器列表使用状态卡片展示启停、健康、版本、地址、最近检查时间和实时任务汇总；连接配置仅在点击编辑后出现。
9. qBittorrent 添加任务必须同时兼容旧版 `200 + Ok.` 和新版 `200/202 + JSON added_torrent_ids`；外部任务可能已经创建但响应确认失败时先按稳定 OMC tag 对账，不能直接终态失败或重复提交。
10. 手动下载选择已有 `MediaClassificationProfile`，默认内置 `default-v1`。Profile ID/revision/rules 在入队时快照；后续规则修改不改变运行任务。
11. 裸 magnet 通过 qBittorrent v5 `stopCondition=MetadataReceived` 获取 metadata 后暂停；旧版 qBittorrent 兼容轮询 metadata 并尽快暂停。Server 读取真实 torrent 名称/文件清单，执行文件名/季集解析、受限 TMDB 匹配和现有 `classification.Classify`，将结果映射为 qBittorrent category 后再恢复正式下载。
12. TMDB v4 API Read Access Token 与 v3 API Key 均可通过系统设置显式选择、加密保存并安全测试；无凭据、认证/网络失败、无结果、歧义、低置信度或 Profile fallback 时自动指派 qBittorrent `未识别` category 并继续下载，不创建阻塞队列的分类确认操作。
13. 下载完成后以真实 provider 文件清单复核同一 Profile 分类并持久化安全刮削摘要；本轮不实现跨媒体库转移，但为后续 ImportPlan/Transfer 保留明确完成边界。
14. Server TMDB 凭据体验与 Player 对齐：正式构建通过 GitHub Secret `OHMYCINE_TMDB_READ_ACCESS_TOKEN` 注入只读应用凭据，源码、普通配置、日志和产物说明均不包含明文；用户加密保存的自定义 Token 优先，清除后自动恢复内置通道。自编译未注入且用户未配置时明确显示不可用。
15. 默认 TMDB API 优先 `https://api.tmdb.org/3`，只在网络错误/超时时回退 `https://api.themoviedb.org/3`，401/403 等凭据错误不回退。管理员可分别配置 HTTPS API 与图片代理前缀；地址不得含 userinfo/query/fragment，每项必须用真实受控请求测试成功后独立保存并立即生效，失败保留旧值。自定义 API 只访问该地址且不跨域回退。
16. TMDB 凭据类型不得依赖内容猜测：Read Access Token 使用 Bearer，API Key 使用 `api_key` query。custom/deployment/builtin 均返回明确的安全 kind；v11 前只保存 Token 的记录升级后继续按 `read_access_token` 使用。两类凭据均不得进入日志、错误、任务 payload 或普通 API 响应。

## MVP Scope

1. 实现 fake、qBittorrent 与 115 原生离线下载 provider；Transmission 和其它下载器保留 adapter/capability 扩展位。
2. qBittorrent 使用系统统一下载暂存目录；未配置或目录不可用时可保存/测试连接，但不能提交下载任务。远程 qBittorrent 路径映射留到下载规则/传输任务细化。
3. 首版提交支持 magnet、HTTP(S) URL 和上传 `.torrent`。下载源/种子可能包含 PT passkey，必须有严格大小/类型限制并加密保存，不能进入公开临时目录、通用 Job payload、日志、审计元数据或普通 API 响应。
4. 下载管理页负责下载器 CRUD、连接测试、手动提交和下载状态摘要；全局排队顺序与通用控制仍由任务中心负责。
5. 暂停/恢复/取消必须同步到 provider；取消必须以 `deleteData=true` 删除 provider 任务及下载数据，确认成功或 provider 明确报告任务不存在后，事务清理本地任务事实。failed/cancelled 任务提供独立“删除”入口；provider 删除失败时保留本地记录供重试。
6. qBittorrent 原生 category 的读取、创建、指派与文件清单属于首版基础闭环；115 原生离线下载复用已有数据源 Cookie，并可从该数据源根下自由选择云端输出目录；Transmission 和其它 provider routing 后续实现。

## Acceptance Criteria

- [x] 可添加并测试 fake/qBit downloader，提交任务并持续看到百分比、速度和 ETA。
- [x] 115 原生离线下载器声明 provider Storage 输出约束并复用所属数据源凭据；任务不能绕过已选 Storage 或目录边界。
- [x] Server 重启后任务状态可从 DB + provider reconciliation 恢复。
- [x] 下载器密码和下载源在 SQLite 中为 AES-GCM 密文，API/日志/审计/Job payload 均不返回明文。
- [x] 下载管理页面在白色/深色主题下可完成 CRUD、测试、提交与状态查看，并正确显示未知 telemetry。
- [x] 系统设置可通过 Server 目录选择器保存统一下载暂存目录，下载器配置不再选择 Storage/媒体库，所有本地下载任务使用该目录。
- [x] 下载器以状态卡片展示实时任务汇总，点击编辑才显示连接表单；连接测试通过自动消失的全局悬浮通知给出可操作反馈。
- [x] qBittorrent v4/v5 旧 `Ok.` 与 v5.2 JSON 添加响应均能成功绑定 provider task；响应歧义或旧失败记录重试时按 OMC tag 接管既有任务，不重复添加。
- [x] 系统设置可显式选择并加密保存/清除/测试 TMDB Read Access Token 或 API Key；API、日志、审计、任务 payload 和 WebSocket 均不返回凭据。
- [x] 裸 magnet 显示“获取 metadata → 轻量刮削 → 已指派分类 → 正式下载”阶段；高置信匹配自动设置 qBittorrent category，其余情况自动归入 `未识别` 并继续，不占用人工确认队列。
- [x] 预分类和下载完成复核复用所选 MediaClassificationProfile 的同一 matcher；页面展示安全标题、媒体类型、category、匹配状态和 TMDB ID，不显示源 URL、绝对路径或 provider 原始响应。
- [x] retry/paused/queued/failed 等非运行态下载取消通过持久化 provider-control intent 恢复；只有 qBittorrent `Cancel(deleteData=true)` 成功或明确 task-not-found 后才清理本地任务，失败保留原任务且不残留旧确认操作。
- [x] failed/cancelled 下载可经 `DELETE /api/v1/downloads/:id` 删除；先确认 provider 数据删除，再事务删除 DownloadTask、Job、attempt/event/action 依赖并记录脱敏审计。
- [x] 官方 Server 构建在没有用户 Token 时报告“内置通道可用”，用户 Token 保存后报告“自定义凭据”，清除后恢复内置通道；本地未注入构建明确报告无有效凭据。
- [x] 默认 API 按短域名→旧域名只对网络故障回退；自定义 API/图片地址分别测试并原子启用，失败不覆盖现有地址，凭据和含鉴权信息的请求 URL 不进入输出。
- [x] 旧 TMDB Token 密文经 v11 迁移保持原文并默认为 `read_access_token`；新 API Key 通过 `api_key` query 发送，Read Access Token 继续通过 Bearer 发送。
- [x] 添加或编辑 115 原生离线下载器时可通过共享目录树进入数据源任意下级目录；Server 保存稳定目录 ID 和相对显示路径，提交任务使用所选目录 ID，不能退化为只能选择 Storage 根目录。
