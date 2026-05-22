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
- [~] Tauri 桌面构建 CI: Windows / macOS / Linux 构建测试包
- [~] Hub 文档站 CI: VitePress build
- [~] 上传 GitHub Actions Artifacts，方便下载本地测试

#### 手动测试构建（MVP 阶段）

- [~] `workflow_dispatch` 手动触发构建
- [~] 支持选择构建组件: Player / Server / CLI / Hub
- [~] 支持选择构建平台: Windows / macOS / Linux
- [~] 构建产物保留 7-30 天供测试下载

#### 发布期 CI（后置）

- [ ] Docker 构建 CI: `docker build`
- [ ] Docker 镜像推送: GHCR / Docker Hub
- [ ] Release CI: tag 触发 GitHub Releases
- [ ] 自动生成 changelog
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

> Player 独立可用版本 — 无需 Server，原生连接 Emby/Jellyfin/OpenList/Alist/CloudDrive2

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
- [x] 配置 `tauri.conf.json` (窗口配置、权限、打包)

#### libmpv 嵌入集成

- [~] 下载 libmpv 二进制库 (Windows/macOS/Linux；Windows setup 已补充 GNU import library `libmpv.dll.a` 与运行时 `libmpv-2.dll`；WSL Windows GNU cross-build 已通过，Windows 宿主透明叠层 + mpv 视频底层窗口播放已验证；macOS/Linux 打包与运行时仍待后续复验)
- [x] 创建 `src-tauri/src/mpv/` 模块
- [x] 实现 `libmpv-sys` FFI 绑定 (C API 调用)
- [x] 实现 `MpvPlayer` 结构体 (封装所有 MPV 操作)
- [x] 实现 Windows 内嵌视频渲染后端（透明 Tauri/WebView 叠层 + mpv `wid` 视频底层 HWND；True render API / `MpvRenderContext` 深度整合保留为后续阶段）
- [ ] 创建 Tauri Plugin: `mpv_plugin.rs`（当前 MVP 继续使用直接 Tauri Commands，不引入第三方 libmpv 插件）
- [x] 实现 Tauri Commands: `mpv_load`, `mpv_pause`, `mpv_resume`, `mpv_seek`
- [x] 实现 Tauri Commands: `mpv_get_property`, `mpv_set_property`
- [x] 实现事件转发: `mpv:time-update`, `mpv:duration-change`, `mpv:paused`, `mpv:resumed`
- [x] 配置 Cargo 依赖: 直接使用 `libmpv-sys = "3.1"` 绑定 libmpv C API
- [~] 编写构建脚本: 自动下载对应平台的 libmpv 库，并为 Windows GNU 链接准备 import library

#### Vue 侧播放器 Composable

- [x] 实现 `useMpv()` composable
- [x] 响应式状态: `isPlaying`, `currentTime`, `duration`, `volume`
- [x] 响应式状态: `subtitleTracks`, `audioTracks`, `currentSubtitle`, `currentAudio`（已接入 mpv 轨道状态、内封/外部字幕和当前音轨/字幕选择）
- [x] 方法: `load()`, `togglePause()`, `seek()`, `setVolume()`
- [x] 方法: `setSubtitle()`, `setAudio()`
- [x] 事件监听自动清理 (`onUnmounted`)

#### 基础窗口管理

- [x] 无边框窗口配置 (`decorations: false`)
- [x] 自定义标题栏组件 (`TitleBar.vue`)
- [x] 窗口拖拽区域 (`data-tauri-drag-region`)
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
- [x] libmpv 渲染在窗口内部，沉浸式体验（Windows MVP 已通过 `wid` + `vo=gpu-next` + 透明 Tauri/WebView 叠层完成；Linux/macOS/mobile 后端仍为后续计划且当前显示 unsupported）
- [x] 基础播放控制 (播放/暂停/进度/音量；控制层已改为 Cinema OS/liquid-glass 并支持静止自动隐藏，Windows 宿主已验证叠层控件可见且可点击)

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
- [~] 定义 `DataSource` 接口 (init, test, destroy, list, listLibraries, getHomeSections, getFeaturedItems, getContinueWatching, getRecentlyAdded, search, getDetail, getStreamURL, optional syncPlaybackProgress；Jellyfin/OpenList/CloudDrive2 等实现待补齐)

