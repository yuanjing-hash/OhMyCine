# OhMyCine — 开发路线图

> **标记说明**: `[x]` 已完成并验证 · `[~]` 已写完待验证 · `[ ]` 未开始
>
> **Trellis 接管说明**: 既有 Player 开发成果按当前状态接入 Trellis 管理，后续任务只在现有基础上继续推进，不要求迁移时重做。任何 Trellis task 完成功能、补齐实现或改变设计状态时，都必须同步更新本路线图的 `[x]`/`[~]`/`[ ]` 标记或状态说明，避免任务状态与路线图脱节。

## 核心设计原则

- **Player 独立优先**：Player 必须在没有 Server 的情况下完整可用，除 Server 联动入口外优先完整开发
- **聚合首页优先**：Player 首页聚合全部已绑定数据源，优先实现 Hero 轮播、继续观看、最新影片与动态数据源侧栏
- **全功能开源**：所有功能完全免费开源，路线图只调整实施顺序，不削减最终功能范围
- **Server 是增强层**：Server 提供媒体流水线等高级功能，但不阻塞 Player 基本使用
- **流水线驱动**：Server 核心是 发现→下载→转移→入库→通知 的闭环
- **刚需驱动 Server MVP**：Server 初期优先支持 115网盘、OpenList/Alist、CloudDrive2、本地文件、STRM 与 302代理，其他网盘和生态能力后续扩展

## 阶段总览

```
Phase 0: 基础设施           ████░░░░░░░░░░░░░░░░  Week 1-2
Phase 1: Player 独立版 MVP  ████████░░░░░░░░░░░░  Week 3-8
Phase 2: Server MVP        ████████████░░░░░░░░  Week 9-14
Phase 3: 核心功能增强       ████████████████░░░░  Week 15-22
Phase 4: 生态系统           ████████████████████  Week 23+
```

---

## Phase 0: 基础设施 (Week 1-2)

> 搭建开发环境、CI/CD、项目骨架

### 0.1 仓库与项目结构

- [x] 创建仓库 `OhMyCine` 并关联本地 (已完成: `yuanjing-hash/OhMyCine`)
- [x] 初始化 monorepo 结构 (所有组件在同一仓库)
  ```
  ohmycine/
  ├── player/          — Tauri + Vue 播放器
  ├── server/          — Go 后端
  ├── hub/             — VitePress 插件市场
  ├── cli/             — Go CLI 工具 (与 server 共享 pkg/)
  ├── docs/            — 架构文档
  └── .github/         — CI/CD
  ```
- [x] 配置 `.gitignore` (Go/Node/Rust/OS 文件)
- [x] 配置 `.editorconfig` (统一编码风格)
- [x] 编写 `LICENSE` (GPL-3.0)
- [x] 编写 `README.md` (项目介绍、快速开始、架构概览)
- [x] 编写 `CONTRIBUTING.md` (贡献指南、PR流程、Commit规范)

### 0.2 CI/CD Pipeline

> 早期 CI/CD 目标是“自动检查、自动编译、产物可下载”，正式发布和 Docker 镜像推送后置。

#### 开发期 CI（Phase 0 必做）

- [~] Go 后端 CI: `go test ./...` + `go build` + `golangci-lint`
- [~] CLI CI: `go test ./...` + `go build`
- [~] Vue/Player 前端 CI: `eslint` + `vue-tsc --noEmit` + `vite build`
- [~] Tauri 桌面构建 CI: 当前只验证 Windows GNU 构建测试包；Linux/macOS Player 渲染器和打包链路完成后再接入 CI
- [~] Hub 文档站 CI: VitePress build
- [~] 上传 GitHub Actions Artifacts，方便下载本地测试

#### 手动测试构建（MVP 阶段）

- [~] `workflow_dispatch` 手动触发构建
- [~] 支持选择构建组件: Player / Server / CLI / Hub
- [~] 支持选择构建平台: 当前 Player 手动构建只保留 Windows GNU；Linux/macOS 后续等渲染器完成后再接入
- [~] 构建产物保留 7-30 天供测试下载

#### 发布期 CI（后置）

- [ ] Docker 构建 CI: `docker build`
- [ ] Docker 镜像推送: GHCR / Docker Hub
- [~] Release CI: Player 已支持 tag/manual GitHub Release、Beta/Stable 渠道、签名 NSIS updater artifact、`latest.json`、安装包、标准免安装 ZIP、便携 ZIP 和 SHA-256；Server Beta 可在同版本 prerelease 追加内嵌 WebUI 的 Windows/Linux 包与校验清单；待配置 GitHub updater 私钥 Secret 并完成首个实机更新发布
- [x] 自动生成 changelog/release notes：Player Release 工作流可从 tag/commit 生成分组说明，并按 `beta` / `stable` 通道分别发布 prerelease 或最新正式版
- [ ] 自动上传正式安装包和二进制文件

### 0.3 开发环境

> 本地开发优先使用手动编译和本地运行；Docker 主要用于后续部署、CI 集成测试和 NAS/服务器环境。

- [x] 本地开发启动脚本: Player / Server / CLI / Hub 手动编译运行
- [x] Makefile / Taskfile (常用命令: build, test, lint, dev)
- [x] VS Code 推荐配置 (`.vscode/extensions.json`, `.vscode/settings.json`)
- [x] 无 Docker 本地开发文档 (npm/tauri、go run/go test、cargo check)
- [ ] Docker Compose 部署配置 (可选，用于 NAS/服务器/CI 集成测试)

### 0.4 品牌与文档

