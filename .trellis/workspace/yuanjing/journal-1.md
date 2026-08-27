# Journal - yuanjing (Part 1)

> AI development session journal
> Started: 2026-05-07

---



## Session 1: 完成 Trellis 迁移与 Player 验证收尾

**Date**: 2026-05-07
**Task**: 完成 Trellis 迁移与 Player 验证收尾
**Branch**: `main`

### Summary

迁移设计规范到 Trellis，接管并验证 Player 当前实现，修复 libmpv/Tauri Windows GNU 交叉构建，补充基础设施与 Hub 骨架，并归档相关 Trellis 任务。

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `4efa50f` | (see git log) |
| `0a09bf1` | (see git log) |
| `a960567` | (see git log) |
| `c998e63` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**


### Next Steps

- None - task complete


## Session 2: 收尾 Player Emby MVP 与 roadmap 规划

**Date**: 2026-05-08
**Task**: 收尾 Player Emby MVP 与 roadmap 规划
**Branch**: `main`

### Summary

跑完 Player typecheck/lint/build、cargo check、Windows GNU 包构建，对照 Emby MVP Acceptance Criteria 全数通过；归档 05-07-player-embedded-video-rendering 与 05-07-player-roadmap 两个任务。

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `90b47fe` | (see git log) |
| `cef481e` | (see git log) |
| `858b9b0` | (see git log) |
| `7e7b59d` | (see git log) |
| `96711ce` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 3: 完成 Player Windows 嵌入式渲染

**Date**: 2026-05-12
**Task**: 完成 Player Windows 嵌入式渲染
**Branch**: `main`

### Summary

完成 Windows 透明 Tauri/WebView overlay + mpv owned top-level HWND underlay 的嵌入式视频渲染，更新规格与路线状态，并归档 Player 渲染相关任务。

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `13199b5` | (see git log) |
| `cdea94d` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 4: 完成 Player 播放设置面板

**Date**: 2026-05-13
**Task**: 完成 Player 播放设置面板
**Branch**: `main`

### Summary

完成播放页底部控制条与画面设置面板：底栏承载倍速、字幕、音轨、队列和全屏入口，设置面板收敛为画面比例/填充，保留返回入口并移除常驻诊断 chip。

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `9e4b4bd` | (see git log) |
| `f08f4d0` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 5: 完成 Player 播放高级控制

**Date**: 2026-05-15
**Task**: 完成 Player 播放高级控制
**Branch**: `main`

### Summary

完成倍速、字幕/音轨、全屏、画面比例与播放器偏好持久化：字幕合并详情页与内嵌轨道，倍速写入 Tauri SQLite，离开播放页停止播放，并清理用户可见底层技术文案。

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `e242134` | (see git log) |
| `fd58a7c` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 6: 修复原始文件源单文件电影识别

**Date**: 2026-07-08
**Task**: 修复原始文件源单文件电影识别
**Branch**: `develop-new/openlist-scrape-cache`

### Summary

完成 OpenList/Alist 原始文件源分类修复：根目录单文件电影和显式分类目录下电影可被识别，作品目录不伪造分类，噪声文件保持未识别；已通过 verify、typecheck、lint、build、cargo check 和 Windows GNU Tauri build，刷新 release exe；补充 frontend spec 并归档 Trellis 任务。

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `dc186fb` | (see git log) |
| `a334610` | (see git log) |
| `1827789` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 7: Windows 原生 Server 启动与测试

**Date**: 2026-08-12
**Task**: Windows 原生 Server 启动与测试
**Branch**: `develop`

### Summary

新增 Windows PowerShell 启动与全量测试入口，缺少 Go 时通过 winget 安装官方系统包；切换纯 Go SQLite，隔离运行与测试目录，修复 CRLF 权限校验并验证真实健康检查。

### Git Commits

| Hash | Message |
|------|---------|
| `00f9400` | (see git log) |

### Status

[OK] **Completed**


## Session 8: 实现 Server 本地 Storage 与路径安全

**Date**: 2026-08-12
**Task**: 实现 Server 本地 Storage 与路径安全
**Branch**: `develop`

### Summary

完成本地 Storage v2 迁移、Windows/UNC 路径与 Reparse Point 安全、只读容量探测、RBAC/审计/API/管理端，并通过完整 Windows 质量门和真实媒体根只读 API 验收。