#### DataSourceManager 实现

- [~] 实现 `DataSourceManager` 类（已支持 Emby 实例化、按配置同步、聚合首页基础区块；跨源搜索/导入导出完善待后续）
- [x] `addSource()` — 创建并初始化数据源
- [x] `removeSource()` — 销毁并移除数据源
- [x] `getAllSources()` / `getSource(id)`
- [x] `getOrderedSources()` — 按绑定配置顺序返回侧栏数据源
- [~] `getAggregatedHome()` — 聚合 Hero / 继续观看 / 最新影片首页数据（已接 Emby，更多源待扩展）
- [ ] `searchAll()` — 跨数据源并发搜索
- [~] `exportAllConfigs()` / `importConfigs()` — 配置导入导出（已能导出已实例化安全配置，完整文件导入导出待后续）
- [x] `createDataSource(type)` — 工厂方法（当前实现 Emby，其他类型保留扩展）

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
- [x] Emby 播放进度同步 — 通过 PlaybackInfo + Sessions/Playing/Progress/Stopped 将 active session、继续观看和播放历史同步回 Emby；本机 SQLite 历史保持 primary，provider sync best-effort

#### JellyfinDataSource 实现

- [ ] 实现 `JellyfinDataSource` (与 Emby API 类似，差异处理)
- [ ] API 路径差异适配 (`/emby/` → `/`)
- [ ] 认证头差异适配

#### 配置持久化

- [~] 使用 Tauri `app_data_dir` 存储配置
- [~] 实现 `config.json` 读写 (datasources, server, ai, ui)
- [~] 配置变更自动保存
- [~] Emby 与 OpenList/Alist 账号、密码与访问令牌持久化到 Tauri app data 下的 SQLite 凭证库，并通过 `credentialRef` 引用；字段使用 provider-specific 结构化 envelope + 本地生成 master key + AES-GCM 加密，master key 仍保存在本机 app data 文件中，暂未接入 OS Keychain/libsecret/DPAPI

#### 设置页面 UI

- [~] `SettingsView.vue` — 设置页面（已提供数据源管理入口、Emby 与 OpenList/Alist 账号密码登录式添加/编辑表单，并提供 OpenList/Alist 登录后的根目录选择）
- [x] 数据源列表管理 (添加/编辑/删除)
- [ ] 数据源排序设置 (决定动态侧栏展示顺序)
- [~] 数据源显示配置 (名称/图标/是否在侧栏显示；当前支持显示名称，图标/侧栏开关待后续)
- [~] 添加数据源表单 (管理入口→可见类型卡片选择→Emby/OpenList/Alist URL/账号/密码登录；OpenList/Alist 登录后可从 `/` 浏览并选择根目录，根目录以 `extra.rootPath` 保存为非敏感配置；账号、密码、token 通过 `credentialRef` 持久化到 Tauri SQLite 凭证库，未写入 localStorage/DataSource 配置)
- [x] 连接测试按钮 (显示成功/失败)
- [~] 数据源状态显示 (在线/离线；当前在测试/浏览错误态中呈现，持久状态徽标待后续)

**产出**:

- [~] 能添加 Emby/Jellyfin 服务器（Emby 已实现；Jellyfin 待后续）
- [x] 能按绑定顺序在动态侧栏展示数据源
- [~] 聚合首页能展示 Emby/Jellyfin 的 Hero 轮播、继续观看、最新影片（Emby 已接入，凭证会话有效时可加载；Jellyfin 待后续）
- [~] 能进入单个数据源媒体库首页并浏览媒体库、搜索影片（Emby 已实现，并改为按媒体库/文件夹/剧集/季/集层级非递归浏览；搜索/首页区块仍可使用递归查询）
- [~] 能直接播放 Emby/Jellyfin 上的视频（Emby 条目可生成 stream URL 并进入现有播放加载流程；Windows 宿主已验证可在透明叠层 + mpv 视频底层窗口中显示，Jellyfin 数据源仍待实现）
- [~] 配置自动持久化（非敏感配置持久化；Emby 与 OpenList/Alist 账号、密码、token 进入 Tauri app data 下 SQLite 凭证库，敏感字段以本地 master key 加密；OS secure storage/Keychain/libsecret/DPAPI 后续接入）

