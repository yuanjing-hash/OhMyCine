# Server 媒体库配置与扫描基础

## Goal

允许用户基于一个 Storage 创建一个 MediaLibrary，选择可复用分类 Profile，并配置独立递归/全量/增量扫描与刮削策略。

## Background

- 本地 Storage、Server 目录选择器和 `MediaClassificationProfile` 已实现，当前任务直接消费这些稳定边界。
- 当前 Server 尚无 MediaLibrary/ScanRun/MediaEntry/FileTreeSnapshot 表，也尚未引入 filesystem watcher 或 cron 依赖。
- 本任务优先于持久任务队列实施，因为每个媒体库 supervisor、事件监听和文件树 reconciliation 是常驻并行控制面，不进入下载/上传/刮削/传输 Job 队列。
- 首个真实验收来源是用户已经通过管理端登记、仅存在于本机 SQLite 的本地 Storage；真实绝对路径继续不得进入 Git 跟踪文件。

## Requirements

1. 一个 MediaLibrary 只能引用一个 Storage、一个受控相对根和一个 MediaClassificationProfile。
2. 相对根 `/` 表示 Storage 根；规范化后必须严格位于 Storage 边界内，默认禁止媒体库扫描范围重叠。
3. 库可包含 movie/tv 混合内容；扫描先识别类型，再应用 Profile 对应规则组。
4. 每库保存递归、扫描模式、全量计划、增量间隔、视频扩展名、忽略规则、TMDB 语言/地区与匹配策略。监听实现由 Storage driver 自动选择，用户不需要手工指定 watcher 类型。
5. 每库分别保存 provider scan/list 限速与并发、metadata scrape 限速与并发。云盘采用保守默认值并允许用户在 provider 安全上限内调整；限流等待必须可观测、可取消并在 scan run 中记录。
6. 每个启用的 MediaLibrary 启动独立常驻 supervisor，所有库同时监听且不进入全局持久任务队列。本地库使用 filesystem watcher 低延迟响应新建/修改/删除/重命名；远程库由 driver 按能力自动选择 provider event、change cursor 或有界 polling，115 优先使用“生活事件”。任何一个库的监听/重连不得占用或阻塞其它库、下载、上传、刮削的 worker slot。
7. MediaLibrary 根据 Storage capability 决定是否显示 signed 302/STRM 开关；local 不显示，cloud 只有满足稳定身份 + direct URL/signed redirect 条件才允许开启。
8. 创建/编辑媒体库时先选择来源 Storage 和来源相对根。仅当来源为符合能力要求的 cloud Storage 且勾选 STRM 时，显示并强制通过本地目录选择器选择本地 STRM 目录；后端将其作为该库专用 managed output root 保存到本机 SQLite，不要求预先创建第二个 local Storage。未勾选时不要求、不保存该目录。本地来源不能开启 STRM。
9. 创建并启用媒体库后自动进入 `initializing`，立即执行首次全量扫描并建立文件树基线，不要求用户额外点击首次扫描。首次扫描全程只读来源 Storage；成功提交基线后才启动该库的常驻监听，并立即补一次增量 reconciliation，覆盖全量扫描结束到 watcher/event source 挂接之间的竞态窗口。创建但未启用的媒体库只保存配置，启用时再自动初始化。
10. 扫描记录原始相对路径、文件身份、解析结果、匹配状态、分类、错误、限流状态与日志；绝对路径不作为展示/导出/AI字段。
11. 媒体库页可选择 Storage 和 Profile，展示扫描状态/日志/媒体清单；规则编辑跳转独立规则管理页。
12. Profile 更新使引用库标记为“分类规则已更新/待重分类”，不自动进行文件操作。
13. 每库维护 provider-relative 文件树快照、driver cursor、dirty generation 和 reconciliation 结果。首次/周期全量重建树；可靠事件直接应用变化；polling、游标失效或事件不确定时对比新旧树识别 create/update/move/delete。单库内 single-flight 合并事件并追赶 generation，不同库互不排队。
14. 所有库保留定时增量与周期全量作为最终一致性保障；provider 请求仍遵守每库/每 provider 限速，但不得用“全局扫描并发数”限制同时监听的媒体库数量。
15. cloud mount 只作为 Storage 访问/上传/传输能力存在，不是媒体库本地投影，也不能替代 STRM 开关及其必填本地目录。
16. 首次全量扫描失败时保留媒体库配置并进入 `initialization_failed`，不启动来源监听；该库按有界指数退避自动重试，同时提供“立即重试”操作。单库初始化失败、重试等待或取消不得阻塞其它媒体库 supervisor；用户停用或删除媒体库时取消其初始化/重试并停止监听。

## Acceptance Criteria

- [ ] 可创建 `本地媒体库1`，选择 `115 下载盘`、相对根 `/` 和默认 Profile。
- [ ] 显式扫描只读发现当前 4 个 MP4，并正确保留电影/剧集候选与相对路径。
- [ ] 创建并启用本地媒体库后无需额外操作即可自动完成首次全量扫描；成功后状态进入监听中，并通过紧随其后的增量对账覆盖扫描/监听交接窗口。
- [ ] 全量/增量配置独立于其它媒体库，扫描失败不影响 Storage 浏览/探测。
- [ ] 自定义 Profile 可在媒体库页面直接选择；共享更新后库进入待重分类状态。
- [ ] 真实 4 个 MP4 的内容、名称、目录与时间戳不被 Server 修改。
- [ ] 两个 fake MediaLibrary 可同时监听；一个库限流、断线重连或全量 reconciliation 时另一个库仍可立即处理事件，且全局 Job 队列中没有监听/文件树 reconciliation job。
- [ ] 本地 create/modify/move/delete 与 fake 115 生活事件都归一化进文件树；事件丢失或 cursor 失效后，下一次增量/全量 diff 可修正最终状态。
- [ ] cloud + STRM 表单要求直接选择有效本地目录且无需第二个 local Storage；cloud 不启用 STRM 和 local source 两种情况都不要求本地目录且不创建投影。
- [ ] 首次扫描失败的库显示稳定错误、下次自动重试时间和“立即重试”；失败库不监听且不阻塞另一个库完成初始化和处理事件。

## Out of Scope

- TMDB 凭据/网络刮削的完整实现可作为下一子切片；本任务先保留明确 match states 和接口。
- 海报下载、NFO 写回、文件整理、STRM/302、Emby/Jellyfin refresh。