### Git Commits

| Hash | Message |
|------|---------|
| `901a8ea` | (see git log) |
| `7ac786a` | (see git log) |

### Status

[OK] **Completed**


## Session 9: 实现 Server 跨平台目录选择器

**Date**: 2026-08-13
**Task**: 实现 Server 跨平台目录选择器
**Branch**: `develop`

### Summary

完成 Server 进程可见盘符/挂载点的只读目录树、密封签名 token、storages.browse 双重授权、限流与路径重验，并将 Storage 创建/编辑替换为跨平台目录选择弹窗；通过完整 Windows 与跨平台质量门。

### Git Commits

| Hash | Message |
|------|---------|
| `21a061a` | (see git log) |
| `b3b0415` | (see git log) |

### Status

[OK] **Completed**


## Session 10: Server 管理端主题与目录根导航

**Date**: 2026-08-13
**Task**: Server 管理端主题与目录根导航
**Branch**: `develop`

### Summary

将 Server 管理端统一为默认白色、可持久化深色切换的传统后台风格，修复 Windows 目录选择器返回盘符根层，并完成全量门禁与实机验证。

### Git Commits

| Hash | Message |
|------|---------|
| `4034d2c` | (see git log) |
| `a2c8a28` | (see git log) |
| `773aeb0` | (see git log) |

### Status

[OK] **Completed**


## Session 11: Server 媒体分类规则管理

**Date**: 2026-08-13
**Task**: Server 媒体分类规则管理
**Branch**: `develop`

### Summary

实现独立 MediaClassificationProfile v1、Player 等价默认规则、严格匹配契约、迁移/RBAC/API/审计与白深双主题规则管理页，并完成 Windows 全量门禁和浏览器生命周期验证。

### Git Commits

| Hash | Message |
|------|---------|
| `8acb41c` | (see git log) |
| `92fc2e7` | (see git log) |
| `0af4d43` | (see git log) |

### Status

[OK] **Completed**


## Session 12: Server 结构化运行日志与日志中心

**Date**: 2026-08-13
**Task**: Server 结构化运行日志与日志中心
**Branch**: `develop`

### Summary

实现 Server 运行日志双写、统一脱敏、轮转压缩和保留策略，新增细粒度查询导出配置 RBAC 与 Web 日志中心，并通过完整 Windows 与浏览器验收。

### Git Commits

| Hash | Message |
|------|---------|
| `37aab85` | (see git log) |

### Status

[OK] **Completed**


## Session 13: 完成 Server 媒体库自动扫描与监听基础

**Date**: 2026-08-13
**Task**: 完成 Server 媒体库自动扫描与监听基础
**Branch**: `develop`

### Summary

实现 Storage 相对根媒体库、自动首次全量扫描、独立 watcher 与文件级增量更新、扫描记录和管理端页面；完成 Windows 全量质量门、真实四文件只读验收与安全复核。

### Git Commits

| Hash | Message |
|------|---------|
| `2fffbe5` | (see git log) |

### Status

[OK] **Completed**


## Session 14: 实现 Server 持久化任务队列与任务中心

**Date**: 2026-08-13
**Task**: 实现 Server 持久化任务队列与任务中心
**Branch**: `develop`

### Summary

实现 SQLite 持久化 Job 队列、typed worker 调度、lease/checkpoint/ActionRequest、lane 调序、权限过滤 WebSocket 与全局任务中心；补齐安全边界、并发恢复、公平性和 Windows/浏览器验收。

### Git Commits

| Hash | Message |
|------|---------|
| `bb99538` | (see git log) |
| `8ac2eec` | (see git log) |
| `02ba79c` | (see git log) |

### Status

[OK] **Completed**


## Session 15: 打通 Player 与 Server 安全媒体接入

**Date**: 2026-08-22
**Task**: 打通 Player 与 Server 安全媒体接入
**Branch**: `develop`

### Summary

完成 Player 设备登录、Server 媒体目录、115 直连播放、Emby 身份合并与播放线路选择；修复令牌重连生命周期、分页截断、停用库与存储访问，并通过 Player、Rust、Server 全量质量门。

### Git Commits

| Hash | Message |
|------|---------|
| `c695b38` | (see git log) |

### Status

[OK] **Completed**