### Sprint 1.3: OpenList/Alist + CloudDrive2 + 本地文件 (Week 7-8)

**目标**: 完整的独立播放器，支持多种数据源

#### OpenList/Alist DataSource 实现

- [~] 实现 OpenList/Alist HTTP API 客户端 (`/api/auth/login`, `/api/fs/list`, `/api/fs/get`；代码已接入 Player，仍待真实 OpenList/Alist 服务 live test)
- [ ] 实现 WebDAV 客户端 (备选方案)
- [~] `list(path)` — 目录浏览（HTTP API 已实现，按 `extra.rootPath` 作为库根目录浏览，待真实服务验证）
- [~] `search(keyword)` — 搜索（优先 `/api/fs/search` 且 parent 指向 `extra.rootPath`，不可用时在选中根目录内有限目录回退搜索；待真实服务验证）
- [~] `getStreamURL(path)` — 构建播放URL (`/d{path}`，支持 `/api/fs/get` 返回的 sign；路径必须位于 `extra.rootPath` 内，待真实服务验证)
- [~] 实现 `AlistDataSource` (OpenList/Alist-compatible, implements DataSource；账号登录-only MVP；支持 `extra.rootPath` 根目录约束)
- [~] 连接测试（设置页添加/编辑时先 `/api/auth/login` 并测试根目录列表；登录后可浏览 `/` 并选择 `extra.rootPath`，待真实服务验证）

#### CloudDrive2DataSource 实现

- [ ] 实现 WebDAV 客户端 (CloudDrive2 暴露 WebDAV)
- [ ] `list(path)` — 目录浏览
- [ ] `getStreamURL(path)` — 构建播放URL
- [ ] 实现 `CloudDrive2DataSource` (implements DataSource)
- [ ] 连接测试

#### LocalFileDataSource 实现

- [ ] 本地文件系统浏览 (Tauri `fs` API)
- [ ] 文件类型过滤 (视频文件)
- [~] 播放页视频区域拖拽播放和右下角悬浮播放按钮打开本地视频文件选择器已实现；当前可进入播放页并交给 libmpv 加载，Windows 内嵌视频渲染已验证，尚不等同于 LocalFileDataSource 浏览能力
- [ ] 文件关联 (双击打开)
- [ ] 实现 `LocalFileDataSource` (implements DataSource)

#### 云盘占位符

- [ ] 115网盘 — UI 中显示"即将推出"标签
- [ ] 123盘 — UI 中显示"即将推出"标签
- [ ] 夸克网盘 — UI 中显示"即将推出"标签
- [ ] 接口定义预留 (`throw new Error('即将推出')`)

#### DataSourceManager 完善

- [ ] 跨数据源搜索结果合并与去重
- [ ] 统一媒体浏览 (合并所有 DataSource 的内容)
- [ ] 云盘/本地文件刮削结果接入聚合首页 Hero / 最新影片
- [ ] 配置导入/导出 (JSON 文件)

#### 播放器增强

- [x] 字幕菜单（已内联在 `PlayerControls.vue`；后续如需复用再拆独立 `SubtitleMenu.vue`）
- [x] 音轨菜单（已内联在 `PlayerControls.vue`；后续如需复用再拆独立 `AudioMenu.vue`）
- [x] 播放队列面板（已内联在播放控制条并支持上一集/下一集；后续如需复用再拆独立 `PlaylistPanel.vue`）
- [x] 播放历史记录（本机 Tauri SQLite 持久化，避免 localStorage 存播放状态）
- [x] 继续观看功能（本机历史 + provider 原生继续观看聚合，Emby 进度同步后首页刷新）
- [x] 右键播放菜单 + 播放详情 stats 浮层（紧凑用户菜单、播放详情浮层、HDR/SDR/杜比视界动态范围展示；诊断入口不暴露给普通用户）

**产出**:

- [~] 能连接 OpenList/Alist 浏览和播放云盘文件（实现已写入 Player，待真实 OpenList/Alist 服务 live test）
- [ ] 能连接 CloudDrive2 浏览和播放
- [~] 能通过文件选择器打开本地视频并进入播放页，播放页视频区域支持拖拽播放；Windows 已验证窗口内视频渲染，本地文件 DataSource 浏览、文件关联与完整本地媒体库能力未完成
- [ ] 115/123/夸克在 UI 中有占位

---

## Phase 2: Server MVP (Week 9-14)

> 后端最小可用版本 — 优先打通刚需存储与播放闭环：115网盘 / OpenList/Alist / CloudDrive2 / 本地文件 + 三层架构 + STRM + 302代理 + 配置同步；PT聚合、追更、AI、插件、多用户权限等功能保留在后续阶段逐步完整实现

### Sprint 2.1: 基础框架 + 三层架构 (Week 9-10)

**目标**: Server 能启动，三层架构 (连接/存储目标/分类规则) 可用，用户管理可用

#### Go 项目初始化

- [ ] `go mod init ohmycine-server`
- [ ] 目录结构创建 (`cmd/`, `internal/`, `pkg/`, `api/`, `configs/`, `docker/`)
- [ ] 配置 `go.mod` 依赖
- [ ] 编写 `cmd/server/main.go` 入口
- [ ] 配置管理 (`internal/config/`) — Viper + YAML
- [ ] 日志系统 (`internal/middleware/logger.go`) — zerolog 结构化日志

#### Web 框架搭建

- [ ] Gin 路由初始化
- [ ] 中间件: CORS, Logger, Recovery
- [ ] JWT 认证中间件 (`internal/middleware/auth.go`)
- [ ] 统一错误响应格式
- [ ] 统一分页格式
- [ ] API 版本路由 (`/api/v1/`)

#### 数据库层

- [ ] GORM 初始化 + SQLite 连接
- [ ] 自动迁移 (`AutoMigrate`)
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
- [ ] 目录模板和命名模板字段
- [ ] 转移策略字段 (move/hardlink/copy/symlink)
- [ ] 排序字段 (匹配优先级)

#### 用户管理

- [ ] `User` 模型 + bcrypt 密码哈希
- [ ] `POST /api/v1/auth/login` — 登录 (返回 JWT)
- [ ] `POST /api/v1/auth/logout` — 登出
- [ ] `GET /api/v1/auth/me` — 当前用户信息
- [ ] `GET /api/v1/users` — 用户列表 (管理员)
- [ ] `POST /api/v1/users` — 添加用户 (管理员)
- [ ] `PUT /api/v1/users/{id}` — 更新用户
- [ ] `DELETE /api/v1/users/{id}` — 删除用户 (管理员)
- [ ] 权限检查中间件 (admin/user)
- [ ] 首次启动自动创建管理员账号

#### 本地运行与部署准备

- [ ] Server 能本地二进制运行 (`go run ./cmd/server` / `go build`)
- [ ] 基础配置文件可用 (`configs/config.example.yaml`)
- [ ] 健康检查端点 (`GET /api/v1/health`)
- [ ] 编写 `Dockerfile` (后续部署准备，可不作为本地开发前置)
- [ ] 编写 `docker-compose.yaml` (后续 NAS/服务器部署准备，可选)

**产出**:

- [ ] Server 能本地运行并通过健康检查
- [ ] 三层架构 CRUD API 可用
- [ ] 用户登录/权限控制可用
- [ ] 连接信息加密存储

### Sprint 2.2: 网盘驱动 + 下载器 + 302代理 (Week 11-12)

**目标**: 网盘驱动可用，下载器能连接，302代理能播放，媒体服务器能通知刷新

#### 网盘驱动抽象层

- [ ] 定义 `Driver` 接口 (`pkg/cloud/driver.go`)
- [ ] 定义 `File`, `DownloadURL`, `Quota` 结构体
- [ ] 实现驱动注册机制 (`pkg/cloud/registry.go`)
- [ ] 实现 `AlistDriver` (`pkg/cloud/alist/`，兼容 OpenList/Alist API)
  - [ ] HTTP API 客户端 (`/api/fs/list`, `/api/fs/get`, `/api/fs/search`)
  - [ ] `List()`, `Get()`, `Upload()`, `GetDownloadURL()`, `Search()`
  - [ ] 连接测试 (`IsAlive`)