- [ ] 设计 Logo (SVG + PNG 多尺寸)
- [x] 设计品牌色 (主色 #4A9EFF, 强调色 #A855F7)
- [x] 初始化 VitePress 文档站点框架
- [x] 编写架构文档索引页

---

## Phase 1: Player 独立版 MVP (Week 3-8)

> Player 独立可用版本 — 无需 Server，原生连接 Emby/Jellyfin/OpenList/Alist/CloudDrive2/本地文件夹

### Sprint 1.1: Tauri 项目 + libmpv (Week 3-4)

**目标**: Player 能启动，libmpv 能播放视频，基础窗口管理可用

#### Tauri 项目初始化

- [x] `npm create tauri-app` 初始化项目
- [x] 配置 Vue 3 + TypeScript + Vite
- [x] 配置 UnoCSS (原子化CSS)
- [x] 配置 Pinia (状态管理)
- [x] 配置 Vue Router (SPA路由)
- [x] 配置 Vue I18n (国际化框架)
- [x] 配置 `tsconfig.json` (严格模式)
- [x] 配置 ESLint + Prettier (代码规范)
- [x] 配置 `tauri.conf.json` (窗口配置、权限、打包；生产 CSP 已限制脚本为应用自身并禁用 eval/frame/object，保留受控 DataSource HTTP/HTTPS、IPC 与图片协议；开发态仅额外开放 HMR WebSocket)

#### libmpv 嵌入集成

- [~] 下载 libmpv 二进制库 (Windows setup 已补充 GNU import library `libmpv.dll.a` 与运行时 `libmpv-2.dll`，WSL Windows GNU cross-build 与 Windows 宿主播放已验证；Android ARM64 预览固定校验并提取官方 mpv-android `2026-04-25` runtime，APK 包内库/JNI 契约已验证并接入标签触发的 GitHub Release 自动构建；Linux/macOS 与 Android 正式签名后续接入)
- [x] 创建 `src-tauri/src/mpv/` 模块
- [x] 实现 `libmpv-sys` FFI 绑定 (C API 调用)
- [x] 实现 `MpvPlayer` 结构体 (封装所有 MPV 操作)
- [x] 实现 Windows 内嵌视频渲染后端（透明 Tauri/WebView 叠层 + mpv `wid` 视频底层 HWND；True render API / `MpvRenderContext` 深度整合保留为后续阶段）
- [~] 创建 Tauri Plugin（桌面继续使用直接 Tauri Commands；Android 已注册原生 Kotlin `MpvPlugin` 并由同名 Rust commands 转发）
- [x] 实现 Tauri Commands: `mpv_load`, `mpv_pause`, `mpv_resume`, `mpv_seek`
- [x] 实现 Tauri Commands: `mpv_get_property`, `mpv_set_property`
- [x] 实现事件转发: `mpv:time-update`, `mpv:duration-change`, `mpv:paused`, `mpv:resumed`
- [x] 配置 Cargo 依赖: 直接使用 `libmpv-sys = "3.1"` 绑定 libmpv C API
- [~] 编写构建脚本: Windows GNU 已能下载运行时 DLL 并准备 import library；Android ARM64 已能校验固定 mpv-android release、提取运行库、构建 debug APK，并由版本标签自动追加到 GitHub Release；Linux/macOS 与 Android 正式签名发布后续完成

#### Vue 侧播放器 Composable

- [x] 实现 `useMpv()` composable
- [x] 响应式状态: `isPlaying`, `currentTime`, `duration`, `volume`
- [x] 响应式状态: `subtitleTracks`, `audioTracks`, `currentSubtitle`, `currentAudio`（已接入 mpv 轨道状态、内封/外部字幕和当前音轨/字幕选择）
- [x] 方法: `load()`, `togglePause()`, `seek()`, `setVolume()`
- [x] 方法: `setSubtitle()`, `setAudio()`
- [x] 事件监听自动清理 (`onUnmounted`)

#### 基础窗口管理

- [x] 无边框窗口配置 (`decorations: false`)
- [x] 自定义标题栏组件 (`WindowChrome.vue`)
- [x] 窗口拖拽区域（空白区域左键按下立即调用原生 `startDragging()`，保留 Windows Snap）
- [x] 窗口控制按钮 (最小化/最大化/关闭)
- [x] 全屏切换 (`appWindow.setFullscreen`)

#### 基础播放控制 UI

- [x] `VideoPlayer.vue` — 视频播放区域（Windows 已实现透明 Tauri/WebView 叠层 + mpv 视频底层 HWND；ready + loaded media 时 DOM 根链保持透明，idle/error/unsupported/no-media 显示有意占位）
- [x] `PlayerControls.vue` — 播放控制条
- [x] `ProgressBar.vue` — 进度条 (可拖拽)
- [x] `VolumeControl.vue` — 音量控制
- [x] 播放/暂停按钮
- [~] 快进/快退按钮 (10s 已接入；60s 待后续快捷控制扩展)
- [x] 音量显示与控制
- [x] 上一集/下一集按钮（已接入播放队列上下文；剧集/队列播放时按可用状态启用）

**产出**:

- [~] 桌面应用能启动，无边框窗口 (WSL 下 `tauri dev` 已可编译并启动进程；图形渲染受 EGL/WSLg 环境限制仍需 Windows 原生或完整桌面环境复验)
- [x] 能拖拽文件到播放页视频区域或通过右下角悬浮播放按钮选择本地视频并交给 libmpv 后端加载；Windows 宿主已验证可透过透明 Tauri/WebView 叠层看到 mpv 视频底层窗口画面
- [x] libmpv 渲染在窗口内部，沉浸式体验（Windows MVP 已通过 `wid` + `vo=gpu-next` + 透明 Tauri/WebView 叠层完成；Android ARM64 已接入 `SurfaceView` + `gpu-next` 并通过构建/产物验证，待真机播放确认；Linux/macOS 仍显示 unsupported）
- [x] 基础播放控制 (播放/暂停/进度/音量；控制层已改为 Cinema OS/liquid-glass 并支持静止自动隐藏，Windows 宿主已验证叠层控件可见且可点击)
- [x] 播放缓冲状态 (桌面与 Android 统一读取 libmpv `paused-for-cache` / `cache-speed`，延迟显示中央缓冲提示与实时速率)

### Sprint 1.2: DataSource 抽象层 (Week 5-6)

**目标**: DataSource 接口定义 + Emby/Jellyfin 原生连接 + 配置管理

#### DataSource 接口设计

- [~] 定义 `MediaItem` 接口 (id, sourceId, libraryId, name, titleLogoUrl, posterUrl, backdropUrl, overview, year, rating, path...)
- [~] 定义 `MediaDetail` 接口 (extends MediaItem + genres, directors, cast...)
- [~] 定义 `MediaLibrary` 接口 (sourceId, name, type, posterUrl, backdropUrl, itemCount)
- [~] 定义 `HomeSection` 接口 (hero, continueWatching, recentlyAdded, recommended, libraryRow)
- [~] 定义 `SubtitleTrack`, `AudioTrack` 接口
- [~] 定义 `DataSourceType` 类型枚举
- [~] 定义 `DataSourceConfig` 接口 (id, type, name, displayName, iconUrl, order, credentials...)
- [~] 定义 `DataSource` 接口 (init, test, destroy, list, listLibraries, getHomeSections, getFeaturedItems, getContinueWatching, getRecentlyAdded, search, getDetail, getStreamURL, optional `getStreamRequest({ itemId, mediaSourceId })` / syncPlaybackProgress；播放路由只保存媒体身份，Jellyfin 等实现待补齐)

#### DataSourceManager 实现

- [~] 实现 `DataSourceManager` 类（已支持 Emby 实例化、按配置同步、聚合首页基础区块；跨源搜索/导入导出完善待后续）
- [x] `addSource()` — 创建并初始化数据源
- [x] `removeSource()` — 销毁并移除数据源
- [x] `getAllSources()` / `getSource(id)`
- [x] `getOrderedSources()` — 按绑定配置顺序返回侧栏数据源
- [~] `getAggregatedHome()` — 聚合 Hero / 继续观看 / 最新影片首页数据（已接 Emby，更多源待扩展）
- [x] `searchAll()` — 跨数据源并发搜索（桌面 `首页 / 搜索 / 设置` 顶部入口展开液态玻璃工作台，手机首页顶部下拉打开全屏工作台；支持搜索前馆藏建议、按来源/媒体库/类型筛选、按源并发、故障隔离、`sourceId + itemId` 去重和结果上限）
- [~] `exportAllConfigs()` / `importConfigs()` — 配置导入导出（已能导出已实例化安全配置，完整文件导入导出待后续）
- [x] `createDataSource(type)` — 工厂方法（当前实现 Emby、OpenList/Alist、CloudDrive2、本地文件；其他类型保留扩展）

#### EmbyDataSource 实现

- [~] 实现 `EmbyClient` 类 (封装 Emby REST API；当前合并在 `EmbyDataSource` 内部 request/client 边界，后续可拆分)
- [x] `getSystemInfo()` — 测试连接
- [x] `getMediaFolders()` — 获取媒体库列表
- [x] `getHomeSections()` — 获取 Emby 首页区块/继续观看/最新项目
- [x] `getFeaturedItems()` — 获取可用于 Hero 轮播的 Logo/backdrop/overview 元数据
- [x] `getItems(parentId)` — 获取库内项目
- [x] `search(keyword)` — 搜索
- [x] `getItem(id)` — 获取详情 (含 People/Genres/MediaStreams)
- [x] `getImageUrl(itemId, type)` — 构建图片URL
- [x] 实现 `EmbyDataSource` (implements DataSource)
- [x] `mapEmbyItem()` — Emby 数据映射到 MediaItem
- [x] Emby 受控 HTTP 边界 — 登录、系统信息、媒体库/详情/搜索/标记已观看与 PlaybackInfo/进度上报统一走 Rust 原生 JSON 客户端；15 秒超时、禁用自动重定向、4 MiB 实际流式响应上限，并限制方法、路径、查询和请求体；浏览器开发 fallback 使用同等边界
- [x] Emby 播放进度同步 — 通过 PlaybackInfo + Sessions/Playing/Progress/Stopped 将 active session、继续观看和播放历史同步回 Emby；本机 SQLite 保留离线兜底，provider sync best-effort；有效 provider 位置优先于设备本地缓存，续播位置解析前暂停进度写入，Android/302 慢加载在 `video-ready` 前保持待恢复位置，避免起播 `0` 秒覆盖跨设备云端记录
- [x] Emby 多版本播放 — 详情页选择的 `mediaSourceId` 参与即时播放请求解析，并沿用到对应播放进度会话；失效版本不再静默回退到首个版本

#### JellyfinDataSource 实现

- [ ] 实现 `JellyfinDataSource` (与 Emby API 类似，差异处理)
- [ ] API 路径差异适配 (`/emby/` → `/`)
- [ ] 认证头差异适配

#### 配置持久化

- [x] 建立统一 Tauri storage layout：Windows 默认使用 `%LOCALAPPDATA%/com.ohmycine.player/data`，数据库、缓存和日志不写入安装目录
- [x] 使用 `settings.sqlite` 保存 datasources、主题、TMDB 非敏感设置、分类规则和扫描计划；标准模式首次启动自动迁移旧 WebView localStorage 并删除旧 key，便携模式保持独立空白配置
- [x] 配置变更自动排队写入 SQLite，数据源和用户主动保存流程等待持久化完成后再提示成功
- [x] Emby/OpenList 账号密码与令牌、CloudDrive2 API Token、WebDAV 账号密码通过 provider-specific envelope + `credentialRef` 进入 AES-GCM SQLite 凭证库；标准模式主密钥由 Windows DPAPI、Android Keystore、Apple Keychain 或 Linux Secret Service 保护，Linux 服务不可用时明确降级为权限受限文件密钥；便携模式使用目录内文件密钥并显示较低保护等级警告；旧 Base64 密钥原地迁移且不轮换
- [x] 支持 `portable.flag` / `--portable` 便携模式，portable ZIP 自动携带标记，应用自有 data/cache/logs 写入 EXE 同目录；禁止自动导入标准配置，并对 WSL/UNC 存储路径显示性能提示

#### 设置页面 UI

- [~] `SettingsView.vue` — 设置页面（已提供数据源管理入口、刮削与分类入口；Emby/OpenList 使用账号密码、CloudDrive2 使用 gRPC 服务地址 + API Token、WebDAV 使用独立 URL + 账号密码；OpenList/CloudDrive2/WebDAV 均可连接后浏览并选择根目录；本地文件夹在桌面使用目录选择器，在 Android 使用 SAF 文档树持久只读授权；115 以禁用的“即将推出”类型卡片保留入口；数据源定义已外提到 service，其他设置分区组件化继续推进）
- [x] 数据源列表管理 (添加/编辑/删除)
- [~] 刮削与分类设置页（已提供电影/剧集分组、TMDB 官方 genre 受控选项、默认分类实例、包含/排除条件、年份范围、兜底分类和本地结构化规则持久化；TMDB 凭据是可选增强，未配置时扫描保留本地候选和兜底分类；扫描任务与实际规则执行待后续完善）
- [ ] 数据源排序设置 (决定动态侧栏展示顺序)
- [~] 数据源显示配置 (名称/图标/是否在侧栏显示；当前支持显示名称，图标/侧栏开关待后续)
- [~] 添加数据源表单 (管理入口→可见类型卡片选择→Emby/OpenList 账号登录、CloudDrive2 API Token、WebDAV Basic Auth；OpenList/CloudDrive2/WebDAV 连接后可从 `/` 浏览并选择根目录，根目录以 `extra.rootPath` 保存为非敏感配置；本地文件夹在桌面保存只读绝对 root，在 Android 保存 SAF tree URI + 展示标签并验证持久读取授权；账号、密码、token 通过 `credentialRef` 持久化到 Tauri SQLite 凭证库，未写入 localStorage/DataSource 配置)
- [x] 连接测试按钮 (显示成功/失败)
- [~] 数据源状态显示 (在线/离线；当前在测试/浏览错误态中呈现，持久状态徽标待后续)

**产出**:

- [~] 能添加 Emby/Jellyfin 服务器（Emby 已实现；Jellyfin 待后续）
- [x] 能按绑定顺序在动态侧栏展示数据源
- [~] 聚合首页能展示 Emby/Jellyfin 的 Hero 轮播、继续观看、最新影片（Emby 已接入，凭证会话有效时可加载；Jellyfin 待后续）
- [~] 能进入单个数据源媒体库首页并浏览媒体库、搜索影片（Emby 已实现，并改为按媒体库/文件夹/剧集/季/集层级非递归浏览；搜索/首页区块仍可使用递归查询）
- [~] 能直接播放 Emby/Jellyfin 上的视频（Emby 条目可生成 stream URL 并进入现有播放加载流程；Windows 宿主已验证可在透明叠层 + mpv 视频底层窗口中显示，Jellyfin 数据源仍待实现）
- [x] 配置自动持久化（非敏感配置进入统一 settings.sqlite；凭据进入 AES-GCM SQLite；Windows/Android/macOS/Linux 标准模式使用对应系统安全存储保护主密钥；便携模式明确使用目录内文件密钥并提示保护等级）

### Sprint 1.3: OpenList/Alist + CloudDrive2 + 本地文件 (Week 7-8)

**目标**: 完整的独立播放器，支持多种数据源

#### OpenList/Alist DataSource 实现

- [x] 实现 OpenList/Alist HTTP API 客户端 (`/api/auth/login`, `/api/fs/list`, `/api/fs/get`；已通过本地 OpenList/Alist 服务 live test)
- [x] OpenList/Alist 保持原生 HTTP API 路线；WebDAV 已拆分为独立通用 DataSource，不作为 OpenList/Alist 或 CloudDrive2 的替代身份
- [x] `list(path)` — 目录浏览（HTTP API 已实现，按 `extra.rootPath` 作为库根目录浏览，已通过本地 OpenList/Alist 服务验证）
- [x] `search(keyword)` — 搜索（优先 `/api/fs/search` 且 parent 指向 `extra.rootPath`，不可用时在选中根目录内有限目录回退搜索；已通过本地 OpenList/Alist 服务验证）
- [x] `getStreamURL(path)` — 构建播放URL (`/d{path}`，支持 `/api/fs/get` 返回的 sign；路径必须位于 `extra.rootPath` 内，已通过本地 OpenList/Alist 服务播放验证)
- [x] 实现 `AlistDataSource` (OpenList/Alist-compatible, implements DataSource；账号登录-only MVP；支持 `extra.rootPath` 根目录约束)
- [x] 连接测试（设置页添加/编辑时先 `/api/auth/login` 并测试根目录列表；登录后可浏览 `/` 并选择 `extra.rootPath`，已通过本地 OpenList/Alist 服务验证）

#### CloudDrive2DataSource 实现

- [x] 实现 Tauri Rust CloudDrive2 gRPC 客户端，使用用户创建的应用 API Token 与 `Authorization: Bearer` metadata
- [x] `list(path)` / `search(keyword)` — 使用 `GetSubFiles` / `GetSearchResults`，按 `extra.rootPath` 约束并过滤支持的视频文件
- [x] `getStreamURL(path)` / `getStreamRequest(path)` — 使用 `GetDownloadUrlPath` 获取直链、User-Agent 与附加 header，播放信息不写入普通配置或缓存
- [x] 实现 `CloudDrive2DataSource` (implements DataSource；支持列表、详情、原生搜索、播放、raw scan cache Home sections)
- [~] 连接测试（已覆盖 focused verification、Tauri Windows GNU 编译、设置页 API Token/选根目录流程；真实 CloudDrive2 服务和用户 Token 仍需实机验证）

#### 通用 WebDAV DataSource 实现

- [x] 新增独立 `webdav` DataSource 类型，不再以 CloudDrive2 名义承载 WebDAV
- [x] 使用 `PROPFIND` + Basic Auth 只读浏览，账号密码不嵌入 URL
- [x] 支持根目录选择、有限递归搜索、视频详情、临时播放 Authorization header、raw scan cache 与双通道扫描调度
- [~] 连接测试（focused verification 与 Windows GNU 编译已覆盖；不同 WebDAV 服务端兼容性仍需真实环境验证）

#### LocalFileDataSource 实现

- [x] 本地文件系统浏览（桌面通过 Tauri `local_file_list` / `local_file_metadata` 在用户选择 root 内只读列目录与文件；Android 通过 SAF 文档树和 Kotlin `LocalMediaPlugin` 在持久授权 root 内查询；前端统一只暴露 `/` 开头的逻辑路径，绝对路径或 `content://` 子文档 URI 只在平台命令内解析）
- [x] 文件类型过滤（本地源可浏览文件夹和普通文件，播放入口仅允许支持的视频扩展；扫描继续复用原始文件源视频过滤）
- [x] 播放页视频区域拖拽播放、桌面悬浮播放按钮和 Android 快捷操作本地视频选择，以及 LocalFileDataSource 文件夹浏览播放均已接入现有 libmpv 加载流程；Android 使用 SAF `content://` + `fdclose://` 描述符播放且不复制大文件，Windows 内嵌视频渲染已验证
- [ ] 文件关联 (双击打开)
- [x] 实现 `LocalFileDataSource` (implements DataSource；支持设置页添加/编辑本地文件夹、目录浏览、有限搜索、详情、播放路径、raw scan cache Home sections)

#### 云盘数据源

- [x] 115网盘 — 设置页数据源类型选择中显示禁用的“即将推出”标签，不伪装成已可连接
- [~] 123 云盘 — 账号/访问令牌登录、官方 API 动态签名、目录/递归搜索/直链播放、根目录选择、raw scan cache 与双通道扫描已实现；待真实账号跨 Windows/Android 实机验证
- [~] 夸克网盘 — Cookie 凭据、官方扫码登录、官方账号登录窗口、目录/搜索/直链播放、根目录选择与 raw scan 已实现；待真实账号跨 Windows/Android 实机验证
- [ ] 115 接口定义继续保留占位

#### DataSourceManager 完善

- [x] 跨数据源搜索结果合并与去重（单源失败不影响其他来源，保留数据源顺序并支持详情/直接播放）
- [ ] 统一媒体浏览 (合并所有 DataSource 的内容)
- [~] 云盘/本地文件刮削结果接入聚合首页 Hero / 最新影片（已接入 OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV 与本地文件 raw scan cache 中的 `matched` + metadata 条目；未匹配/失败/跳过/未配置条目不进入 Home，缓存读取失败按源隔离）
- [ ] 配置导入/导出 (JSON 文件)

#### 原始文件源本地刮削与海报墙

- [~] 通用刮削分类规则配置（默认实例来自 MP 风格思路，但用户通过受控设置页编辑；分类只作为本地逻辑分组，不要求固定 `movie` / `tv` / `Movies` / `TV` 顶层目录，不写回 OpenList/Alist）
- [~] OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV 与本地文件递归只读扫描、扫描日志与双通道调度（已接入 SourceLibraryView 手动全量/增量扫描、app 启动后台全量/增量调度、数据源页首次无缓存时的当前源/root 前台索引提示；设置页可按原始文件源配置全量/增量启用状态和分钟间隔；本地文件源接入 Tauri root-scoped watcher 并只用逻辑 provider path `/...` 标记增量 dirty；远程原始文件源使用短间隔 polling/diff；Emby/Jellyfin 不进入 Player 原始文件扫描调度；首页/搜索/Hero/海报卡片已接入受控 `cache/images` 二进制缓存）
- [~] 标准目录 / 非标准目录自动识别（已建立 Player 侧纯 TypeScript 评分工具并接入递归扫描；首次进入无缓存媒体库时显示索引进度/状态，不再用空媒体库误导）
- [~] 文件名解析、电影/剧集候选聚合与未识别兜底（已建立基础路径/文件名候选解析，并补充 release/source/subtitle 噪声清洗与中英文搜索标题提取；完整修正工作台待后续）
- [~] TMDB 搜索、详情补全、海报/背景缓存（已接入构建期 CI Secret 注入的 OhMyCine 应用级 Read Access Token，正式包默认可用；用户可用安全凭证中的自定义 token/key 覆盖内置通道；API 默认优先 `api.tmdb.org`，仅在网络错误/超时时回退 `api.themoviedb.org`，图片默认使用 `image.tmdb.org`；已支持独立配置自托管 HTTPS API/图片代理，每项通过自己的真实请求测试后单独启用，失败保留该项旧路由，自定义 API 不跨域回退；已实现搜索/详情补全、poster/backdrop URL 与基于 TMDB metadata 的分类规则执行，无年份自动匹配优先精确标题；豆瓣/Bangumi 等并列元数据提供器与完整多源字段合并待后续）
- [~] OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV 与本地文件 Emby-like 媒体库首页与文件夹兜底视图（已提供 `alist` / `clouddrive2` / `quark` / `123` / `webdav` / `local` 可见 MVP：默认进入大海报轮播 + 逻辑媒体库卡片，分类内电影/剧集/未识别按作品聚合成海报墙；无缓存首次进入会显示自动索引进度/状态并在完成后加载分类；文件夹浏览通过按钮作为兜底入口；扫描状态、结构判断和日志已收进扫描管理面板；标准目录优先使用路径分类，非标准或无路径分类时再使用 TMDB 分类规则兜底）

#### 播放器增强

- [~] Player 下载管理与完整离线播放（已完成下载中心、并发/桌面 Range 分段/全局限速、暂停/恢复/取消/重试、崩溃恢复、Emby/Jellyfin 与 Server 物理媒体稳定身份重解析、OfflineDataSource、持久详情/海报/背景/still/字幕/弹幕包、附件独立重试、作品→季→集投影、静态清晰度选择、下载徽标和本地优先播放；Server 在线插件在缺少用途受限离线流时安全禁用；Windows 中断/短期 302/断网冷启动与 Android SAF/通知/重启续传仍待真实设备验证）
- [x] 字幕菜单（已内联在 `PlayerControls.vue`；后续如需复用再拆独立 `SubtitleMenu.vue`）
- [~] 播放中字幕搜索（已实现 Emby 搜索/Player 本机搜索显式二选一、所有 DataSource 可进入本机搜索、媒体名称/原始文件名/无媒体 ID 限制的自定义关键词三选一、Emby 远程字幕 API、OpenSubtitles REST API Key 与 XML-RPC 账号/匿名兼容模式、现代邮箱账号 401 自动回退、射手网四段 MD5、默认启用的迅雷名称搜索与可选 CID、本地文件直接哈希、远程播放流受限 Range 哈希、提供器独立容错、Rust 短期下载引用与 Tauri cache 即时加载；原生手机使用显式全屏布局，横屏左右分栏并展示本次实际参与的提供器；待真实账号、远程媒体和字幕结果继续实机验证）
- [x] 播放中字幕偏移（字幕菜单内即时控制 mpv `sub-delay`，支持提前/延后 30 秒、0.1 秒滑动、0.5 秒步进和重置，并按 source/media 保存恢复）
- [~] 签名自动更新（Windows 已实现启动/手动检测、Beta/正式渠道、minisign、标准/便携 NSIS；Android 已实现固定 preview 签名、Release APK + SHA-256 受控下载、FileProvider 和系统安装确认；待 Android 从固定签名版本开始验证连续覆盖升级）
- [x] 音轨菜单（已内联在 `PlayerControls.vue`；后续如需复用再拆独立 `AudioMenu.vue`）
- [x] 播放队列面板（已内联在播放控制条并支持上一集/下一集；后续如需复用再拆独立 `PlaylistPanel.vue`）
- [x] 播放历史记录（本机 Tauri SQLite 持久化，避免 localStorage 存播放状态）
- [x] 媒体源删除生命周期（删除配置后按 `sourceId` 清理本机播放历史、单视频播放偏好、来源字幕缓存、动态导航快捷键，并按 source/root 清理原始文件扫描缓存，不影响其他来源）
- [x] 单视频播放偏好与缓存管理（按 `sourceId + mediaIdentity` 保存字幕/音轨稳定草稿、字幕偏移、单视频倍速、画面比例和填充模式；同媒体优先精确轨道 ID，启动期临时轨道状态不能覆盖用户选择；设置页可清除媒体/扫描/字幕缓存和全部单视频偏好，同时保留数据源、凭据、播放记录和全局软件设置）
- [x] 安全播放上下文（Home、详情页、数据源页和队列统一通过 allowlist 只把 `sourceId`、`itemId`、可选 `mediaSourceId` 与短生命周期 `contextId` 写入路由；路由守卫会 replace 清除旧 `path`、标题和 artwork query；播放 URL/header 与本地绝对路径仅在 PlayerView 调用 `getStreamRequest()` 时即时解析或保存在短生命周期内存中；Android 302 回环桥链路保持不变）
- [x] 继续观看功能（本机历史 + provider 原生继续观看聚合，Emby 进度同步后首页刷新）
- [x] 右键播放菜单 + 播放详情 stats 浮层（紧凑用户菜单、播放详情浮层、HDR/SDR/杜比视界动态范围展示；诊断入口不暴露给普通用户）
- [~] 大型视图逻辑拆分（设置数据源契约、播放器安全展示格式化、原始媒体库分类/剧集聚合已分别外提到 service；`SettingsView` 4046→3917 行、`PlayerView` 3369→3244 行、`SourceLibraryView` 2764→2399 行，后续继续按设置分区和播放状态机拆 composable/component）

**产出**:

- [x] 能连接 OpenList/Alist 浏览和播放云盘文件（已通过本地 OpenList/Alist 服务 live test）
- [~] 能连接 CloudDrive2 浏览和播放（原生 gRPC API Token DataSource、Tauri 命令、设置页和播放直链/header 已完成；待真实 CloudDrive2 服务实机验证）
- [~] 能连接通用 WebDAV 浏览和播放（独立 `webdav` DataSource、Basic Auth、设置页、扫描调度和播放 header 已完成；待更多真实 WebDAV 服务兼容性验证）
- [~] 能连接夸克网盘浏览和播放（官方扫码/账号登录、Cookie 凭据、根目录、搜索、扫描和原始直链/header 已完成；待真实账号实机验证）
- [~] 能连接 123 云盘浏览和播放（账号/访问令牌凭据、动态 API 签名、根目录、递归搜索、扫描和临时原始直链/header 已完成；待真实账号实机验证）
- [x] 桌面与 Android 都能通过平台文件选择器打开本地视频并进入播放页；桌面播放页支持拖拽；两端都能把本地文件夹添加为数据源，像 OpenList/Alist 一样浏览、扫描、生成媒体库并播放 root 内视频；文件关联仍待后续
- [~] 夸克网盘与 123 云盘已进入可用 DataSource；115 仍保留占位

---

## Phase 2: Server MVP (Week 9-14)

> 后端最小可用版本 — 先交付安全可用的独立 Web 管理与 RBAC 基础，再从本地 Storage/path safety 开始，按 OpenList/Alist → STRM → signed 302 → Emby/Jellyfin refresh 纵向切片打通刚需存储与播放闭环。115、CloudDrive2、本地文件、PT、追更、AI、插件和更大权限范围仍保留并按依赖顺序接入。

### Sprint 2.1A: 管理端与权限基础（已完成 v0.2 壳层）

- [x] Go 1.22+ module、Gin、SQLite/GORM 与显式版本迁移
- [x] Vue 3 + TypeScript + Vite + Pinia + Router + UnoCSS 独立管理端
- [x] 首次 owner 设置；已有用户时永久关闭 setup；缺 owner 时进入安全恢复状态
- [x] opaque server-side session + HttpOnly/SameSite Cookie、idle/absolute 过期和撤销
- [x] session-bound CSRF、Origin/Referer、Fetch Metadata 与 JSON content-type 防护
- [x] 登录 IP+username 组合限速
- [x] permission catalog、系统/自定义角色、多角色权限并集
- [x] route/nav/button/API 共享 permission code
- [x] owner、最后管理员、自我降权和防权限提升事务不变量
- [x] setup/login/dashboard/users/roles/audit 页面和对应真实 API
- [x] 分组侧栏、统一顶栏、用户管理二级路由、日志中心入口与响应式移动抽屉
- [x] 运维优先的 12 列混合仪表盘；未实现媒体域只显示明确规划/未配置状态
- [x] Vite 开发代理；生产 `webui` build tag 嵌入 `dist`；默认 Go 测试不要求 `dist`

### Sprint 2.1A.1: 本地 Storage 与路径安全基础（已完成）

- [x] `storages` 显式版本迁移、`type=local` 模型、唯一规范化名称/根路径与未来 nullable Connection 引用
- [x] `storages.read/create/update/delete/test` permission catalog、系统角色、API middleware 与管理端控制
- [x] 本地根绝对目录校验、Windows/UNC 兼容、Reparse Point 拒绝、稳定安全错误码
- [x] 只读目录探测、磁盘容量和明确能力快照；不创建探测文件、不递归扫描
- [x] Storage 管理页及 CRUD/test API；删除只删配置，Connection/Destination 保留规划状态
- [x] 独立 `MediaClassificationProfile` v1：内置 Player-v1 等价默认规则、严格结构校验、纯 Go matcher、CRUD/copy/revision、独立权限与管理页；它不是流水线 `CategoryRule`
- [x] Server 统一媒体识别核心：下载完成与 local/115 媒体库扫描共用 Profile 预处理、候选生成、TMDB 验证和分类；根电影/剧集季/BDMV 分组、持久缓存、v25 识别投影、未识别分页/重试/TMDB 人工匹配及目录变更清理已接入
- [x] 权威媒体身份快照：下载前建立 identity，完成后只补全逐文件季集/版本事实，Transfer 仅执行安全校验；manual/direct_id/automatic/ai/local_provisional 来源、revision 与人工锁跨重试和后续阶段保持一致
- [x] MP 式宽容识别与剧集包事实：普通低分/同分稳定选中最高候选并标记 provisional，极低/无候选进入可见待整理；`[01]` / `[01v2]` 等动漫集号按包级证据解析，无法确定的多视频包不再只选最大文件
- [x] 可选 Server AI 识别辅助：默认关闭，支持 OpenAI-compatible 与 Google AI Studio；低分/冲突只仲裁既有候选，极低/无候选只重写查询，严格 Schema、单 revision 调用上限、AES-GCM API Key 和关闭时运行时零请求
- [x] 修正识别并安全重新整理：下载历史、媒体整理和媒体详情支持预览/确认；只操作 Transfer 登记的 managed manifest，绑定 actor/identity/profile/manifest revision，并在本地或 115 重整后重建产物和刷新下游
- [x] 内置预识别词包：固定离线内置 MoviePilot-Help TV/anime 共 322 条有效规则及 MIT/来源/commit/SHA-256，默认 TV→anime→用户规则，支持 Profile 关闭/复制/下载快照、直接 TMDB ID 复验和有界兼容正则
- [x] MediaLibrary 本地只读索引基础：Storage 相对根 + Classification Profile 引用、自动首次全量、独立 watcher/catch-up、定时增量/全量、失败退避/立即重试、扫描记录与相对媒体清单，以及 `/system/media-libraries` 管理页
- [x] Server 持久化任务队列基础：SQLite Job/Attempt/状态事件/ActionRequest/策略、type+priority lane 顺序、类型与资源并发、lease/heartbeat/checkpoint/recovery、控制与 RBAC API，以及真实 `/automation/tasks` 任务中心
- [x] qBittorrent 下载器纵向切片：AES-GCM 凭据/下载源、TMDB 自定义→部署→构建内置凭据优先级及 Read Access Token/API Key 显式双认证、网络故障限定的官方 API 回退、独立测试后启用的 API/图片路由、系统级统一 local 暂存目录、旧/新 add API 与 OMC tag 幂等接管、magnet metadata 暂停、Profile 快照轻量刮削、暂存边界内 category、无法可靠识别时自动归入“未识别”、完成复核、实时 telemetry 与 provider 控制；fake 仅用于开发验收
- [x] 媒体整理任务中心：`transfers.read_own/read_all`、自动 TransferTask 分页/统计/筛选、目标相对命名摘要、Job attempts/timeline/冲突响应、失败阶段重试、下载页详情深链；手动整理归后续文件管理
- [~] MediaLibrary 云 Storage driver/event/cursor、Destination、STRM/302、Transmission 和 metadata 网络匹配；local/115 识别已统一，事件目前合并唤醒完整只读 reconciliation 并以指纹/缓存避免重复 TMDB，provider 级 affected-unit 枚举优化与其余能力由后续独立切片实现

### Sprint 2.1A.2: 115 数据源与云目录基础（进行中）

- [x] 115 Connection、Cookie allowlist、AES-GCM 凭据、账号/容量探测与安全健康摘要
- [x] provider-neutral cloud Driver registry，以及 115 list/stat/DirectURL 只读 adapter 和连接级保守限速
- [x] 统一“数据源”页面：本地/115 类型选择、新建或复用 115 账号、云端目录浏览、未完成账号恢复
- [x] 绑定 actor、Connection、provider directory ID、用途和过期时间的 opaque 云目录令牌
- [x] `type=pan115` Storage：稳定 file ID、显示路径、Connection-scoped 唯一根身份和 capability 快照
- [x] 115 MediaLibrary：支持 Storage 下级目录选择、稳定 provider root 全量/周期 reconciliation、bulk-tree 分页限流与 partial-preserve，以及 Connection-scoped 生活事件增量监听；周期 reconciliation 持续补漏
- [x] MediaLibrary 作品目录：文件事实索引、真实服务端分页/筛选，以及 Movie / Series -> Season -> Episode 聚合详情
- [x] 115 原生离线下载器：复用 Connection Cookie、在所选 115 Storage 根内自由选择下载子目录、生活事件广播立即唤醒完成复核并由低频任务查询补漏、统一任务遥测/取消与完成 manifest 分类；不声明暂停、恢复或做种能力
- [x] 115 云端自动整理：下载时选择同 Connection 的目标 MediaLibrary，完成后按分类与模板执行云端移动/复制/改名，复用四种冲突策略、持久化幂等 checkpoint，并通过 dirty generation 唤醒增量对账
- [x] 115 分享与手工转存接管：媒体库绑定独立中转目录和同账号原生下载器，分享链接转存到稳定任务目录；生活事件只唤醒直接子项 sweep，启动/周期 reconciliation 补漏，并复用统一识别、广告过滤和云端 Transfer
- [ ] 115 STRM 投影、signed 302、文件树差异同步和关联 sidecar 下载；云盘无 STRM 时提供默认关闭的 NFO/JPG 旁挂上传策略

### Sprint 2.1B: OpenList/Alist 可播放纵向切片（下一步）

**目标**：沿 Connection → Storage Destination → STRM Run → signed 302 → Emby/Jellyfin refresh 交付第一个真实媒体闭环。只创建该闭环需要的表、接口和适配器，不横向铺设空 CRUD。

- [ ] OpenList/Alist Connection：加密凭据、连接测试、根路径与最小驱动能力
- [ ] Storage Destination：关联连接、远端路径、受控 STRM 根目录
- [ ] 持久化 STRM Run：可重跑、幂等、逐项错误与原子写入
- [ ] signed URL 续签/轮换策略与安全 302
- [x] Emby/Jellyfin 连接、受控媒体库枚举、明确目标绑定与持久库刷新
- [ ] 可重复的本地端到端演示与真实播放验证

### 原 Sprint 2.1 横向清单（按纵向切片逐步吸收）

**目标**: Server 能启动，三层架构 (连接/存储目标/分类规则) 可用，用户管理可用

#### Go 项目初始化

- [x] Go module 与当前切片需要的目录结构
- [x] 配置 `go.mod` 依赖
- [x] 编写 `cmd/server/main.go` 入口
- [ ] 配置管理 (`internal/config/`) — Viper + YAML
- [x] 日志系统 — zerolog 结构化双写、统一脱敏、JSONL 轮转/gzip/保留、细粒度查询/导出/配置 RBAC 与 Web 日志中心

#### Web 框架搭建

- [x] Gin 路由初始化
- [ ] 中间件: CORS, Logger, Recovery
- [x] Cookie Session 认证、CSRF 与 permission 中间件
- [x] 统一错误响应格式
- [~] 统一分页格式（MediaLibrary entries/catalog 与 transfers 已使用真实 `list,total,page,page_size`，其余列表逐步迁移）
- [ ] API 版本路由 (`/api/v1/`)

#### 数据库层

- [x] GORM 初始化 + SQLite 连接
- [x] 显式递增版本迁移（不以 AutoMigrate 代替长期 schema）
- [ ] 数据模型定义 (`internal/models/`)
  - [ ] `Connection` 模型
  - [ ] `StorageDestination` 模型
  - [ ] `CategoryRule` 模型
  - [ ] `User` 模型
  - [ ] `Setting` 模型

#### 连接管理

- [ ] `Connection` CRUD API (`internal/handlers/connection.go`)
- [ ] `POST /api/v1/connections` — 添加连接
- [ ] `GET /api/v1/connections` — 连接列表
- [ ] `PUT /api/v1/connections/{id}` — 更新连接
- [ ] `DELETE /api/v1/connections/{id}` — 删除连接
- [ ] `POST /api/v1/connections/{id}/test` — 测试连接
- [ ] 连接信息加密存储 (AES-GCM)

#### 存储目标

- [ ] `StorageDestination` CRUD API (`internal/handlers/destination.go`)
- [ ] `POST /api/v1/destinations` — 添加存储目标
- [ ] `GET /api/v1/destinations` — 存储目标列表
- [ ] `PUT /api/v1/destinations/{id}` — 更新
- [ ] `DELETE /api/v1/destinations/{id}` — 删除
- [ ] 本地/网盘类型区分
- [ ] STRM 配置字段 (strm_enabled, strm_output_path, strm_base_url)

#### 分类规则

- [ ] `CategoryRule` CRUD API (`internal/handlers/category.go`)
- [ ] `POST /api/v1/categories` — 添加分类规则
- [ ] `GET /api/v1/categories` — 分类规则列表
- [ ] `PUT /api/v1/categories/{id}` — 更新
- [ ] `DELETE /api/v1/categories/{id}` — 删除
- [x] MediaLibrary 电影/剧集目录模板和命名模板字段
- [~] 转移策略字段（本地 move/copy/symlink 已实现；hardlink 与云盘策略待后续）
- [x] MediaLibrary 全局排序字段、拖放和可访问顺序按钮

#### 用户管理

- [x] users/roles/permissions/user_roles/role_permissions/sessions 模型 + bcrypt
- [x] setup/login/logout/me/csrf API
- [x] 用户创建、资料、启停、角色分配、密码重置与删除 API
- [x] 自定义角色与权限矩阵 API
- [x] permission code 强制鉴权
- [x] 首次网页创建唯一 owner，不创建默认账号或密码

#### 本地运行与部署准备

- [x] Server 本地运行入口与单二进制 Web UI 嵌入方向
- [ ] 基础配置文件可用 (`configs/config.example.yaml`)
- [ ] 健康检查端点 (`GET /api/v1/health`)
- [ ] 编写 `Dockerfile` (后续部署准备，可不作为本地开发前置)
- [ ] 编写 `docker-compose.yaml` (后续 NAS/服务器部署准备，可选)

**产出**:

- [x] 健康检查和管理基础代码完成
- [ ] 三层架构业务 API 随 2.1B 纵向闭环实现
- [x] 用户登录/权限控制可用
- [ ] 连接信息加密存储

### Sprint 2.2: 网盘驱动 + 下载器 + 302代理 (Week 11-12)

**目标**: 网盘驱动可用，下载器能连接，302代理能播放，媒体服务器能通知刷新

#### 网盘驱动抽象层

- [x] 定义 provider-neutral `Driver`、Item、Account、TemporaryURL 与 capability 接口 (`pkg/cloud/client.go`)
- [x] 实现驱动注册机制 (`pkg/cloud/client.go`)
- [ ] 实现 `AlistDriver` (`pkg/cloud/alist/`，兼容 OpenList/Alist API)
  - [ ] HTTP API 客户端 (`/api/fs/list`, `/api/fs/get`, `/api/fs/search`)
  - [ ] `List()`, `Get()`, `Upload()`, `GetDownloadURL()`, `Search()`
  - [ ] 连接测试 (`IsAlive`)
- [~] 实现 `115Driver` (`pkg/cloud/pan115/`)
  - [x] Cookie allowlist 认证与账号探测
  - [~] 文件分页列表、属性、临时下载链接、原生离线下载及受控 mkdir/move/copy/rename/recycle 已实现；搜索与其它通用写操作后续接入
- [ ] 实现 `AliyunDriver` (`pkg/cloud/aliyun/`)
  - [ ] Token 认证
  - [ ] 文件列表/搜索/下载链接

#### 下载器管理

- [x] 定义 provider-neutral `Client`、`Source`、`SubmitRequest`、`Task` 与 capability registry (`pkg/downloader/client.go`)
- [x] 实现 `QBittorrentClient` (`pkg/downloader/qbittorrent/`)
  - [x] Cookie-based 认证与受控 HTTP client
  - [x] magnet/HTTP(S) URL 与内存 `.torrent` 提交
  - [x] task/tag 查询、进度/上下行速度/ETA telemetry
  - [x] pause/resume/cancel；取消经二次确认后固定 `deleteFiles=true`，provider 确认后清理本地任务事实
  - [x] 持久 Job worker、lease 恢复和 provider reconciliation
- [ ] 实现 `TransmissionClient` (`pkg/downloader/transmission/`)
  - [ ] RPC 认证
  - [ ] 同上接口实现
- [x] 下载器 CRUD/test API 与独立 RBAC (`internal/handlers/downloaders.go`)
- [x] `POST/GET/DELETE /api/v1/downloads` 与磁力、URL、4 MiB 种子上传管理页；failed/cancelled 可安全删除，provider 已手动删除时幂等清理

#### 302代理引擎

- [ ] 实现 `Engine` (`pkg/proxy/engine.go`)
- [ ] 路由: `GET /proxy/{driver}/{path...}`
- [ ] URL 缓存 (TTL 机制)
- [ ] 302 重定向逻辑
- [ ] CORS 支持
- [ ] 错误处理 (驱动不存在/文件不存在)

#### 媒体服务器客户端

- [x] 定义 provider-neutral `mediaserver.Client` 接口 (`pkg/mediaserver/client.go`)
- [~] 实现 `EmbyClient` (`pkg/mediaserver/emby/`)
  - [x] `Probe()` — 测试连接
  - [x] `RefreshLibrary(libraryID)` — 刷新媒体库
  - [x] `ListLibraries()` — 获取受控媒体库列表
  - [ ] `Search(keyword)` — 搜索
- [x] 实现 `JellyfinClient` (`pkg/mediaserver/jellyfin/`)，覆盖探测、媒体库枚举和刷新

#### 配置同步 API

- [ ] `POST /api/v1/sync/push` — Player 推送数据源配置
- [ ] `GET /api/v1/sync/pull` — Player 拉取 Server 配置
- [ ] `GET /api/v1/sync/status` — 同步状态
- [ ] 用户显式选择方向和范围的结构化配置同步；不自动导入，不默认同步凭据

**产出**:

- [ ] 网盘文件浏览/上传可用
- [ ] qBit/Transmission 能连接和控制
- [ ] 302 代理能重定向到云盘 CDN
- [x] Emby/Jellyfin 能通过 API 和持久目标 Job 刷新明确绑定的媒体库

### Sprint 2.3: 媒体流水线 + STRM + 元数据 (Week 13-14)

**目标**: 完整的 下载→转移→入库 流水线跑通

#### 元数据刮削

- [~] TMDB API 客户端 (`pkg/metadata/tmdb/`)（Server 已实现电影/剧集搜索、按 ID 读取、候选搜索、v3 API Key/v4 Token 与安全路由；credits/images 完整详情仍待补）
  - [x] `Search(title, year)` — 搜索电影/剧集并比较本地化/原始标题
  - [~] `GetDetail(tmdbId)` — 已支持识别所需按 ID 验证；credits/images 完整详情待补
  - [ ] `GetByIMDBID(imdbId)` — 通过 IMDB ID 查询
  - [ ] 图片 URL 构建 (poster, backdrop)
- [~] 文件名解析器（现位于共享识别/媒体库分组层，后续可再下沉为独立 `pkg/metadata` API）
  - [x] 标题提取
  - [x] 年份提取
  - [ ] 分辨率/编码/来源提取
  - [x] 季/集号提取 (剧集)
  - [ ] 制作组提取
- [ ] NFO 生成器 (XML 格式)
- [ ] 海报/背景图下载

#### 文件转移引擎

- [x] `TransferService` (`internal/services/transfer.go`)
- [x] 下载完成 manifest 复核并幂等创建独立 transfer Job
- [x] 同一 115 Connection 内的云端 move/copy、冲突回收/跳过/改名/询问、重启幂等和媒体库 dirty-generation 对账
- [~] 自动分类匹配逻辑
  - [ ] 优先: 站点分类
  - [ ] 次选: 文件名解析 (有 season → tv)
  - [ ] 兜底: TMDB 查询确认
- [~] 目标路径构建（按目标 MediaLibrary 快照的分类与模板）
  - [~] 变量替换: `{category}`, `{title}`, `{year}`, `{season:02}`, `{episode:02}`（resolution 待后续）
  - [x] 扩展名保留，并携带同名 srt/ssa/ass/jpg
- [~] 转移策略执行
  - [x] `move` — 同盘原子移动，跨盘复制校验后删除源文件（默认）
  - [ ] `hardlink` — 硬链接 (保种)
  - [x] `copy` — partial + sync + 原子改名复制
  - [x] `symlink` — 软链接（明确依赖暂存源）
- [x] 转移任务记录 (`transfer_tasks` 表) 与私有 manifest
- [x] 完整下载清单与安全入库清单的精确差集清理；识别/转移/对账失败时保留，qBittorrent copy/symlink 做种结束前延后，115 只回收精确 item ID
- [x] ask/overwrite/skip/rename 冲突策略；ask 等待时释放 worker slot
- [x] 转移失败沿持久任务队列保留事实；`/automation/organization` 提供专用筛选、详情、冲突响应和精确阶段重试

#### 通知服务

- [x] 统一 `MediaChangeService`：事务 content revision、ready/pending outbox、artifact readiness 与有界保留
- [x] Emby/Jellyfin 刷新通知：目标级持久 Job、合并、重试、恢复与手动执行
- [x] Player 客户端通知：device Bearer 长轮询、持久 cursor、`resync_required` 与权限过滤
- [x] 通知变化类型：受控 `catalog` / `metadata` / `artifacts` / `removed`，不从下载或转移中间事件直接发布

#### STRM 管理器

- [x] 基于 `MediaArtifactService` 的持久化 STRM 生成与 manifest 所有权
- [ ] `GenerateOne()` — 生成单个 STRM 文件
  - [ ] 内容: 302 代理 URL (`http://server:3000/proxy/{driver}/{path}`)
  - [ ] 目录结构: `{dest}/{title} ({year})/{filename}.strm`
- [x] 手动增量刷新与全量重建通过 `strm_reconcile` 持久任务触发媒体库 generation 对账
- [x] 失效托管产物清理预览、短时确认令牌与投影根边界删除
- [x] 完整成功且非 partial 的全量/增量 generation 自动清理旧托管产物，失败/根变化时安全保留
- [x] 115 signed 302 默认双设备 lease：首设备原文件、次设备一个短命受控副本、第三设备限流，并按持有 item ID 精确清理回收站
- [ ] Emby Web 4.9.x 302 播放兼容：固定播放器资源移除远程 DirectPlay `crossOrigin=anonymous`，固定 HTML 壳加载同源兜底脚本覆盖旧模块缓存；自动化路由测试已通过，待真实 Emby Web 播放复验
- [ ] STRM 定时任务配置 (`strm_schedules` 表)
  - [ ] 增量同步 cron
  - [ ] 全量扫描 cron
  - [ ] 无效清理 cron
- [x] STRM 管理 API 与 `/automation/strm` 页面
  - [x] 启用 STRM 的媒体库、Run 历史和 managed artifact 分页
  - [x] 单库增量、全量、失败重试
  - [x] 清理 preview + confirmation execute；unmanaged 文件永不删除

#### 部署配置（后置，不阻塞本地开发）

- [ ] `docker-compose.yaml` 编写 (用于 NAS/服务器部署与后续集成测试)
  - [ ] ohmycine-server 服务
  - [ ] emby 服务 (可选)
  - [ ] qbittorrent 服务 (可选)
  - [ ] 共享卷配置 (STRM 库目录)

#### Player 端 Server 连接 UI

- [x] 添加 Server 连接表单（URL + 用户名/密码首次认证，换取可撤销 device token）
- [x] `ServerDataSource` 状态、媒体库、目录、搜索与详情
- [x] TMDB/Emby SystemId/Artifact 身份聚合，保留 Server 直连与 Player 自有 Emby 用户线路
- [x] active 115 STRM 通过受保护 Player stream endpoint → Server 解析 → 302 直连，Windows/Android 跨源跳转清除 Bearer
- [x] Bilibili DASH 视频/音频双轨 loopback 会话与 Range 播放，切换/停止后统一回收旧 token
- [x] Server 本地/115/插件库动态合成封面与 Player 独立目录本地封面，支持内容 revision、安全同源分发和静态兜底
- [x] Server ready change 只失效对应 ServerDataSource，后台刷新聚合首页；当前列表提示后原位刷新并恢复滚动
- [ ] 设备管理 Web UI、多设备设置/进度同步和数据源配置同步（延期，当前连接不会自动同步）

**产出**:

- [ ] 下载完成 → 自动转移 → 自动生成 STRM → 通知 Emby 刷新 → Emby 能播放
- [ ] STRM 定时增量/全量/清理可用
- [x] Player 连接 Server 后可浏览并直连 Server 管理的媒体；Player 脱离 Server 时其它 DataSource 不受影响
- [ ] Player ↔ Server 配置同步（延期，必须由用户显式开启，不自动同步）

---

## Phase 3: 核心功能增强 (Week 15-22)

> 补全核心功能 — 在 Server 刚需闭环稳定后，继续实现发现页、PT/BT 聚合搜索、追更、AI助手、网盘增强、Cinema OS UI 等完整产品能力

### Sprint 3.1: 发现页 + PT/BT 站点实现 (Week 15-17)

**目标**: 发现页可用，主流 PT 与公开 BT 站点接入，聚合搜索 + 一键下载

#### PT/BT 站点框架

- [x] 定义版本化 `Site` adapter 接口与统一搜索 DTO (`pkg/site`)
- [x] 定义 `Config`, `Query`, `Page`, `Result` 与稳定错误分类
- [x] 内建 adapter 注册机制
- [x] 站点管理 API（首版管理入口仍仅系统管理员）
  - [x] `POST /api/v1/sites` — 测试候选配置并添加
  - [x] `GET /api/v1/sites` — 脱敏站点列表
  - [x] `PATCH /api/v1/sites/{id}` — revision CAS 更新
  - [x] `DELETE /api/v1/sites/{id}` — 删除
  - [x] `POST /api/v1/sites/{id}/test` — 测试连接

#### PT 站点实现

- [x] PTTime 首个内建适配器
  - [x] Cookie 登录态检测与加密配置
  - [x] NexusPHP 兼容分页搜索和脱敏 fixture 解析
  - [x] 受控种子获取、大小/类型边界和错误分类
  - [~] 媒体类型候选已进入统一查询 DTO；真实站点分类 ID 映射待账户联调后固化在 adapter 内
- [x] SewerPT（下水道）内建 NexusPHP 适配与 CookieCloud 自动发现
- [x] PandaPT（熊猫高清）内建 NexusPHP 适配与 CookieCloud 自动发现
  - [x] 嵌套标题表格去重并保留外层大小、做种、下载与完成统计
  - [x] 普通视频 `torrents.php` 搜索；音频 `special.php` 专区仍待独立契约

- [ ] M-Team (馒头) 站点适配器
  - [ ] Cookie 认证
  - [ ] 搜索 API 解析
  - [ ] 种子详情解析
  - [ ] 分类映射
- [ ] HDSky 站点适配器
  - [ ] Cookie 认证
  - [ ] 搜索 API 解析
- [ ] OurBits (我堡) 站点适配器
  - [ ] Cookie 认证
  - [ ] 搜索 API 解析

#### 公开 BT 与聚合索引

- [x] Nyaa 内建 RSS 适配器
- [x] AnimeTosho 内建 RSS 适配器
- [x] Tokyo Toshokan 内建 RSS 适配器
- [x] Mikan 内建 RSS 适配器
- [x] AniDex 内建 RSS 适配器
- [x] 动漫花园（DMHY）、ACG.RIP、YTS、EZTV 内建适配器
- [x] 1337x、The Pirate Bay、EXT.to、LimeTorrents 内建适配器
- [x] 地址驱动添加：管理员输入规范 HTTPS 官网，Server 精确解析 host 并在创建时二次校验；未配置的公共 BT 站不展示、不探测、不搜索
- [x] `POST /api/v1/sites/resolve`，拒绝 userinfo/query/fragment、非根路径、异常端口、相似域名、伪造子域和客户端伪造 kind
- [x] 通用 Torznab 连接（Jackett/Prowlarr）
  - [x] API Key 使用站点专用 AES-GCM envelope 加密保存
  - [x] HTTPS、同源 torrent 与受控重定向约束
- [x] BT torrent/magnet 仅在 Server 内解析并复用既有下载、整理和入库流水线
- [x] 通用 `/api/v1/discovery/torrent-search*` 搜索/识别路由，保留旧 PT 路由兼容

#### 发现页聚合搜索

- [x] 推荐 `DiscoveryService`：TMDB + 豆瓣栏目、缓存和来源级降级
- [x] PT/BT `SiteService`：有界并发搜索所有已启用站点 (`goroutine` + `channel`)
- [x] SSE 按站渐进结果、普通 JSON 回退、单站重试与分页
- [x] 站点卡片单站搜索：固定 `site_id` 贯穿搜索、重试、分页、会话恢复、识别和入库，目标站失败不回退聚合搜索
- [ ] 结果聚合 + 去重
- [x] 统一媒体身份匹配：结构化解析、TMDB 候选排序/复验、稳定 provisional 兜底及人工 locked identity
- [ ] 结果排序 (相关度/做种数/大小)
- [ ] 筛选/过滤 (分类/分辨率/大小范围/做种数)
- [ ] 搜索 API
  - [ ] `POST /api/v1/discovery/search` — 聚合搜索
  - [ ] `GET /api/v1/discovery/trending` — 热门资源
  - [ ] `GET /api/v1/discovery/latest` — 最新资源

#### 一键下载

- [x] 自动分类匹配（站点结果 → 统一 identity → 目标媒体库 Profile）
- [x] 确定下载目录（统一暂存目录 + 用户选择或媒体库排序 + Transfer 目标快照）
- [x] 不透明结果令牌换取种子并提交到既有下载器服务
- [x] 复用现有下载任务记录、用户归属、媒体库排序和后续整理入库
- [ ] WebSocket 进度推送
- [ ] 下载 API
  - [ ] `POST /api/v1/discovery/download` — 一键下载
  - [ ] `GET /api/v1/downloads` — 下载任务列表 (用户隔离)
  - [ ] `POST /api/v1/downloads/{id}/pause` — 暂停
  - [ ] `POST /api/v1/downloads/{id}/resume` — 恢复
  - [ ] `DELETE /api/v1/downloads/{id}` — 删除

#### Player 端发现页 UI

- [ ] `DiscoveryView.vue` — 发现页
- [ ] 搜索栏 + 筛选器 (分类/分辨率/大小)
- [ ] 搜索结果列表 (来源/标题/大小/做种/制作组)
- [ ] TMDB 元数据展示 (海报/评分/简介)
- [ ] 一键下载按钮
- [ ] 下载进度显示

**产出**:

- [x] 能跨站点聚合搜索，并可从站点卡片固定单站搜索
- [x] 搜索结果通过统一 identity service 自动匹配 TMDB 元数据或进入可见暂定状态
- [x] 一键下载 → 自动分类 → 自动转移 → 自动入库

### Sprint 3.2: 追更 + 网盘增强 (Week 18-19)

**目标**: 追更可用，更多网盘支持，302代理增强

#### 追更引擎

- [ ] `FollowService` (`internal/services/follow.go`)
- [ ] 追更任务模型 (`follow_tasks` 表)
- [ ] 创建追更任务
  - [ ] TMDB ID + 剧名 + 季号
  - [x] 站点过滤（现有搜索合同支持固定 `site_id`；追更任务的筛选 UI/调度绑定仍待实现）
  - [ ] 质量偏好 (分辨率/编码/制作组)
  - [ ] Cron 表达式 (默认每天 3:00)
- [ ] 定时执行逻辑
  - [~] 在指定站点搜索剧名/IMDB ID（单站搜索基础已完成；追更调度仍待实现）
  - [ ] 过滤缺少的集数 (对比 TMDB 总集数 vs 本地已有)
  - [ ] 匹配质量偏好
  - [ ] 选择最佳种子
  - [ ] 提交下载
- [ ] 追更状态管理 (active/paused/completed)
- [ ] 追更 API
  - [ ] `POST /api/v1/follows` — 创建追更
  - [ ] `GET /api/v1/follows` — 追更列表 (用户隔离)
  - [ ] `PUT /api/v1/follows/{id}` — 更新
  - [ ] `DELETE /api/v1/follows/{id}` — 删除
  - [ ] `POST /api/v1/follows/{id}/pause` — 暂停
  - [ ] `POST /api/v1/follows/{id}/resume` — 恢复
  - [ ] `POST /api/v1/follows/{id}/execute` — 立即执行

#### 网盘驱动增强

- [ ] 实现 `QuarkDriver` (`pkg/cloud/quark/`)
- [ ] 实现 `BaiduDriver` (`pkg/cloud/baidu/`)
- [ ] 实现 `TianyiDriver` (`pkg/cloud/tianyi/`)
- [ ] 实现 `UCDriver` (`pkg/cloud/uc/`)
- [ ] 实现 `WebDAVDriver` (`pkg/cloud/webdav/`)

#### 302代理增强

- [ ] 多网盘统一代理路由
- [ ] URL 健康检查 (定期验证缓存的 URL 是否有效)
- [ ] 自动故障转移 (一个驱动失败 → 尝试备用驱动)

#### Player 端 UI

- [ ] `FollowView.vue` — 追更管理页面
  - [ ] 追更列表 (剧名/当前进度/站点/下次检查)
  - [ ] 操作按钮 (暂停/恢复/编辑/删除/立即执行)
  - [ ] 追更详情 (已追集数/缺少集数/下载历史)
- [ ] 网盘文件浏览器
  - [ ] 目录树导航
  - [ ] 文件列表 (名称/大小/修改时间)
  - [ ] 文件操作 (上传/删除/移动)

**产出**:

- [ ] 能追更剧集，自动下载缺少的集数
- [ ] 7+ 个网盘驱动可用
- [ ] 302 播放稳定可靠

### Sprint 3.3: AI助手 + Cinema OS UI (Week 20-22)

**目标**: AI 推荐可用，沉浸式 UI 完善，文件管理可用

#### AI 助手 (Player 侧)

- [ ] AI Provider 抽象层
  - [ ] OpenAI 兼容接口 (默认)
  - [ ] Claude 支持
  - [ ] 自定义 Base URL (本地 LLM)
- [ ] 用户 API Key 配置
- [ ] RAG 架构实现
  - [ ] `MediaIndexer` — 媒体库索引 (为每部影片生成文本描述)
  - [ ] `LocalVectorStore` — 本地向量存储 (余弦相似度搜索)
  - [ ] Embedding 生成 (text-embedding-3-small)
  - [ ] 向量索引持久化
- [ ] `AIRecommendService`
  - [ ] `recommend(query)` — 自然语言推荐
  - [ ] 检索增强: 从本地库中检索相关影片
  - [ ] Prompt 构建: 系统提示 + 检索结果 + 用户问题
  - [ ] LLM 调用: 生成推荐结果
- [ ] AI 设置页面
  - [ ] Provider 选择
  - [ ] API Key 输入
  - [ ] Model 选择
  - [ ] Base URL 配置
  - [ ] Embedding 模型配置

#### Cinema OS UI 完善

- [~] CSS Variables 设计 Token (`variables.css`)
  - [~] 色彩系统 (主色/强调色/中性色/语义色)
  - [~] 液态玻璃变量 (bg/border/blur/shadow)
  - [~] 圆角/间距/字体/动画变量
- [~] 液态玻璃组件库 (`glass.css`)
  - [~] `.glass` 基础液态玻璃
  - [~] `.glass-card` 悬停光晕效果
  - [~] `.datasource-sidebar-glass` 动态数据源侧栏玻璃
  - [~] `.player-controls-glass` 播放控制条玻璃
- [~] 布局系统
  - [~] `AppLayout.vue` — 主布局 (动态数据源侧栏+内容区+窗口控制)
  - [~] `DataSourceSidebar.vue` — 按绑定顺序渲染首页、数据源和设置入口
  - [~] `WindowChrome.vue` — 无边框窗口拖拽与控制按钮
  - [ ] `StatusBar.vue` — 状态栏
- [~] 动画系统
  - [ ] 页面切换动画 (Motion Vue)
  - [~] 悬停光晕动画 (CSS + JS)
  - [~] 列表项进入动画
- [~] 首页 (`HomeView.vue`)
  - [~] Hero Carousel (跨数据源聚合轮播，自动/手动切换)
  - [x] 继续观看面板 (本机/Emby 聚合播放进度、剧集标题排布、下一集/续播入口已接入)
  - [~] 最新影片面板 (海报+名称)
- [~] 单数据源媒体库页 (`SourceLibraryView.vue`)
  - [x] 数据源级 Hero Carousel (当前来源元数据与 raw scan 聚合 Hero 已接入)
  - [~] 媒体库分组 (电影/剧集/文件夹)
  - [x] 库内海报墙与详情浏览
- [~] 媒体展示组件
  - [x] `MediaCard.vue` — 媒体卡片 (海报+信息)
  - [x] `MediaGrid.vue` — 网格布局
  - [ ] `MediaRow.vue` — 横向滚动行
  - [ ] `MediaDetail.vue` — 媒体详情面板
  - [ ] `PosterWall.vue` — 海报墙
  - [x] `HeroCarousel.vue` — 首页/数据源页大图轮播
  - [ ] `ContinueWatchingPanel.vue` — 继续观看面板

#### 文件管理页面

- [ ] `FileView.vue` — 文件管理页面
- [ ] 数据源选择器
- [ ] 目录树导航
- [ ] 文件列表 (名称/大小/修改时间/类型)
- [ ] 文件操作 (上传/删除/移动/重命名)
- [ ] 文件详情面板 (大小/路径/关联媒体)

#### 快捷键系统

- [ ] `useKeyboard()` composable
- [~] 播放控制快捷键（已实现点击画面/Space 播放暂停、左右方向键短按 5 秒跳转、右键长按临时倍速和左键长按连续后退；音量、静音等其余按键待后续）
- [ ] 字幕/音轨快捷键 (S, A)
- [~] 弹幕快捷键（已实现 D 开关、Shift+D 打开设置；透明度快捷键待后续）
- [ ] 窗口快捷键 (F, Escape, P)
- [x] 导航快捷键（首页、设置、数据源管理和每个动态媒体源入口可独立捕获、保存、清空和恢复默认）
- [x] 快捷键冲突检测（拒绝重复导航绑定，并保留 Space、左右方向键和 Escape 给播放器）

#### 弹幕系统

- [x] 弹幕数据格式定义（统一滚动/顶部/底部类型，兼容弹弹play `p` 标准字段）
- [ ] B 站 XML 弹幕解析器
- [x] JSON 弹幕解析器（弹弹play v2 comment 响应）
- [x] 弹弹Play API 弹幕源（官方 API + 自定义兼容根地址、签名鉴权与限次 302）
- [ ] 本地弹幕文件自动匹配 (同目录同名 .xml/.json)
- [~] Canvas 弹幕渲染引擎（桌面/Android 共享，播放时间驱动）
  - [x] 基于显示区域与密度的确定性轨道分配
  - [x] 滚动弹幕动画
  - [x] 顶部/底部固定弹幕
  - [ ] 弹幕碰撞检测
- [x] 弹幕设置面板（桌面/Android 开关、透明度、字号、速度、区域、密度、类型和关键词屏蔽）
- [~] Tauri Commands（已实现安全的远程匹配/弹幕获取；本地 XML/JSON 文件命令待本地弹幕阶段）

#### 整体优化

- [~] 性能优化 (首页/数据源根页已接入 5 分钟会话快照与保留旧内容的后台刷新；海报/背景使用应用私有 `cache/images`、IntersectionObserver 懒加载和默认 500 MB 可配置 LRU 上限；虚拟滚动和更细粒度列表分页待后续)
- [ ] 错误处理完善 (统一错误边界)
- [x] Server 日志系统增强（结构化日志、模块/组件/插件筛选、日志轮转压缩与容量保留）；Player 独立日志增强仍按 Player 任务推进
- [ ] 国际化完善 (中英文完整翻译)

**产出**:

- [ ] AI 能基于本地库推荐电影
- [ ] 液态玻璃 UI 流畅
- [ ] 文件管理跨数据源可用
- [~] 快捷键系统完整（播放器基础控制和可配置导航已完成，字幕/音轨、弹幕、窗口与更多播放按键待后续）

---

## Phase 4: 生态系统 (Week 23+, 持续迭代)

> CLI、插件市场、社区建设

### Sprint 4.1: CLI 工具 (Week 23-24)

- [ ] omc CLI 框架 (Cobra)
- [ ] `omc server start/stop/status` — 服务器管理
- [ ] `omc config get/set/list` — 配置管理
- [ ] `omc library list/scan` — 媒体库管理
- [ ] `omc cloud list/test` — 网盘管理
- [ ] `omc search <keyword>` — 资源搜索
- [ ] `omc download add/list/cancel` — 下载管理
- [ ] `omc strm sync/clean/status` — STRM 管理
- [ ] `omc doctor` — 系统诊断
- [ ] Shell 补全 (Bash/Zsh/Fish/PowerShell)
- [ ] Man page 生成

### Sprint 4.2: 插件系统 (Week 25-28)

- [ ] Server 通用 WASM 插件引擎、GitHub Registry/Release 仓库与权限生命周期（Bilibili 为首个真实参考插件，不限制后续元数据、通知、下载器、存储、媒体服务器、自动化和规则扩展）
- [ ] 插件接口定义
- [ ] 插件生命周期管理 (Init/Start/Stop)
- [ ] 事件总线 (插件间通信)
- [ ] 插件配置管理
- [ ] Hub 网站 (VitePress)
  - [ ] 插件列表页
  - [ ] 插件详情页
  - [ ] 开发者文档
  - [ ] 安装指南
- [ ] 预置插件
  - [ ] Telegram 通知插件
  - [ ] Server 酱通知插件
  - [ ] 115 网盘增强插件

### Sprint 4.3: Android + 持续优化 (Week 29+)

- [x] Tauri Android 构建配置（已生成 Android Studio 工程并通过 ARM64 debug APK 预览构建）
- [~] libmpv Android 集成（ARM64 已通过官方 mpv-android runtime + Kotlin Tauri Plugin + 原生 `SurfaceView` 接入同名播放命令；已补 Surface 延迟就绪屏障、待播请求、初始化错误回传、自动横屏沉浸模式和触摸优先控制布局，APK/JNI 静态验证通过；真机画面、硬解、远程 header、字幕、seek 与生命周期待复验，其他 ABI 与可复现自建 runtime 后续完成）
- [~] 移动端 UI 适配（已完成独立手机外壳、统一规格底部导航、媒体库/快捷底部抽屉、触屏海报操作、手机设置列表、紧凑液态玻璃播放控制、首页下拉全屏聚合搜索、全屏字幕搜索、可配置横向卡片/竖向列表的手机选集页、Android 原生视频底层，以及 SAF 本地文件/媒体目录选择；首页/来源展示快照进入 SQLite 并在进程重建后先恢复再后台刷新，海报写入应用私有 `cache/images`；单视频音轨/字幕/倍速/画面亮度偏好通过 SQLite 恢复，缓存外部字幕等待 `video-ready` 后加载，禁止 PlayerView 高频原生轨道轮询；真机系统交互与授权撤销恢复仍待复验）
  - [~] 触摸手势（已完成横向快退/快进、Android Activity 屏幕亮度、Windows DDC/CI + WMI 显示亮度、右侧音量、单击控制 UI、双击暂停、左右半屏静止长按复用方向键连续后退/临时倍速，并加入顶部/底部系统手势保护与方向优势判断；Windows 不支持亮度协议的显示器会明确提示，捏合手势仍待完成）
  - [~] Android 后台播放（已接入可关闭的前台媒体服务、MediaSession 进度与播放/暂停/seek/前后 10 秒系统控制；通知权限拒绝、自然结束、后台返回和不同系统厂商真机行为待复验）
  - [x] 底部导航栏与媒体库/快捷抽屉
  - [x] 替换手机端 hover-only 全局与媒体操作
  - [~] 横屏播放（已完成顶部工具、中央小型三键、低高度底部时间轴/工具坞和横竖屏面板重排；Android 原生渲染与系统栏避让仍待真机复验）
  - [~] 安全区域适配（应用外壳底部已接入 `safe-area`，播放器横屏和系统栏避让待 Android 实机完成）
  - [ ] 平板与多窗口适配（按 compact / medium / expanded 实时窗口宽度切换，覆盖横竖屏、左右分屏、桌面模式与折叠屏窗口变化）
- [ ] 性能优化 (启动时间/内存/渲染)
- [ ] 国际化完善 (日文/韩文)
- [ ] 社区建设
  - [ ] Discord / QQ 群
  - [ ] 贡献者指南完善
  - [ ] Issue 模板 (Bug/Feature/Question)
  - [ ] PR 模板
- [ ] 文档完善
  - [ ] 用户文档
  - [ ] 开发者文档
  - [ ] API 文档 (OpenAPI)

---

## 里程碑时间线

```
2026 Q2 (May-Jun)
  └─ Phase 0 + Phase 1 完成
     Player 独立版可用，聚合首页 + 动态数据源侧栏，Emby/Jellyfin/OpenList/Alist/CloudDrive2 原生连接

2026 Q3 (Jul-Sep)
  └─ Phase 2 完成
     Server 三层架构 + 媒体流水线 + 302代理 + 配置同步

2026 Q4 (Oct-Dec)
  └─ Phase 3 完成
     发现页 + 追更 + AI助手 + 网盘增强 + Cinema OS UI

2027 Q1 (Jan-Mar)
  └─ Phase 4 初步完成
     CLI + 插件系统 + Android + 社区生态
```

## 技术风险与应对

| 风险                 | 影响             | 应对                              |
| -------------------- | ---------------- | --------------------------------- |
| libmpv 库体积        | 安装包增加 ~30MB | 按平台动态链接，构建时自动下载    |
| 网盘 API 不稳定      | 网盘功能不可用   | 多驱动容错、自动降级到 OpenList/Alist 代理 |
| PT 站点反爬          | 站点功能失效     | 社区维护 Cookie、适配器热更新     |
| Tauri Android 成熟度 | 移动端体验差     | 备选方案：Flutter/原生 Android    |
| 插件安全             | 恶意插件         | 签名验证 + 沙箱执行 + 社区审核    |
| 法律风险(PT站点)     | 合规问题         | 不内置站点列表，用户自行配置      |
| 追更误判             | 下载错误资源     | IMDB ID 精确匹配 + 人工确认选项   |