## Session 16: 修复 Server 本地播放与媒体详情并发布 Player v1.1.10

**Date**: 2026-08-22
**Task**: 修复 Server 本地播放与媒体详情并发布 Player v1.1.10
**Branch**: `develop`

### Summary

完成 Server 本地安全 Range 直出、完整 TMDB 元数据与多剧照、Player Server/Emby 详情修复；全部质量门通过并成功发布 Player v1.1.10 Beta，Server 仅推送 develop。

### Main Changes

- 本地 Storage 电影与剧集可通过 Player Bearer stream endpoint 播放，支持 GET/HEAD/Range 并拒绝路径逃逸和 Windows reparse point。
- Server DTO 与 Player DataSource 补齐评分、时长、类型、演职人员、外部 ID 和多剧照，Emby People 与 backdrop 查询同步补强。
- 从最新远端 develop 提交发布 v1.1.10 Beta，Windows、Android、签名清单与校验资产全部成功。

### Git Commits

| Hash | Message |
|------|---------|
| `c2f753d` | (see git log) |
| `2651402` | (see git log) |
| `5e2d2a0` | (see git log) |

### Testing

- [OK] Server go test ./...、CGO_ENABLED=0 go test ./...、go vet ./... 通过。
- [OK] Player 专用 verify、typecheck、lint、build 通过；Rust cargo test 90/90、check、clippy 通过。

### Status

[OK] **Completed**

### Next Steps

- 在真实本地媒体库与 Emby 环境中人工验证电影播放、剧集季集、演职人员和多剧照展示。


## Session 17: 修复 Player 设备管理与 Server 媒体来源

**Date**: 2026-08-22
**Task**: 修复 Player 设备管理与 Server 媒体来源
**Branch**: `develop`

### Summary

接通 Server Web UI 的真实 Player 配对设备列表与安全撤销；区分 Server 本地文件流和 115 302 直链，完成全量验证并发布 Player v1.1.11 Beta。

### Git Commits

| Hash | Message |
|------|---------|
| `16ec854` | (see git log) |

### Status

[OK] **Completed**


## Session 18: 建立插件平台并接入 Bilibili 在线媒体源

**Date**: 2026-08-23
**Task**: 插件平台与 Bilibili 在线媒体源
**Branch**: `develop`

### Summary

完成 GitHub 多插件仓库、WASM 隔离安装生命周期、受控 Host API、Server 在线媒体库和 Player 通用站点体验；Bilibili 首个插件支持推荐、热门、排行、搜索、详情、分 P、清晰度、历史分页、进度回传、原生弹幕和下载计划。

### Main Changes

- Player 新增真正分页的本地历史页，并支持 Server/Bilibili 历史 cursor 和来源区分。
- 播放进度先保存本地，再按能力回传提供方；远端失败不影响播放恢复。
- 插件弹幕优先使用 Server 同源安全资产，缺失或失败时回退原有弹幕服务。
- 修复 Bilibili 三字段历史 cursor 和 Server 多来源耗尽游标，避免第二页重复、遗漏或无限翻页。
- 官方 Registry 和 Bilibili `0.1.0` Release 资产可供 Server 通过 GitHub 仓库发现和安装。

### Git Commits

| Hash | Message |
|------|---------|
| `99b837f` | feat: 建立插件平台并接入 Bilibili 在线媒体源 |

### Testing

- [OK] Server `go test ./...`、`go vet ./...`、普通与 webui build 通过。
- [OK] Server Web UI 102 项测试、typecheck、lint、build 通过。
- [OK] Player 在线库/清晰度/弹幕验证、typecheck、lint、build、Cargo 91 项测试和严格 Clippy 通过。
- [OK] Plugin SDK verify/typecheck、Hub build、Bilibili 7 项 Rust 测试、严格 Clippy、WASM 构建和确定性打包通过。
- [OK] Windows 隔离 Server 冒烟通过，测试进程与隔离目录已清理。

### Status

[OK] **Current milestone completed**

### Next Steps

- 后续继续实现 Bilibili 扫码登录、收藏/稍后再看/关注、DASH 双轨与宿主真实下载执行器。


## Session 18: 完成插件平台与 Bilibili 在线媒体全链路

**Date**: 2026-08-23
**Task**: 完成插件平台与 Bilibili 在线媒体全链路
**Branch**: `develop`

### Summary