- [ ] 实现 `115Driver` (`pkg/cloud/pan115/`)
  - [ ] Cookie 认证
  - [ ] 文件列表/搜索/下载链接
- [ ] 实现 `AliyunDriver` (`pkg/cloud/aliyun/`)
  - [ ] Token 认证
  - [ ] 文件列表/搜索/下载链接

#### 下载器管理

- [ ] 定义 `DownloadClient` 接口 (`pkg/downloader/client.go`)
- [ ] 定义 `Task`, `AddRequest` 结构体
- [ ] 实现 `QBittorrentClient` (`pkg/downloader/qbittorrent/`)
  - [ ] 认证 (cookie-based)
  - [ ] `AddTorrent()` — 添加种子
  - [ ] `ListTasks()` / `GetTask()` — 查询任务
  - [ ] `PauseTask()` / `ResumeTask()` / `DeleteTask()` — 控制任务
  - [ ] 任务状态同步
- [ ] 实现 `TransmissionClient` (`pkg/downloader/transmission/`)
  - [ ] RPC 认证
  - [ ] 同上接口实现
- [ ] 下载器 CRUD API (`internal/handlers/download.go`)
  - [ ] `POST /api/v1/downloaders` — 添加下载器
  - [ ] `GET /api/v1/downloaders` — 下载器列表
  - [ ] `POST /api/v1/downloaders/{id}/test` — 测试连接

#### 302代理引擎

- [ ] 实现 `Engine` (`pkg/proxy/engine.go`)
- [ ] 路由: `GET /proxy/{driver}/{path...}`
- [ ] URL 缓存 (TTL 机制)
- [ ] 302 重定向逻辑
- [ ] CORS 支持
- [ ] 错误处理 (驱动不存在/文件不存在)

#### 媒体服务器客户端

- [ ] 定义 `MediaServerClient` 接口 (`pkg/mediaserver/client.go`)
- [ ] 实现 `EmbyClient` (`pkg/mediaserver/emby.go`)
  - [ ] `TestConnection()` — 测试连接
  - [ ] `RefreshLibrary(libraryID)` — 刷新媒体库
  - [ ] `GetLibraries()` — 获取媒体库列表
  - [ ] `Search(keyword)` — 搜索
- [ ] 实现 `JellyfinClient` (`pkg/mediaserver/jellyfin.go`)

#### 配置同步 API

- [ ] `POST /api/v1/sync/push` — Player 推送数据源配置
- [ ] `GET /api/v1/sync/pull` — Player 拉取 Server 配置
- [ ] `GET /api/v1/sync/status` — 同步状态
- [ ] 自动导入 Player 的数据源配置到连接管理

**产出**:

- [ ] 网盘文件浏览/上传可用
- [ ] qBit/Transmission 能连接和控制
- [ ] 302 代理能重定向到云盘 CDN
- [ ] Emby/Jellyfin 能通过 API 刷新媒体库

### Sprint 2.3: 媒体流水线 + STRM + 元数据 (Week 13-14)

**目标**: 完整的 下载→转移→入库 流水线跑通

#### 元数据刮削

- [ ] TMDB API 客户端 (`pkg/metadata/tmdb.go`)
  - [ ] `Search(title, year)` — 搜索电影/剧集
  - [ ] `GetDetail(tmdbId)` — 获取详情 (含 credits/images)
  - [ ] `GetByIMDBID(imdbId)` — 通过 IMDB ID 查询
  - [ ] 图片 URL 构建 (poster, backdrop)
- [ ] 文件名解析器 (`pkg/metadata/parser.go`)
  - [ ] 标题提取
  - [ ] 年份提取
  - [ ] 分辨率/编码/来源提取
  - [ ] 季/集号提取 (剧集)
  - [ ] 制作组提取
- [ ] NFO 生成器 (XML 格式)
- [ ] 海报/背景图下载

#### 文件转移引擎

- [ ] `TransferService` (`internal/services/transfer.go`)
- [ ] 下载完成回调监听
- [ ] 自动分类匹配逻辑
  - [ ] 优先: 站点分类
  - [ ] 次选: 文件名解析 (有 season → tv)
  - [ ] 兜底: TMDB 查询确认