完成通用在线媒体插件契约、安全扫码凭据捕获、Bilibili 浏览播放与真实下载、Player 首页贡献和 DASH 双轨；发布插件 0.2.0 与 Player v1.1.13 Beta。

### Git Commits

| Hash | Message |
|------|---------|
| `35e92a8` | (see git log) |

### Status

[OK] **Completed**


## Session 19: 发布插件宿主能力与 Bilibili 0.3.0

**Date**: 2026-08-23
**Task**: 发布插件宿主能力与 Bilibili 0.3.0
**Branch**: `develop`

### Summary

完成声明式插件设置页、Host 内嵌扫码、插件专属元数据及统一下载入库能力；Server 已推送 develop，Bilibili 0.3.0 已发布至官方插件仓库并通过公网哈希与 Manifest 校验。

### Git Commits

| Hash | Message |
|------|---------|
| `e33669f` | (see git log) |

### Status

[OK] **Completed**


## Session 20: 修复并发布 Bilibili 扫码登录 0.3.1

**Date**: 2026-08-23
**Task**: 修复并发布 Bilibili 扫码登录 0.3.1
**Branch**: `develop`

### Summary

定位 Bilibili 二维码域名迁移导致 Host 精确权限校验拒绝，补齐 account.bilibili.com 最小权限和真实响应回归测试，发布官方插件 0.3.1 并完成公网摘要校验。

### Git Commits

| Hash | Message |
|------|---------|
| `d9ffecb` | (see git log) |

### Status

[OK] **Completed**


## Session 21: Server 嵌套媒体库导航与 Bilibili 在线播放

**Date**: 2026-08-23
**Task**: Server 嵌套媒体库导航与 Bilibili 在线播放
**Branch**: `develop`

### Summary

实现标准媒体库分类入口和插件任意层级导航，修复 Bilibili DASH 音频 CDN 端口与备用地址选择，完成跨层安全校验、全量回归、插件与 Player 发布准备。

### Git Commits

| Hash | Message |
|------|---------|
| `84d2079` | (see git log) |

### Status

[OK] **Completed**


## Session 22: 完善 Server 媒体库封面与分层导航

**Date**: 2026-08-23
**Task**: 完善 Server 媒体库封面与分层导航
**Branch**: `develop`

### Summary

为 Server 物理媒体库、分类与插件在线媒体库增加安全静态封面，过滤空分类，统一 Player 分层返回，并发布 Bilibili 插件 0.3.3。

### Git Commits

| Hash | Message |
|------|---------|
| `c248b7b` | (see git log) |

### Status

[OK] **Completed**


## Session 23: 修复 Bilibili 双轨播放与动态媒体库封面

**Date**: 2026-08-24
**Task**: 修复 Bilibili 双轨播放与动态媒体库封面
**Branch**: `develop`

### Summary

修复 Player 桌面与 Android 的 DASH 视频音频多 token 回环会话；Server 为本地、115 与插件媒体库生成签名动态封面，Player 独立目录本地组合封面，并发布 Bilibili 0.3.4 所需插件契约与安全校验。

### Git Commits

| Hash | Message |
|------|---------|
| `8469966` | (see git log) |

### Status

[OK] **Completed**


## Session 24: 统一风格3分类封面并发布新版

**Date**: 2026-08-24
**Task**: 统一风格3分类封面并发布新版
**Branch**: `develop`

### Summary

对照 MoviePilot-Plugins style_static_3 源码，将 Server 顶层入口固定封面与分类动态封面分层；本地、115 和 Bilibili 分类统一 1920×1080 风格3，保持 Emby/Jellyfin 原生图不受影响；推送 develop，发布 Bilibili 0.3.5 与 Player v1.1.17 Beta。

### Git Commits

| Hash | Message |
|------|---------|
| `5f9ab90` | (see git log) |

### Status

[OK] **Completed**


## Session 25: 修复分类封面候选与少海报构图

**Date**: 2026-08-24
**Task**: 修复分类封面候选与少海报构图
**Branch**: `develop`

### Summary

用真实本地与115数据库定位低置信识别快照被候选查询遗漏的问题；按参考插件补齐九个风格3槽位，修复Player独立媒体库单海报分类，并补充跨层回归测试与规范。

### Git Commits

| Hash | Message |
|------|---------|
| `d7b9a68` | (see git log) |

### Status

[OK] **Completed**


## Session 26: 发布 Player 下一代多语言识别器 Beta

**Date**: 2026-08-24
**Task**: 发布 Player 下一代多语言识别器 Beta
**Branch**: `codex/player-nextgen-media-recognition-beta`

### Summary

完成 Unicode 多语言解析、确定性 TMDB 候选排名、安全缓存迁移与人工覆盖保护；通过 Player 全量门禁后推送 develop，并成功发布 v1.1.19 Beta，核对 Windows/Android 资产和提交链一致。

### Git Commits

| Hash | Message |
|------|---------|
| `8b7c56ad9a5f988be5c87a72e0f79a3163e0f270` | (see git log) |

### Status

[OK] **Completed**


## Session 27: 同步双端媒体识别 v10 并准备 Beta

**Date**: 2026-08-25
**Task**: 同步双端媒体识别 v10 并准备 Beta
**Branch**: `develop`

### Summary

Server 修复下载重试和真实 TMDB 同名空壳候选，Player 同步 nextgen v4/contract v3；双端补齐有界权威消歧、详情降级、缓存边界和分数饱和回归，完整门禁通过并准备 v1.1.25 Beta。

### Git Commits

| Hash | Message |
|------|---------|
| `0c565df` | (see git log) |
| `78497a3` | (see git log) |
| `edce279` | (see git log) |

### Status

[OK] **Completed**


## Session 28: 完成媒体入库通知闭环

**Date**: 2026-08-26
**Task**: 完成媒体入库通知闭环
**Branch**: `develop`

### Summary

补齐权威媒体变更、Emby/Jellyfin 持久刷新、Player 安全增量收敛，并完成跨端回归与安全检查。

### Main Changes

- 修复 artifact readiness、刷新目标恢复重试与管理操作
- 实现 Player 多设备安全长轮询和媒体库级无干扰刷新
- 补齐本地 watcher 与 fake 115 双消费者闭环回归

### Git Commits

| Hash | Message |
|------|---------|
| `f9496d8` | (see git log) |
| `f23b055` | (see git log) |
| `6a2bf5b` | (see git log) |

### Testing

- [OK] Server 全量测试、vet、普通与 WebUI 构建通过
- [OK] WebUI 133 项测试及 Player 验证、类型检查、Lint、构建通过
- [OK] Rust 92 项测试和 Clippy -D warnings 通过

### Status

[OK] **Completed**

### Next Steps

- 在真实 115、qBittorrent、Emby/Jellyfin 与多台 Player 环境执行操作员联调


## Session 29: 归档已完成历史任务

**Date**: 2026-08-26
**Task**: 归档已完成历史任务
**Branch**: `develop`

### Summary

按项目实际完成情况归档除 PT 站点工作外的 40 个历史任务，仅保留站点发现、PT 搜索下载和推荐聚合三个任务。

### Main Changes

- 归档 40 个已完成的 Server、Player、存储、下载、115、媒体整理与识别任务
- 保留 3 个 PT 相关任务等待按站点管理内搜索入口重新收敛

### Git Commits

(No commits - planning session)

### Testing

- [OK] 确认活跃任务目录只剩 3 个 PT 相关任务
- [OK] 确认归档后工作区干净

### Status

[OK] **Completed**

### Next Steps

- 重新规划站点管理卡片内的单站点搜索入口


## Session 30: 完成 Server 站点识别与安全整理

**Date**: 2026-08-26
**Task**: 完成 Server 站点识别与安全整理
**Branch**: `develop`

### Summary

完成地址驱动 BT 站点与单站搜索、统一媒体身份和可选 AI 识别、修正识别重新整理，以及本地/115 四档安全删除；全量 Go/Web UI 门禁通过，准备发布 Server Beta。

### Git Commits

| Hash | Message |
|------|---------|
| `0cca9b4` | (see git log) |

### Status

[OK] **Completed**


## Session 31: Server 扫描与 OpenRouter 热修

**Date**: 2026-08-26
**Task**: Server 扫描与 OpenRouter 热修
**Branch**: `develop`

### Summary

修复媒体库编辑导致内容修订游标回退和扫描提交唯一键冲突，增加旧库自愈、并发与十集扫描回归、安全持久化诊断，并兼容 OpenRouter /api/v1。