- [ ] 目标路径构建 (根据分类规则模板)
  - [ ] 变量替换: `{title}`, `{year}`, `{season:02d}`, `{episode:02d}`, `{resolution}`
  - [ ] 扩展名保留
- [ ] 转移策略执行
  - [ ] `move` — 移动文件 (默认)
  - [ ] `hardlink` — 硬链接 (保种)
  - [ ] `copy` — 复制
  - [ ] `symlink` — 软链接
- [ ] 转移任务记录 (transfer_tasks 表)
- [ ] 转移失败重试机制

#### 通知服务

- [ ] `NotifyService` (`internal/services/notify.go`)
- [ ] Emby/Jellyfin 刷新通知 (REST API 调用)
- [ ] Player 客户端通知 (WebSocket 推送)
- [ ] 通知事件类型: `media.added`, `transfer.completed`

#### STRM 管理器

- [ ] `STRMGenerator` (`pkg/strm/generator.go`)
- [ ] `GenerateOne()` — 生成单个 STRM 文件
  - [ ] 内容: 302 代理 URL (`http://server:3000/proxy/{driver}/{path}`)
  - [ ] 目录结构: `{dest}/{title} ({year})/{filename}.strm`
- [ ] `IncrementalSync()` — 增量同步 (只处理新增/修改)
- [ ] `FullSync()` — 全量扫描
- [ ] `CleanInvalid()` — 清理无效 STRM (指向不存在的文件)
- [ ] STRM 定时任务配置 (`strm_schedules` 表)
  - [ ] 增量同步 cron
  - [ ] 全量扫描 cron
  - [ ] 无效清理 cron
- [ ] STRM 管理 API
  - [ ] `GET /api/v1/strm/status` — 同步状态
  - [ ] `POST /api/v1/strm/sync/incremental` — 立即增量
  - [ ] `POST /api/v1/strm/sync/full` — 立即全量
  - [ ] `POST /api/v1/strm/clean` — 清理无效

#### 部署配置（后置，不阻塞本地开发）

- [ ] `docker-compose.yaml` 编写 (用于 NAS/服务器部署与后续集成测试)
  - [ ] ohmycine-server 服务
  - [ ] emby 服务 (可选)
  - [ ] qbittorrent 服务 (可选)
  - [ ] 共享卷配置 (STRM 库目录)

#### Player 端 Server 连接 UI

- [ ] 添加 Server 连接表单 (URL + API Key)
- [ ] 连接状态显示
- [ ] 同步状态显示
- [ ] Server 侧功能展示入口

**产出**:

- [ ] 下载完成 → 自动转移 → 自动生成 STRM → 通知 Emby 刷新 → Emby 能播放
- [ ] STRM 定时增量/全量/清理可用
- [ ] Player 连接 Server 后配置自动同步

---

## Phase 3: 核心功能增强 (Week 15-22)

> 补全核心功能 — 在 Server 刚需闭环稳定后，继续实现发现页、PT聚合搜索、追更、AI助手、网盘增强、Cinema OS UI 等完整产品能力

### Sprint 3.1: 发现页 + PT站点实现 (Week 15-17)

**目标**: 发现页可用，主流 PT 站点接入，聚合搜索 + 一键下载

#### PT 站点框架

- [ ] 定义 `Site` 接口 (`pkg/scraper/site.go`)
- [ ] 定义 `SiteConfig`, `SearchRequest`, `Torrent` 结构体
- [ ] 站点注册机制
- [ ] 站点管理 API
  - [ ] `POST /api/v1/sites` — 添加站点
  - [ ] `GET /api/v1/sites` — 站点列表
  - [ ] `PUT /api/v1/sites/{id}` — 更新
  - [ ] `DELETE /api/v1/sites/{id}` — 删除
  - [ ] `POST /api/v1/sites/{id}/test` — 测试连接

#### PT 站点实现

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

#### 发现页聚合搜索