### Git Commits

| Hash | Message |
|------|---------|
| `599ec2f` | (see git log) |

### Status

[OK] **Completed**


## Session 32: 完成Player下载管理与完整离线播放

**Date**: 2026-08-27
**Task**: 完成Player下载管理与完整离线播放
**Branch**: `develop`

### Summary

完成下载中心、可恢复调度、稳定身份解析、完整离线包、OfflineDataSource、本地优先播放、下载徽标与桌面/Android构建门禁；修复Android异步SQLite跨await问题，并保持Server并行任务隔离。

### Git Commits

| Hash | Message |
|------|---------|
| `e898d45` | (see git log) |

### Status

[OK] **Completed**


## Session 33: 发布 Server AI 模型选择 Beta

**Date**: 2026-08-27
**Task**: 发布 Server AI 模型选择 Beta
**Branch**: `develop`

### Summary

完成 Server AI 模型选择窗口、独立响应上限和安全错误映射，新增精确边界与真实 DOM 交互测试；完整 Server/WebUI 门禁通过，准备从最新 develop 发布匹配 Beta。

### Git Commits

| Hash | Message |
|------|---------|
| `ada3077` | (see git log) |

### Status

[OK] **Completed**


## Session 34: 修复 Player 下载入口与操作菜单

**Date**: 2026-08-27
**Task**: 修复 Player 下载入口与操作菜单
**Branch**: `develop`

### Summary

移除重复离线下载入口，统一应用内下载确认，修复媒体操作菜单越界，并补充已下载徽标回归验证。

### Git Commits

| Hash | Message |
|------|---------|
| `ec29b0f` | (see git log) |

### Status

[OK] **Completed**


## Session 35: 修复 Player 离线投影与线程恢复

**Date**: 2026-08-27
**Task**: 修复 Player 离线投影与线程恢复
**Branch**: `develop`

### Summary

将离线下载统一为原媒体本地副本状态，清理旧离线投影与下载徽标残留，并让新任务、暂停继续和失败重试采用最新下载线程设置。

### Git Commits

| Hash | Message |
|------|---------|
| `082ae6c` | (see git log) |

### Status

[OK] **Completed**


## Session 36: 统一媒体身份搜索与库覆盖率

**Date**: 2026-08-27
**Task**: 统一媒体身份搜索与库覆盖率
**Branch**: `codex/unified-media-search-subscription`

### Summary

完成 TMDB 海报搜索、统一详情、多名称 PT/BT 聚合、跨媒体库电影与季集覆盖率，并通过独立检查修复 SSE 渐进输出、精确季过滤、TMDB partial unknown 和查询长度边界。

### Git Commits

| Hash | Message |
|------|---------|
| `6379537` | (see git log) |
| `c120846` | (see git log) |
| `980c153` | (see git log) |

### Status

[OK] **Completed**


## Session 37: 完成统一媒体搜索与自动电视剧订阅

**Date**: 2026-08-27
**Task**: 完成统一媒体搜索与自动电视剧订阅
**Branch**: `codex/unified-media-search-subscription`

### Summary

基于 MoviePilot v3 产品流程完成 TMDB 海报搜索、多语言资源聚合、跨库覆盖率和可配置电视剧订阅；订阅通过持久队列自动识别明确缺集、确定性选种并复用下载转移入库管线，同时补齐 RBAC、竞态防护、安全快照、管理界面、测试与架构规范。

### Git Commits

| Hash | Message |
|------|---------|
| `ef9ed65` | (see git log) |
| `ca8df52` | (see git log) |
| `d5a2f9d` | (see git log) |

### Status

[OK] **Completed**


## Session 38: 统一搜索订阅与 115 下载流水线

**Date**: 2026-08-27
**Task**: 统一搜索订阅与 115 下载流水线
**Branch**: `codex/unified-media-search-subscription`

### Summary

完成搜索双入口与白屏修复、详情库存和自动订阅闭环、PT/BT 到 115 的权威路由、115 统一下载目录与生活事件监听，以及 provider-first 文件保留型取消和默认删除合同；补齐并发竞态和全量验证。

### Git Commits

| Hash | Message |
|------|---------|
| `ee79df0` | (see git log) |
| `d074536` | (see git log) |
| `43812b5` | (see git log) |

### Status

[OK] **Completed**