- [ ] `DiscoveryService` (`internal/services/discovery.go`)
- [ ] 并发搜索所有已配置站点 (`goroutine` + `channel`)
- [ ] 结果聚合 + 去重
- [ ] TMDB 自动匹配 (IMDB ID 优先, 标题+年份兜底)
- [ ] 结果排序 (相关度/做种数/大小)
- [ ] 筛选/过滤 (分类/分辨率/大小范围/做种数)
- [ ] 搜索 API
  - [ ] `POST /api/v1/discovery/search` — 聚合搜索
  - [ ] `GET /api/v1/discovery/trending` — 热门资源
  - [ ] `GET /api/v1/discovery/latest` — 最新资源

#### 一键下载

- [ ] 自动分类匹配 (站点分类 + 文件名解析 + TMDB)
- [ ] 确定下载目录 (根据分类规则 → 存储目标)
- [ ] 提交到下载器
- [ ] 记录下载任务 (关联用户 ID)
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

- [ ] 能跨站点聚合搜索
- [ ] 搜索结果自动匹配 TMDB 元数据
- [ ] 一键下载 → 自动分类 → 自动转移 → 自动入库

### Sprint 3.2: 追更 + 网盘增强 (Week 18-19)

**目标**: 追更可用，更多网盘支持，302代理增强

#### 追更引擎

- [ ] `FollowService` (`internal/services/follow.go`)
- [ ] 追更任务模型 (`follow_tasks` 表)
- [ ] 创建追更任务
  - [ ] TMDB ID + 剧名 + 季号
  - [ ] 站点过滤 (只在指定站点搜索)
  - [ ] 质量偏好 (分辨率/编码/制作组)
  - [ ] Cron 表达式 (默认每天 3:00)
- [ ] 定时执行逻辑
  - [ ] 在指定站点搜索剧名/IMDB ID
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
  - [ ] 数据源级 Hero Carousel (当前来源元数据)
  - [~] 媒体库分组 (电影/剧集/文件夹)
  - [ ] 库内海报墙与详情浏览
- [~] 媒体展示组件
  - [ ] `MediaCard.vue` — 媒体卡片 (海报+信息)
  - [ ] `MediaGrid.vue` — 网格布局
  - [ ] `MediaRow.vue` — 横向滚动行
  - [ ] `MediaDetail.vue` — 媒体详情面板
  - [ ] `PosterWall.vue` — 海报墙
  - [~] `HeroCarousel.vue` — 首页/数据源页大图轮播
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
- [ ] 播放控制快捷键 (Space, ←, →, ↑, ↓, M)
- [ ] 字幕/音轨快捷键 (S, A)
- [ ] 弹幕快捷键 (D, Shift+D, Shift+↑, Shift+↓)
- [ ] 窗口快捷键 (F, Escape, P)
- [ ] 导航快捷键 (Ctrl+F, Ctrl+,)
- [ ] 快捷键冲突检测

#### 弹幕系统

- [ ] 弹幕数据格式定义 (`DanmakuItem` 接口)
- [ ] B 站 XML 弹幕解析器
- [ ] JSON 弹幕解析器
- [ ] 弹弹Play API 弹幕源
- [ ] 本地弹幕文件自动匹配 (同目录同名 .xml/.json)
- [ ] Canvas 弹幕渲染引擎
  - [ ] 轨道分配管理器
  - [ ] 滚动弹幕动画
  - [ ] 顶部/底部固定弹幕
  - [ ] 弹幕碰撞检测
- [ ] 弹幕设置面板 (透明度/字号/速度/屏蔽)
- [ ] Tauri Commands (danmaku_load_xml, danmaku_load_json, danmaku_fetch)

#### 整体优化

- [ ] 性能优化 (虚拟滚动/懒加载/缓存)
- [ ] 错误处理完善 (统一错误边界)
- [ ] 日志系统增强 (结构化日志/日志轮转)
- [ ] 国际化完善 (中英文完整翻译)

**产出**:

- [ ] AI 能基于本地库推荐电影
- [ ] 液态玻璃 UI 流畅
- [ ] 文件管理跨数据源可用
- [ ] 快捷键系统完整

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

- [ ] 插件引擎 (Go plugin / WASM)
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

- [ ] Tauri Android 构建配置
- [ ] libmpv Android 集成 (交叉编译 .so)
- [ ] 移动端 UI 适配
  - [ ] 触摸手势 (滑动/捏合/双击)
  - [ ] 底部导航栏
  - [ ] 横屏播放
  - [ ] 安全区域适配
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
