# OhMyCine Player — 播放器设计文档

## 1. 概述

OhMyCine Player 是一款**独立可用**的跨平台沉浸式家庭影院播放器，核心特点：
- **独立运行** — 无需 Server，原生连接 Emby/Jellyfin/OpenList/Alist/CloudDrive2/WebDAV/本地文件夹
- **Cinema OS 风格 UI** — 液态玻璃设计语言，深浅色主题，电影感排版
- **libmpv 引擎** — 全格式支持，硬件解码，HDR/Dolby Vision，沉浸式嵌入渲染
- **全平台目标** — 当前 Player MVP 先完成 Windows；macOS、Linux (桌面) 和 Android 渲染/打包链路作为后续平台目标保留
- **Server 增强** — 可选连接 OhMyCine Server 获取 PT站点管理、自动下载、STRM生成等高级功能

## 2. 技术栈

| 组件 | 技术 | 说明 |
|------|------|------|
| 应用框架 | Tauri v2 | 跨平台壳，Rust后端 + WebView前端 |
| UI框架 | Vue 3.4+ | Composition API + `<script setup>` |
| 类型系统 | TypeScript 5.x | 全面类型安全 |
| 状态管理 | Pinia | 轻量、TypeScript友好 |
| 样式方案 | UnoCSS + CSS Variables | 原子化CSS + 设计系统Token |
| 组件库 | 自研 (ohmycine-ui) | Cinema OS风格基础组件 |
| 图标 | Iconify + 自研SVG | 统一图标系统 |
| 动画 | Motion Vue + GSAP | 流畅的页面/组件动画 |
| 路由 | Vue Router 4 | SPA路由 |
| HTTP客户端 | ofetch | Tauri IPC + HTTP请求 |
| 播放器引擎 | libmpv (嵌入式) | Rust FFI 绑定，直接嵌入窗口渲染 |
| 国际化 | Vue I18n | 中文/英文 |
| 构建工具 | Vite 5 | 开发体验 + 构建性能 |

## 3. 项目结构

```
ohmycine-
├── src-tauri/                    # Rust/Tauri 后端
│   ├── src/
│   │   ├── main.rs               # 入口
│   │   ├── commands/             # Tauri Commands (暴露给前端的API)
│   │   │   ├── mod.rs
│   │   │   ├── player.rs         # 播放器控制命令
│   │   │   ├── file.rs           # 文件操作命令
│   │   │   ├── system.rs         # 系统信息命令
│   │   │   └── window.rs         # 窗口控制命令
│   │   ├── mpv/                  # MPV IPC 集成
│   │   │   ├── mod.rs
│   │   │   ├── player.rs         # MPV进程管理
│   │   │   └── ipc.rs            # JSON IPC通信
│   │   └── render/              # 渲染上下文管理
│   ├── Cargo.toml
│   └── tauri.conf.json
│
├── src/                          # Vue 前端
│   ├── main.ts
│   ├── App.vue
│   │
│   ├── assets/                   # 静态资源
│   │   ├── fonts/                # 电影感字体
│   │   ├── images/               # 默认海报/背景
│   │   └── icons/                # 自研图标
│   │
│   ├── components/               # 通用组件
│   │   ├── ui/                   # 基础UI组件库
│   │   │   ├── OButton.vue       # 液态玻璃按钮
│   │   │   ├── OCard.vue         # 毛玻璃卡片
│   │   │   ├── ODialog.vue       # 弹窗
│   │   │   ├── OInput.vue        # 输入框
│   │   │   ├── OSlider.vue       # 滑块
│   │   │   ├── OToggle.vue       # 开关
│   │   │   ├── OSelect.vue       # 下拉选择
│   │   │   ├── OToast.vue        # 提示
│   │   │   ├── OProgress.vue     # 进度条
│   │   │   └── index.ts          # 导出
│   │   │
│   │   ├── layout/               # 布局组件
│   │   │   ├── AppLayout.vue     # 主布局（动态数据源侧栏+内容区+窗口控制）
│   │   │   ├── DataSourceSidebar.vue # 动态数据源侧栏
│   │   │   ├── WindowChrome.vue  # 无边框窗口拖拽与控制按钮
│   │   │   └── StatusBar.vue     # 状态栏
│   │   │
│   │   ├──                # 播放器相关组件
│   │   │   ├── VideoPlayer.vue   # 视频播放器（MPV嵌入）
│   │   │   ├── PlayerControls.vue # 播放控制条
│   │   │   ├── ProgressBar.vue   # 进度条
│   │   │   ├── VolumeControl.vue # 音量控制
│   │   │   ├── SubtitleMenu.vue  # 字幕菜单
│   │   │   ├── AudioMenu.vue     # 音轨菜单
│   │   │   └── PlaylistPanel.vue # 播放列表
│   │   │
│   │   ├── media/                # 媒体展示组件
│   │   │   ├── MediaCard.vue     # 媒体卡片（海报+信息）
│   │   │   ├── MediaGrid.vue     # 网格布局
│   │   │   ├── MediaRow.vue      # 横向滚动行
│   │   │   ├── MediaDetail.vue   # 媒体详情面板
│   │   │   ├── PosterWall.vue    # 海报墙
│   │   │   ├── HeroCarousel.vue  # 首页/数据源页大图轮播
│   │   │   └── ContinueWatchingPanel.vue # 继续观看面板
│   │   │
│   │   └── common/               # 其他通用组件
│   │       ├── SearchBar.vue     # 搜索栏
│   │       ├── SettingsPanel.vue # 设置面板
│   │       └── ServerStatus.vue  # 服务器连接状态
│   │
│   ├── views/                    # 页面
│   │   ├── HomeView.vue          # 聚合首页（全部数据源推荐/最新/继续观看）
│   │   ├── SourceLibraryView.vue # 单数据源媒体库首页（Emby风格库浏览）
│   │   ├── MoviesView.vue        # 电影库
│   │   ├── SeriesView.vue        # 剧集库
│   │   ├── PlayerView.vue        # 播放器页面
│   │   ├── SearchView.vue        # 搜索结果
│   │   ├── SettingsView.vue      # 设置
│   │   ├── CloudView.vue         # 网盘管理（Server联动）
│   │   ├── DownloadsView.vue     # 下载管理（Server联动）
│   │   └── AISearchView.vue      # AI推荐
│   │
│   ├── stores/                   # Pinia状态管理
│   │   ├── player.ts             # 播放器状态
│   │   ├── media.ts              # 媒体库状态
│   │   ├── server.ts             # Server连接状态
│   │   ├── settings.ts           # 设置状态
│   │   └── ui.ts                 # UI状态（主题/布局等）
│   │
│   ├── composables/              # 组合式函数
│   │   ├── useMpv.ts             # MPV播放器控制
│   │   ├── useServer.ts          # Server API调用
│   │   ├── useMedia.ts           # 媒体操作
│   │   ├── useTheme.ts           # 主题管理
│   │   └── useKeyboard.ts        # 快捷键
│   │
│   ├── styles/                   # 全局样式
│   │   ├── variables.css         # CSS变量（设计Token）
│   │   ├── glass.css             # 液态玻璃效果
│   │   ├── animations.css        # 动画定义
│   │   └── global.css            # 全局基础样式
│   │
│   ├── router/                   # 路由
│   │   └── index.ts
│   │
│   ├── i18n/                     # 国际化
│   │   ├── index.ts
│   │   ├── zh-CN.json
│   │   └── en-US.json
│   │
│   └── utils/                    # 工具函数
│       ├── format.ts             # 格式化（时长/大小等）
│       ├── image.ts              # 图片处理
│       └── keyboard.ts           # 快捷键映射
│
├── public/                       # 公共静态资源
├── scripts/                      # 构建脚本
│   ├── download-libmpv.ts         # 下载libmpv库文件
│   └── build-icons.ts            # 图标构建
├── package.json
├── tsconfig.json
├── vite.config.ts
├── unocss.config.ts
└── index.html
```

## 4. DataSource 数据源抽象层

### 4.1 架构设计

Player 的核心设计是 **DataSource 抽象层** — 每种媒体源（Emby、Jellyfin、OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV、本地文件夹等）都是一个 DataSource 实现，通过统一接口访问。Server 也只是其中一个可选的 DataSource。

```
┌─────────────────────────────────────────────────────────────┐
│  OhMyCine Player                                            │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  DataSourceManager (数据源管理器)                     │   │
│  │  统一接口: list / search / getDetail / getStreamURL  │   │
│  └───────────┬──────────┬──────────┬──────────┬────────┘   │
│              │          │          │          │             │
│  ┌───────────▼──┐ ┌─────▼────┐ ┌───▼───┐ ┌───▼────────┐  │
│  │ EmbyDataSource│ │JellyfinDS│ │AlistDS│ │CloudDrive2DS│  │
│  │ (原生API)    │ │(原生API) │ │(HTTP) │ │ (gRPC API) │  │
│  └──────────────┘ └──────────┘ └───────┘ └─────────────┘  │
│                                                             │
│  ┌─────────────────────┐       ┌─────────────────────┐     │
│  │ WebDavDataSource    │       │ LocalFileDataSource │     │
│  │ (PROPFIND + Basic)  │       │ (Tauri 只读文件命令) │     │
│  └─────────────────────┘       └─────────────────────┘     │
│              │          │          │          │             │
│  ┌───────────▼──────────▼──────────▼──────────▼────────┐  │
│  │  CloudDriveDataSource (占位)                          │  │
│  │  ├─ 115网盘  (待实现)                                │  │
│  │  ├─ 123 云盘  (账号 / Access Token)                   │  │
│  │  └─ 夸克网盘 (Cookie / 扫码 / 官方账号登录)          │  │
│  └─────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  ServerDataSource (可选 - 连接 OhMyCine Server)       │   │
│  │  当前: 状态/媒体库/搜索/详情/115 STRM 直连            │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

`ServerDataSource` 已使用现有 DataSource 生命周期接入设置页和 `DataSourceManager`。首次连接提交 Server 地址、用户名、密码和 Player 本机随机设备 ID，成功后只保存 `credentialRef`、设备 ID、Server URL 与安全媒体库摘要；密码不保存，`omc_player_` device token 进入 provider-specific AES-GCM 凭据 envelope。后续 bootstrap、目录、搜索和详情请求统一通过受限 Tauri 原生 JSON 命令访问 `/api/v1/*`，禁止自动跟随重定向并限制方法、路径、请求体和响应体。

Server 媒体与 Player 直连 Emby 的聚合使用显式身份，而不是标题猜测：TMDB ID 合并作品卡片，OhMyCine artifact identity 合并精确版本，Emby `SystemId` 指纹区分实例并与 Library/Item/MediaSource ID 组合。Server 项目作为聚合默认卡片，匹配的 Emby 用户线路仍保留为可选版本；显式进入 Emby 来源页时不执行跨源去重。身份不足时宁可显示两项。ServerDataSource 运行时校验 Server 返回的可选完整详情字段，映射原始标题、评分、时长、类型、演职人员、外部 ID 和多张剧照；旧 Server 缺少这些字段时继续显示基础详情。Server 本地媒体与 115 STRM 都通过 `DataSource.getStreamRequest` 请求同一个 entry stream：本地条目由 Server 提供 Bearer + Range 文件流，115 条目由 Server 返回安全 302；Player 不读取 `.strm` 文件、不接触 Server 绝对路径、不使用 Server 保存的 Emby 管理 API Key，也不经过 Emby，跨 origin 302 前仍移除私有 Header。Emby 详情请求独立允许有界多张 Backdrop，People 类型按大小写不敏感清洗去重。

每个启用且凭据有效的 `ServerDataSource` 还维护一个 device Bearer 长轮询：`GET /api/v1/media-changes?cursor=...&wait_seconds=12`。cursor 以 `ohmycine:server-media-change-cursor:<sourceId>:<encodedServerOrigin>` 保存到 app settings，避免同一来源配置切换 Server 后复用旧游标；断线按 source 独立指数退避，最大 15 秒，`resync_required` 只全量失效该 Server source。收到 ready change 后立即清除对应 ServerDataSource cache/来源根快照、按媒体库 revision 合并失效聚合首页并在后台强制刷新；直连 Emby/Jellyfin、本地、OpenList/Alist、CloudDrive2、WebDAV 与正在播放的流不受影响。若用户正在浏览受影响的 Server 列表，仅在当前逻辑媒体库匹配时保留现有内容并合并显示“媒体库已更新”；点击后原位刷新，恢复 `main.cinema-scrollbar.scrollTop` 和当前焦点卡片，离开再进入则直接读取最新数据。旧 Server 不支持端点时继续使用现有 TTL/手工刷新，不影响浏览。

### 4.2 DataSource 接口定义

```typescript
// src/services/datasource/types.ts

export interface MediaItem {
  id: string
  sourceId: string
  libraryId?: string
  name: string
  titleLogoUrl?: string
  type: 'movie' | 'series' | 'season' | 'episode' | 'folder' | 'file'
  posterUrl?: string
  backdropUrl?: string
  year?: number
  rating?: number
  overview?: string
  tagline?: string
  duration?: number        // 秒
  size?: number            // 字节
  modified?: string        // 最后修改时间 (ISO 8601)
  path: string
  children?: MediaItem[]   // 子项（剧集/文件夹）
}

export interface FileEntry {
  name: string
  path: string
  modified: string
}

export interface MediaLibrary {
  id: string
  sourceId: string
  name: string
  type: 'movies' | 'series' | 'anime' | 'music' | 'mixed' | 'folders'
  posterUrl?: string
  backdropUrl?: string
  itemCount?: number
}

export interface HomeSection {
  id: string
  sourceId?: string
  title: string
  type: 'hero' | 'continueWatching' | 'recentlyAdded' | 'recommended' | 'libraryRow'
  items: MediaItem[]
}

export interface MediaDetail extends MediaItem {
  genres?: string[]
  directors?: string[]
  cast?: string[]
  imdbId?: string
  tmdbId?: number
  resolution?: string      // 4K / 1080p / 720p
  codec?: string           // H265 / AV1
  audioCodec?: string      // DTS-HD / Atmos
  subtitles?: SubtitleTrack[]
  audioTracks?: AudioTrack[]
}

export interface SubtitleTrack {
  index: number
  language: string
  title?: string
  isDefault: boolean
}

export interface SubtitleSearchResult {
  id: string
  origin: 'emby' | 'local'
  providerName: string
  language: string
  title: string
  format?: string
  downloadRef?: string
}

export interface AudioTrack {
  index: number
  language: string
  codec: string
  channels: number
  isDefault: boolean
}

export type DataSourceType = 'emby' | 'jellyfin' | 'alist' | 'clouddrive2' | 'webdav' | 'server' | '115' | '123' | 'quark' | 'local'

export interface DataSourceConfig {
  id: string
  type: DataSourceType
  name: string
  displayName?: string
  iconUrl?: string
  order: number
  url: string
  enabled?: boolean
  // 非敏感扩展配置，例如 credentialRef、rootPath、library ids。
  // API key、账号、密码、token 不进入普通 DataSourceConfig。
  extra?: Record<string, unknown>
}

export interface DataSource {
  readonly id: string
  readonly name: string
  readonly type: DataSourceType
  readonly isConnected: boolean

  // 生命周期
  init(config: DataSourceConfig): Promise<void>
  test(): Promise<boolean>
  destroy(): void

  // 媒体浏览
  list(path?: string): Promise<MediaItem[]>
  listLibraries?(): Promise<MediaLibrary[]>
  getHomeSections?(): Promise<HomeSection[]>
  getFeaturedItems?(): Promise<MediaItem[]>
  getContinueWatching?(): Promise<MediaItem[]>
  getRecentlyAdded?(): Promise<MediaItem[]>
  search(keyword: string): Promise<MediaItem[]>
  getDetail(id: string): Promise<MediaDetail>

  // 播放
  getStreamURL(id: string): Promise<string>
  getStreamRequest?(request: PlaybackRequest): Promise<MediaStreamRequest>
  searchSubtitles?(input: SubtitleSearchInput): Promise<SubtitleSearchResult[]>
  downloadSubtitle?(input: SubtitleDownloadInput): Promise<SubtitleTrack>

  // 配置导出（用于同步给Server）
  exportConfig(): DataSourceConfig
}
```

播放导航只携带 `sourceId`、`itemId`、可选 `mediaSourceId` 和短生命周期 `contextId`。Home、详情页、数据源媒体库和播放队列不得在 Vue Router query/history 中保存远程直链、签名 URL、认证 header 或本地绝对路径；`PlayerView` 在即将调用 mpv 时通过 `getStreamRequest({ itemId, mediaSourceId })` 即时解析播放请求。文件拖放和本机继续观看所需的绝对路径只进入当前进程内存中的 `PlaybackMediaContext.locator`，不持久化。Emby 多版本选择必须把选中的 `mediaSourceId` 传到流解析和后续进度同步会话，版本已失效时明确要求用户重新选择。

删除媒体源时，配置删除是主操作，同时按 `sourceId` 清理本机 SQLite 播放历史，并按 source/root 清理原始文件扫描缓存；清理范围不得影响其他媒体源。凭据、历史或缓存清理失败不应把已经删除的数据源重新加入 UI。

播放中字幕搜索分为两条明确路径，不把多个来源静默混合成一次请求：

- Emby 媒体点击“搜索字幕”后先选择 `Emby 搜索` 或 `本地搜索`。Emby 搜索调用服务端远程字幕 API，下载由 Emby 保存；Player 刷新媒体轨道并把新增外部字幕立即加载到 mpv。
- OpenList/Alist、CloudDrive2、WebDAV、本地文件等其他媒体源直接进入 Player 本地搜索，不显示 Emby 来源选择。
- Player 本地搜索通过独立 `SubtitleProvider` 抽象扩展，当前支持 OpenSubtitles、射手网和迅雷字幕，不在 Vue 组件中抓取网站页面。
- “本地搜索”表示搜索逻辑运行在 Player 本机、下载字幕写入 Player cache，不表示媒体文件必须位于本地硬盘。Emby、OpenList/Alist、CloudDrive2、WebDAV 和后续远程 DataSource 均可使用本地搜索。
- 各字幕提供器独立运行和容错。OpenSubtitles 未配置时只跳过自身，不得阻断已启用的射手网或迅雷字幕；哈希来源根据当前播放目标选择真实本机绝对路径或当前 HTTP(S) 播放请求，不额外依赖可能尚未同步的数据源类型。单个提供器失败也不得丢弃其他提供器已经返回的结果。
- 提供器能力必须明确区分：OpenSubtitles 和迅雷使用媒体名称、文件名或自定义关键词查询；射手网只按当前视频内容哈希精确匹配。保存新的 OpenSubtitles 凭据时自动启用该提供器；若 OpenSubtitles 与迅雷均未启用且射手网未命中，搜索界面必须明确提示“输入的关键词没有被查询”，不得显示成所有提供器都完成关键词搜索后的普通零结果。
- OpenSubtitles 提供互斥的二选一模式：API Key 模式使用 OpenSubtitles.com REST API；账号密码模式使用固定 HTTPS OpenSubtitles.org 旧 XML-RPC 接口，不要求用户再填写 API Key。现代 OpenSubtitles.com 邮箱账号可能不属于旧账号体系并返回 401；此时 Player 自动使用官方匿名 XML-RPC 会话继续免 Key 搜索，并在设置反馈中明确显示兼容状态，不得伪称账号已认证。旧版 `API Key + 账号` 凭据迁移时，有完整账号密码则转为账号模式，否则转为 API Key 模式。
- 两种 OpenSubtitles 凭据都保存到 Player 凭据边界。REST 下载只接受受信任 HTTPS OpenSubtitles 域名；XML-RPC 固定请求官方 HTTPS 端点，限制响应大小，认证或匿名兼容会话只保存在 Rust 进程内，下载内容经受限 Base64/gzip 解码后写入 Tauri 字幕缓存。
- 字幕搜索关键词提供三种明确来源：默认使用刮削/展示媒体名称；可切换为不含目录的原始文件名；也可手动输入自定义关键词。自定义模式只发送关键词和语言，不附带当前媒体的 IMDb/TMDB、年份、类型或季集条件。不得把目录、签名 URL、查询参数、本地绝对路径或凭据作为标题查询发送给字幕服务。
- 射手网使用四段 MD5 内容哈希，通过固定 HTTPS API 精确匹配。迅雷固定请求 `https://api-shoulei-ssl.xunlei.com/oracle/subtitle?name=...` 进行名称搜索；同时在 Rust 内限时尝试三段 SHA-1 CID，用于标记并优先展示精确匹配，CID 失败不得阻断名称结果。
- 迅雷名称结果在 Player 本地进行媒体身份精筛：CID 完全一致最高；电影按类型、年份、原始标题、文件名 token 与时长排序，明确排除 `S01E01`、`1x01`、`Season/Episode`、`第01集` 等剧集结果和冲突年份；剧集按剧名、季集编号与时长筛选并排除明确错集。Emby 的 `OriginalTitle`、`ProductionYear`、`RunTimeTicks`、`SeriesName` 与季集号，以及本地刮削的 TMDB 原始标题均可参与本地排序，但发给迅雷的仍只有本次选定的搜索词。
- 本地文件由 Rust 直接读取哈希片段；远程媒体由 Rust 使用当前播放 URL 与必要 Header 做受限 Range 读取：先请求 `bytes=0-0` 验证 Range 和总大小，再只读取算法要求的片段。跨源重定向清除数据源 Header，拒绝 HTTPS 降级、非 HTTP(S) 地址、非 Range 响应和超限 Header。迅雷名称请求只发送用户选定的媒体名、文件名或自定义关键词，不发送播放 URL、目录、Header 或凭据。
- 本地绝对路径只通过 IPC 进入 Rust 读取哈希片段。外部字幕服务只接收内容哈希、文件名和语言，不接收绝对路径、远程播放 URL、数据源账号或 Token。
- 射手网和迅雷下载 URL 保存在 Rust 短期内存表中，Vue 仅持有不透明引用。所有提供器下载均限制域名、重定向、响应大小和扩展名，并使用受控哈希文件名写入当前存储模式的 `cache/subtitles`，不写入媒体目录。
- 播放器字幕菜单内提供轻量字幕偏移控制，直接设置 mpv `sub-delay`，范围为提前 30 秒到延后 30 秒并支持 0.1 秒滑动、0.5 秒步进和一键重置。调节过程不打开遮挡视频的全屏弹窗，新媒体加载时重置为同步状态。

### 4.3 Emby/Jellyfin DataSource 实现

```typescript
// src/services/datasource/emby.ts

export class EmbyDataSource implements DataSource {
  readonly id: string = ''
  readonly type: DataSourceType = 'emby'
  private config!: DataSourceConfig
  private client!: EmbyClient

  async init(config: DataSourceConfig): Promise<void> {
    this.config = config
    ;(this as { id: string }).id = config.name
    this.client = new EmbyClient(config.url, config.apiKey!)
  }

  async test(): Promise<boolean> {
    try {
      await this.client.getSystemInfo()
      return true
    } catch {
      return false
    }
  }

  async list(path?: string): Promise<MediaItem[]> {
    if (!path) {
      // 获取根库（电影库、剧集库）
      const libs = await this.client.getMediaFolders()
      return libs.map(lib => ({
        id: lib.Id,
        name: lib.Name,
        type: 'folder' as const,
        path: lib.Id,
        posterUrl: this.client.getImageUrl(lib.Id, 'Primary'),
      }))
    }
    // 获取库内项目
    const items = await this.client.getItems(path)
    return items.map(item => this.mapEmbyItem(item))
  }

  async search(keyword: string): Promise<MediaItem[]> {
    const results = await this.client.search(keyword)
    return results.map(item => this.mapEmbyItem(item))
  }

  async getDetail(id: string): Promise<MediaDetail> {
    const item = await this.client.getItem(id)
    return {
      ...this.mapEmbyItem(item),
      genres: item.Genres,
      directors: item.People?.filter(p => p.Type === 'Director').map(p => p.Name),
      cast: item.People?.filter(p => p.Type === 'Actor').map(p => p.Name),
      imdbId: item.ProviderIds?.Imdb,
      tmdbId: item.ProviderIds?.Tmdb ? Number(item.ProviderIds.Tmdb) : undefined,
      resolution: (item.MediaStreams?.find(s => s.Type === 'Video')?.Width ?? 0) >= 3840 ? '4K' : '1080p',
      codec: item.MediaStreams?.find(s => s.Type === 'Video')?.Codec,
      audioCodec: item.MediaStreams?.find(s => s.Type === 'Audio')?.Codec,
    }
  }

  async getStreamURL(id: string): Promise<string> {
    // Emby 直接播放 URL
    return `${this.config.url}/emby/Videos/${id}/stream?api_key=${this.config.apiKey}&Static=true`
  }

  exportConfig(): DataSourceConfig {
    return { ...this.config }
  }
}
```

### 4.4 OpenList/Alist DataSource 实现

OpenList/Alist 使用自身 HTTP JSON API：账号密码调用 `/api/auth/login` 获取 token，目录和搜索调用 `/api/fs/*`，播放使用 `/d{path}` 与服务端返回的签名。它不是 WebDAV DataSource。

```typescript
// src/services/datasource/alist.ts

export class AlistDataSource implements DataSource {
  readonly type: DataSourceType = 'alist'
  private config!: DataSourceConfig
  private baseURL!: string

  async init(config: DataSourceConfig): Promise<void> {
    this.config = config
    this.baseURL = config.url.replace(/\/$/, '')
  }

  async test(): Promise<boolean> {
    try {
      const res = await fetch(`${this.baseURL}/api/public/settings`)
      return res.ok
    } catch {
      return false
    }
  }

  async list(path?: string): Promise<MediaItem[]> {
    const res = await fetch(`${this.baseURL}/api/fs/list`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: path || '/', password: this.config.password || '' }),
    })
    const data = await res.json()
    if (data.code !== 200 || !data.data?.content) return []
    return data.data.content.map((item: any) => ({
      id: item.name,
      name: item.name,
      type: item.is_dir ? 'folder' : this.getMediaType(item.name),
      path: `${path || ''}/${item.name}`.replace(/\/+/g, '/'),
      size: item.size,
      modified: item.modified,
    })) || []
  }

  async getStreamURL(id: string): Promise<string> {
    // Alist 直接下载/播放 URL
    return `${this.baseURL}/d${id}`
  }

  // ... 其他方法
}
```

#### CloudDrive2、夸克网盘、123 云盘与通用 WebDAV 协议边界

- `CloudDrive2DataSource` 使用 CloudDrive2 官方 gRPC API，只接受服务地址和用户创建的应用 API Token。Tauri Rust 负责 `GetSubFiles`、`GetSearchResults` 和 `GetDownloadUrlPath`，Bearer Token 只存在于凭据边界和瞬时原生请求中。
- `QuarkDataSource` 固定访问夸克官方 HTTPS Web API，不提供用户可编辑 API Base URL。设置页默认提供夸克 App 扫码登录，也可打开夸克官方账号登录窗口处理账号密码、验证码和设备风控；手动 Cookie 仅作为高级兜底。三种入口最终都只保存 Cookie credential envelope，Player 不保存夸克账号密码。
- 夸克扫码登录由 Rust 向官方 `uop.quark.cn` 获取 token、在本机生成二维码、轮询 service ticket，再向 `pan.quark.cn/account/info` 换取 Cookie；二维码 token 不发送给第三方二维码服务。账号登录使用独立官方 WebView，并在登录完成后读取 `quark.cn` Cookie，再通过固定 `/config` 请求验证。
- 夸克目录浏览使用 provider path `/...` 与内部 `fid` 映射，播放时才通过 `/file/download` 获取临时原始直链，并附带 Cookie、Referer、User-Agent 和受控 `x-urlp`。服务端轮换的 `__puus` / `__pus` 必须立即合并回凭据边界，直链和 Header 不持久化。
- `Pan123DataSource` 固定访问 123 云盘官方 HTTPS API。设置页支持手机号/邮箱与密码登录，也支持高级访问令牌导入；账号模式保存独立的 `123` credential envelope，使短期 token 过期后可在 Rust 原生层重新登录并立即轮换安全凭据，普通配置只保存 `credentialRef` 与 `rootPath`。
- 123 云盘目录和搜索由 Rust 生成网页 API 动态 CRC32 签名，使用 provider path `/...` 与内部 `FileId` / `Etag` / `S3KeyFlag` 映射；播放时才请求 `download_info`、解析受控 Base64 参数并解析有限重定向。Access Token、账号密码、动态签名、下载直链和播放 Referer 不进入普通配置、扫描缓存、日志或诊断。
- `WebDavDataSource` 是独立通用数据源，使用 WebDAV URL、用户名、密码、`PROPFIND` 与 Basic Auth。它不冒充 CloudDrive2，也不复用 CloudDrive2 API Token。
- 四类数据源都只读，支持用户选择 `extra.rootPath`、本地 raw scan cache、海报墙、Home 聚合和全量/增量扫描。

### 4.5 DataSourceManager

```typescript
// src/services/datasource/manager.ts

export class DataSourceManager {
  private sources: Map<string, DataSource> = new Map()

  async addSource(config: DataSourceConfig): Promise<DataSource> {
    const source = this.createDataSource(config.type)
    await source.init(config)
    this.sources.set(source.id, source)
    // 持久化到本地配置
    await this.saveConfig()
    return source
  }

  async removeSource(id: string): Promise<void> {
    const source = this.sources.get(id)
    if (source) {
      source.destroy()
      this.sources.delete(id)
      await this.saveConfig()
    }
  }

  getAllSources(): DataSource[] {
    return Array.from(this.sources.values())
  }

  getSource(id: string): DataSource | undefined {
    return this.sources.get(id)
  }

  // 跨数据源搜索
  async searchAll(keyword: string): Promise<MediaItem[]> {
    return searchAcrossDataSources(this.getOrderedSources(), keyword, {
      limitPerSource: 18,
      limit: 60,
    })
  }

  // 导出所有配置（用于同步给Server）
  exportAllConfigs(): DataSourceConfig[] {
    return Array.from(this.sources.values()).map(s => s.exportConfig())
  }

  // 从Server导入配置
  async importConfigs(configs: DataSourceConfig[]): Promise<void> {
    for (const config of configs) {
      const exists = Array.from(this.sources.values()).some(s => s.name === config.name)
      if (!exists) {
        await this.addSource(config)
      }
    }
  }

  private createDataSource(type: DataSourceType): DataSource {
    switch (type) {
      case 'emby': return new EmbyDataSource()
      case 'jellyfin': return new JellyfinDataSource()
      case 'alist': return new AlistDataSource()
      case 'clouddrive2': return new CloudDrive2DataSource()
      case 'webdav': return new WebDavDataSource()
      case 'server': return new ServerDataSource()
      // 占位
      case '115': throw new Error('115网盘支持即将推出')
      case '123': return new Pan123DataSource()
      case 'quark': return new QuarkDataSource()
      default: throw new Error(`Unknown data source type: ${type}`)
    }
  }
}
```

聚合搜索由 `AppLayout` 提供全局搜索工作台，不再嵌入首页 Hero。桌面顶部中央导航固定为 `首页 / 搜索 / 设置`，点击搜索后在窗口标题栏下方展开大尺寸液态玻璃区域；手机在首页真实滚动容器位于顶部时下拉打开全屏搜索。未输入关键词时使用当前安全媒体快照展示海报推荐和可点击建议词，输入后并行查询所有已启用 DataSource，按数据源顺序合并，以 `sourceId + itemId` 去重并限制单源/总结果数。单个数据源超时或失败只丢弃该源结果，不能让整次搜索报错；用户可按数据源、该来源的媒体库和媒体类型筛选，结果显示海报、标题、年份、媒体类型和来源，可进入详情或直接播放。普通输入不会隐式请求 Server 的 TMDB 发现；只有用户点击底部“从 Server 搜索更多并入库”，且已连接 Server 的 Bootstrap capability 包含 `discovery_search` 时才进入 Server 海报结果。Server 发现详情与本地详情复用同一 Hero/元数据主体，播放动作替换为搜索、直接搜索、订阅和分步入库，并独立降级 coverage/acquisition 状态。

### 4.6 配置存储

Player 使用统一 Rust `storage` layout，应用数据库不写入安装目录，也不再依赖 WebView localStorage。Windows 默认结构：

```text
%LOCALAPPDATA%/com.ohmycine.
├── data/
│   ├── settings.sqlite
│   ├── credentials.sqlite
│   ├── master.key
│   ├── playback_history.sqlite
│   ├── player_preferences.sqlite  # 全局播放偏好 + 按 source/media 保存的单视频播放设置
│   └── raw_scan_cache.sqlite
├── cache/
└── logs/
    └── render-diagnostics.log
```

`settings.sqlite` 保存数据源非敏感配置、主题、TMDB 非敏感设置、分类规则和扫描计划。标准模式升级后的首次启动会把旧 WebView localStorage key 导入 SQLite，成功后删除；localStorage 只保留浏览器/Vite fallback。旧 `%APPDATA%/com.ohmycine.player` 下的 SQLite 文件也只在标准模式自动迁移到统一 `data` 目录，迁移不得覆盖已有新文件。

EXE 同目录存在 `portable.flag` 或使用 `--portable` 时启用便携模式：

```text
OhMyCine/
├── ohmycine-player.exe
├── portable.flag
├── data/
├── cache/
└── logs/
```

正式 portable ZIP 必须自带 `portable.flag`，安装包不得包含。便携模式携带 Player 自有配置、数据库和日志；WebView2 的网页/GPU 缓存仍是 Windows 管理的可丢弃机器缓存，不作为配置来源。标准模式与便携模式是完全隔离的配置档案：没有 `data` 的全新便携目录必须空白启动，便携模式不自动导入标准目录、旧 Roaming 目录或共享 WebView localStorage；已有便携 `data` 则继续复用。未来如提供标准数据导入，必须由用户显式操作。

便携目录位于 UNC、WSL 映射或其他网络式路径时仍可运行，但 SQLite、日志和缓存的频繁小文件读写可能明显变慢。Player 应在设置诊断页提示用户把完整便携目录移动到 Windows 本地磁盘（例如 `C:\OhMyCine-Portable`）后运行。

标准模式按平台保护 AES 主密钥：Windows 使用 DPAPI，Android 使用 Keystore，macOS/iOS 使用 Apple Keychain，Linux 优先使用 Secret Service/libsecret；Linux 系统存储不可用时才降级为权限受限文件密钥并在设置页提示。旧文件主密钥原地迁移且不轮换，已有凭据数据库缺失主密钥时禁止生成新钥匙。便携模式为了能随目录移动，继续使用目录内文件密钥，因此整个便携文件夹都应视为敏感数据，并在设置页明确显示低于系统安全存储的保护等级。

### 4.7 配置同步机制（延期）

当前 Player ↔ Server 接入实现 `ServerDataSource`、设备令牌、媒体目录、跨源身份去重、安全播放、播放历史/进度同步，以及由用户显式触发的 Server 发现与入库。Server 发现多站搜索由 Tauri 原生 SSE 桥接承载 Bearer，严格限制 Player discovery stream 路径、事件类型、单事件/累计大小、redirect 与 idle timeout；取消、新搜索和离开页面都会终止旧流。Player 不同步数据源配置或凭据，连接 Server 不会自动 push/pull，也不会把 Player 本地数据源导入 Server。

后续若启用配置同步，必须由用户显式选择方向与范围，默认只允许非敏感结构字段；下面代码仅表示未来接口形态，不是当前行为：

```typescript
// src/services/sync.ts

export class ConfigSync {
  constructor(
    private dsManager: DataSourceManager,
    private serverClient: ServerClient,
  ) {}

  // 推送 Player 配置到 Server
  async pushToServer(): Promise<void> {
    const configs = this.dsManager.exportAllConfigs()
    await this.serverClient.post('/api/v1/sync/push', { datasources: configs })
  }

  // 从 Server 拉取配置
  async pullFromServer(): Promise<void> {
    const configs = await this.serverClient.get('/api/v1/sync/pull')
    await this.dsManager.importConfigs(configs.datasources)
  }

}
```

### 4.8 签名自动更新

Windows 使用 Tauri updater 的 minisign 信任根，不直接下载 GitHub EXE 覆盖自身；Android 使用同一 Release 选择逻辑但采用受控 APK 安装链：

```text
GitHub Releases API
  → 按 Beta / Stable 选择 Release
  → 固定仓库 latest.json
  → Tauri updater 校验 manifest + NSIS 签名
  → 用户确认
  → 下载进度
  → Windows NSIS 安装并重启

Android:
GitHub Releases API
  → 固定仓库 ARM64 APK + SHA-256 asset
  → 限制 HTTPS 重定向域名、响应大小和缓存目录
  → Rust 校验 SHA-256
  → Android FileProvider + 系统安装确认
```

- Beta 渠道选择最新非草稿发布，包括 prerelease 和正式发布；Stable 只选择非草稿且非 prerelease。
- GitHub API 和 manifest URL 固定到 `yuanjing-hash/OhMyCine`，不接受用户自定义更新服务器。
- 启动自动检测和设置页手动检测复用同一个 Pinia updater store，并合并并发检查。
- 发现更新只弹确认窗，不静默安装。下载完成后仍由 Tauri updater 使用内置公钥验证签名。
- 普通设置只保存 `autoCheck` 和 `channel`，不保存私钥、签名或临时下载 URL。
- 标准模式使用默认 NSIS 安装；便携模式向安装器传入当前 EXE 目录，保留 `portable.flag` 和便携数据目录。
- 普通本地构建不要求签名私钥。GitHub Release 构建额外启用 `tauri.updater.conf.json`，生成 `.sig` 和 `latest.json`。
- Android 不静默安装。首次更新需要用户在系统页面允许 OhMyCine 安装未知应用；APK 只写入应用 cache 的 `updates/`，FileProvider 不暴露外部存储或整个 cache。
- GitHub Android 预览包从首个 updater 版本起使用固定 preview keystore。keystore 与密码只存本机受限备份和 GitHub Actions Secrets，不进入仓库；历史随机 debug 签名包需要一次卸载重装，之后同一签名可覆盖升级。

## 5. 网盘自动刮削系统

### 5.1 设计背景

Emby/Jellyfin 自带刮削功能，但 OpenList/Alist/CloudDrive2/夸克网盘/WebDAV 这类原始文件数据源**没有元数据**——只有原始文件名。Player 需要自己实现刮削，为网盘文件生成海报墙。

本系统只面向“原始文件源”：OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV、本地文件以及未来类似的自定义文件源。Emby/Jellyfin 已经由服务端维护媒体库和元数据，默认不套用 Player 本地刮削分类规则。

刮削系统必须遵守三条边界：

1. **只读远端**：Player 不对 OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV 或本地源执行上传、重命名、移动、删除、创建目录等写操作。
2. **本地缓存**：扫描日志、匹配结果、海报、背景图、用户修正和分类结果都保存在 Player 本地 app data；后续右键识别、手动选择 TMDB 结果、海报/剧照上传和元信息编辑都属于本地覆盖层，只写本地 app data/cache，不写回 OpenList/Alist。
3. **任意根目录**：从用户选择的根目录开始自动识别结构，不要求物理目录顶层必须叫 `movie`、`tv`、`Movies` 或 `TV`。

### 5.2 刮削流程

```
用户选择的根目录 (OpenList/Alist / CloudDrive2 gRPC / WebDAV / 本地文件夹)
        │
        ▼
递归只读扫描 + 视频文件过滤
        │
        ▼
路径结构采样
  → 自动判断 standard / nonStandard
        │
        ├─ 标准目录模式
        │    → 从路径推断电影/剧集/季/集/年份/可能分类
        │
        └─ 非标准目录模式
             → 把视频文件按散文件处理
        │
        ▼
文件名解析
  "Inception.2010.2160p.UHD.BluRay.x265.TrueHD.Atmos.7.1-FGT.mkv"
  → { title: "Inception", year: 2010, resolution: "2160p", codec: "H.265", ... }
        │
        ▼
TMDB API 查询与详情补全
  → { tmdb_id, poster_url, overview, genres, genre_ids, rating, cast, ... }
        │
        ▼
本地缓存 (SQLite + 海报图片)
  → 数据库记录 + cache/posters/{tmdb_id}.jpg
        │
        ▼
通用分类规则
  → 逻辑分类：华语电影 / 外语电影 / 综艺 / 国产剧 / 未分类...
        │
        ▼
海报墙 / 详情页 / 播放
```

正式发布包通过 CI Secret 注入 OhMyCine 应用级 TMDB Read Access Token，提供开箱即用的默认元数据通道；用户仍可在安全凭证边界中保存自己的 TMDB token/key，并优先覆盖内置凭据。应用级凭据不会进入 Git 源码、普通配置、日志或导出，但它最终存在于发布二进制中，应按可提取、可撤销、可限流的应用凭据管理，不能与用户秘密凭据使用同一安全假设。若当前自编译版本未注入内置凭据、用户也未配置自定义凭据，或 TMDB 超时/请求失败，扫描仍应保留可播放候选、目录识别、文件名解析和季集结构；这些 `notConfigured` / `notFound` / `failed` / `skipped` 等未完成匹配条目统一进入 `未识别`。

### 5.2.1 标准目录模式

标准目录模式不是固定目录名，而是扫描器对用户选择根目录下面的结构进行评分后的结果。以下结构都可以作为标准模式信号：

- `分类/片名 (年份)/片名.mkv`
- `片名 (年份)/片名.mkv`
- `分类/剧名/Season 01/S01E01.mkv`
- `剧名/Season 01/S01E01.mkv`
- `剧名/第01集.mkv`

路径识别采用 MoviePilot-like 的合并顺序：先解析文件名 stem，再解析父目录，再解析祖父目录，并用后续层级补齐缺失的身份字段。`Season 01` / `S01` / `第1季` 这类父目录只提供季信息，不作为标题；`S01E01.mkv` 这类文件名只提供季集信息，不作为标题；在 `剧名/Season 01/S01E01.mkv` 中，祖父目录才是剧名候选。带发布源/制作组噪声的作品目录，例如 `机械之声的传奇 The Legend of Vox Machina AMZN GrassTV`，应清洗出 `机械之声的传奇` / `The Legend of Vox Machina` 作为搜索标题，而不能成为媒体库 root 分类。

Player 的独立识别器以 Unicode NFC 和 Unicode letter/number/mark 边界处理标题，不使用“只允许中文或 ASCII”的脚本白名单。季集 token pack 覆盖 `S01E02`、`1x02`、中英日韩及常见欧洲语言标签；只有删除后仍存在有效作品标题时，才移除可能与合法片名冲突的自然语言 token，因此《第八集》、`[REC]`、`Spider-Man` 等整标题必须保留。TMDB 自动识别先让 file、parent、grandparent 的 canonical 标题公平获得查询机会，再消费噪声回退；单作品搜索最多 10 次、详情补全最多 3 个身份。最终匹配同时使用本地化标题、原标题、别名、翻译、年份、媒体类型和季集结构，并以确定性置信度及候选间距拒绝低置信或身份冲突，不接受 provider 返回数组的首项作为身份。

Player 与 Server 使用同一个脱敏 `provider-neutral-v1` 识别 corpus 作为跨实现契约。Player 识别缓存写入独立引擎版本和 `automatic` / `manual` 来源；引擎升级只使旧自动失败或旧自动派生结果重新计算，已有人工 TMDB 身份、人工元数据/图片覆盖、文件扫描事实、数据源配置、凭据和播放历史不得被清空或覆盖。

示例：

```text
Movies/华语电影/片名 (年份)/片名.mkv
TV/综艺/剧名/Season 01/S01E01.mkv
TV/国产剧/剧名/Season 01/S01E01.mkv
影视库/华语电影/片名 (年份)/片名.mkv
影视库/综艺/剧名/Season 01/S01E01.mkv
```

这些示例中的 `Movies` / `TV` 只是可识别路径的一种，不是强制目录名。用户也可以直接选择 `华语电影`、`综艺` 或任何其它根目录，扫描器必须从该根目录下面继续推断。

评分信号包括：

- 目录层级是否稳定表达“分类 / 标题 / 季 / 集”。
- 文件名是否包含年份、`S01E01`、`Season 01`、`第01集` 等常见信息。
- 同一剧名目录下是否聚合多集、多季。
- 单个电影目录下是否通常只有一个主视频和相关字幕/花絮。
- 同一目录是否混杂大量互不相关标题。

低置信度时，扫描不应失败。Player 可以按推荐模式先执行，并在扫描日志或设置中允许用户切换模式后重新扫描。

### 5.2.2 数据源页呈现

OpenList/Alist、CloudDrive2、WebDAV、本地文件等原始文件源在完成本地扫描后，数据源首页应与 Emby/Jellyfin 保持同一用户心智：顶部是大海报/背景轮播，下面是媒体库卡片，再进入具体分类的作品海报墙。只有 TMDB `matched` 条目才进入正式分类优先级：标准目录优先使用明确的路径分类目录作为媒体库分类，例如 `动漫`、`综艺`、`国产剧`；非标准目录或没有清晰路径分类时，再使用 TMDB 元数据和本地分类规则生成分类。分类不应来自作品目录名或发布组噪声目录名。

未完成 TMDB 匹配的条目统一显示在 `未识别` 分类。即使路径已经解析出电影、剧集、分类提示、季号或集号，也只把这些结构作为 `未识别` 内部的作品/季集聚合和后续手动识别依据，不把它们提升为媒体库分类卡片。

扫描管理是辅助功能。已有扫描缓存时，不在媒体库标题旁常驻维护按钮；桌面端把“重新刮削”“扫描管理”和“文件夹”放入全局右侧悬浮菜单，手机端把同一组当前媒体库操作放入“快捷”Bottom Sheet，确保触摸设备不依赖 hover。扫描状态、结构判断、日志、全量扫描和增量扫描放在展开后的扫描管理区域。首次进入原始文件源且本地 scan cache 尚未生成时，媒体库区域应显示当前源/root 的自动索引进度、状态和可进入文件夹视图的兜底入口，而不是空媒体库。文件夹视图保留为兜底入口，继续通过 DataSource `list()` 只读浏览和播放，但不替代默认媒体库视图。

原始文件源使用双通道扫描：`full` 全量扫描默认 6 小时一次，负责完整递归扫描和一致性校准；`incremental` 增量扫描默认 1 分钟一次，先对比 provider path、大小和修改时间，有新增、删除或修改时再刷新本地索引。设置页按数据源保存 `extra.rawSourceScanSchedule`，可分别启停全量/增量并调整间隔。当前覆盖 OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV 和本地文件夹；本地文件夹通过 Tauri root-scoped watcher 监听变更，事件只用于标记 source/root 需要增量扫描，前端和缓存仍只使用 `/...` provider path，不展示或持久化本地绝对路径。OpenList/Alist、CloudDrive2、夸克网盘、123 云盘与 WebDAV 暂以短间隔 polling/diff 实现近实时增量；Emby/Jellyfin 使用服务端媒体库和元数据，不进入 Player 原始文件扫描调度。

### 5.2.3 非标准目录模式

非标准目录模式默认目录信息不可靠，适合所有影片、剧集、综艺混在一个或多个文件夹里的情况。此模式主要依赖文件名解析和 TMDB 匹配：

- 每个视频文件先作为独立候选项。
- 解析标题、年份、季、集、分辨率、片源、编码、音频和制作组。
- 剧集文件按标题 + 季集号在本地聚合成剧集。
- 无法完成 TMDB 匹配的文件保留为 `未识别`，仍然可以从文件夹视图或未识别列表播放。
- 如果已经解析出剧集标题、季号和集号，`未识别` 分类内部仍按作品/季/集聚合，便于后续右键识别或手动选择 TMDB 结果。
- 用户修正、右键识别、手动 TMDB 结果选择、海报/剧照上传和元信息编辑写入本地 override 表或本地缓存，不写回网盘。

### 5.2.4 通用分类规则

分类规则是刮削后的**本地逻辑分组**，不是物理目录约束。它影响海报墙分组、筛选、媒体库标签、聚合首页和未来 AI 推荐上下文，但不移动或重命名远端文件。分类规则只作用于已完成 TMDB 匹配的条目；未完成匹配的条目统一显示为 `未识别`。

规则适用于 OpenList/Alist、CloudDrive2、WebDAV、本地文件等原始文件源；Emby/Jellyfin 默认使用服务端已有分类与元数据。

分类规则支持以下 TMDB 字段：

- `genre_ids`
- `original_language`
- `production_countries`（电影）
- `origin_country`（剧集）
- `release_year`
- 后续可扩展 TMDB 详情接口中的其它一级字段

多个条件需要同时满足；同一条件可以选择多个值；每个条件支持“包含/排除”。内部可以表达为结构化 JSON，普通用户不直接编辑 YAML/JSON。

用户提供的 MP 风格配置只作为**默认实例**，用于初始化内置分类，不代表规则格式必须暴露为 YAML，也不代表目录必须按这些名字存在。

默认显式分类建议：

- 电影分类：动画电影、华语电影、外语电影
- 剧集分类：国漫、日番、纪录片、儿童、综艺、国产剧、欧美剧、日韩剧
- 兜底分类：电影和剧集都默认为 `未分类`

这些默认分类在 UI 中呈现为可编辑的受控规则。例如“动画电影”选择 TMDB movie genre `Animation`，“外语电影”是电影侧显式规则，“综艺”选择 TMDB TV genre `Reality` / `Talk`，“国产剧”选择 origin country `CN` / `TW` / `HK`。

### 5.2.5 分类设置页

设置页中新增“刮削与分类”，与“管理数据源”同级，进入后管理 TMDB 默认通道、自定义凭据、默认语言地区、扫描模式和分类规则。正式包没有用户自定义凭据时显示“内置通道可用”；用户凭据存在时显示“使用自定义凭据”，清除后恢复内置通道。自编译版本未注入应用凭据时必须明确显示当前构建未提供凭据，不能假装在线刮削可用。

分类规则编辑必须使用受控设置页：

- 页面分为“电影分类”和“剧集分类”两组。
- 每组标题右侧提供 `+` 添加分类。
- 分类名由用户填写，例如 `华语电影`、`动画电影`、`综艺`。
- 类型/题材使用 TMDB 官方 genre 多选；电影分类只展示 TMDB movie genre，剧集分类只展示 TMDB TV genre。
- 语种、国家/地区和年份范围使用多选、开关、范围输入等控件。
- 包含/排除通过切换控件表达，不要求用户理解 `!value` 语法。
- 分类顺序可调整，匹配时从上到下命中第一个满足条件的分类。
- 每组保留不可删除的兜底分类，电影和剧集默认都为 `未分类`，可以改名。
- 高级导入/导出可以后置，MVP 不把自由文本配置作为主流程。

内部结构示例：

```json
{
  "version": 1,
  "groups": [
    {
      "mediaType": "movie",
      "categories": [
        {
          "name": "动画电影",
          "conditions": {
            "genreIds": { "include": [16], "exclude": [] },
            "originalLanguages": { "include": [], "exclude": [] },
            "productionCountries": { "include": [], "exclude": [] },
            "releaseYear": null
          }
        }
      ],
      "fallbackCategoryName": "未分类"
    },
    {
      "mediaType": "tv",
      "categories": [
        {
          "name": "综艺",
          "conditions": {
            "genreIds": { "include": [10764, 10767], "exclude": [] },
            "originCountries": { "include": [], "exclude": [] },
            "releaseYear": null
          }
        }
      ],
      "fallbackCategoryName": "未分类"
    }
  ]
}
```

### 5.3 文件名解析器

```typescript
// src/services/scraper/parser.ts

export interface ParsedFilename {
  title: string
  year?: number
  resolution?: string      // 2160p, 1080p, 720p
  source?: string          // BluRay, WEB-DL, HDTV
  videoCodec?: string      // H.265, H.264, AV1
  audioCodec?: string      // DTS-HD, TrueHD, Atmos, AAC
  releaseGroup?: string
  mediaType: 'movie' | 'tv'
  season?: number
  episode?: number
}

// 使用 parse-torrent-name 库
import PTN from 'parse-torrent-name'

export function parseFilename(filename: string): ParsedFilename {
  const cleanName = filename.replace(/\.[^.]+$/, '') // 去掉扩展名
  const parsed = PTN(cleanName)

  return {
    title: parsed.title,
    year: parsed.year,
    resolution: parsed.resolution,
    source: parsed.source,
    videoCodec: parsed.codec,
    audioCodec: parsed.audio,
    releaseGroup: parsed.group,
    mediaType: parsed.season ? 'tv' : 'movie',
    season: parsed.season,
    episode: parsed.episode,
  }
}
```

### 5.4 TMDB 刮削服务

```typescript
// src/services/scraper/tmdb.ts

export class TmdbScraper {
  private apiKey?: string
  private baseURLs = ['https://api.tmdb.org/3', 'https://api.themoviedb.org/3']
  private imageBase = 'https://image.tmdb.org/t/p'

  constructor(apiKey?: string) {
    this.apiKey = apiKey
  }

  async search(title: string, year?: number): Promise<TmdbResult | null> {
    if (!this.apiKey) return null

    const params = new URLSearchParams({
      api_key: this.apiKey,
      query: title,
      language: 'zh-CN',
    })
    if (year) params.set('year', year.toString())

    const res = await fetch(`${this.baseURL}/search/movie?${params}`)
    const data = await res.json()

    if (!data.results?.length) return null

    // 优先匹配年份
    const best = year
      ? data.results.find((r: any) => r.release_date?.startsWith(year.toString())) || data.results[0]
      : data.results[0]

    return this.getDetail(best.id)
  }

  async getDetail(tmdbId: number): Promise<TmdbResult> {
    const params = new URLSearchParams({
      api_key: this.apiKey,
      language: 'zh-CN',
      append_to_response: 'credits,images',
    })

    const res = await fetch(`${this.baseURL}/movie/${tmdbId}?${params}`)
    const data = await res.json()

    return {
      tmdbId: data.id,
      title: data.title,
      originalTitle: data.original_title,
      year: Number(data.release_date?.slice(0, 4)),
      overview: data.overview,
      rating: data.vote_average,
      genres: data.genres.map((g: any) => g.name),
      posterUrl: data.poster_path ? `${this.imageBase}/w500${data.poster_path}` : null,
      backdropUrl: data.backdrop_path ? `${this.imageBase}/w1280${data.backdrop_path}` : null,
      runtime: data.runtime,
      directors: data.credits?.crew?.filter((c: any) => c.job === 'Director').map((c: any) => c.name) || [],
      cast: data.credits?.cast?.slice(0, 10).map((c: any) => c.name) || [],
    }
  }
}
```

Player 默认优先访问 TMDB 的短域名 `https://api.tmdb.org/3`，网络失败或超时时才回退到 `https://api.themoviedb.org/3`；HTTP 401/403 等凭据错误不得通过切换域名掩盖或重复请求。图片继续使用 `https://image.tmdb.org/t/p` 并进入受控本地图片缓存。该双入口只能提高部分国内网络环境的直连成功率，不能承诺绕过所有地区网络限制；公共第三方镜像不得作为内置默认值。

用户可在“刮削与分类”中分别配置 TMDB API 与图片 HTTPS 代理前缀，两项完全独立，可以只配置其中一个。地址不得包含账号密码、查询参数或片段；API 地址通过已知电影详情请求验证，图片地址通过固定 TMDB 海报路径验证，各自只有测试成功才保存并立即启用，失败只保留该项上一次验证通过的地址且不影响另一项。默认官方 API 仍保留短域名到旧域名的网络故障回退，自定义 API 代理只访问用户选择的单一地址且禁用重定向，避免凭据在故障时被静默发送到其它域名。Tauri 桌面和 Android 的测试与实际刮削统一走 Rust 原生请求，浏览器开发环境才使用 `fetch` fallback；已有扫描缓存根据保存的 TMDB 图片路径动态套用当前图片地址，无需清空媒体库缓存。

### 5.5 本地元数据数据库

```typescript
// src/services/scraper/database.ts

// 使用 Tauri 的 SQLite 插件 (tauri-plugin-sql)
import Database from 'tauri-plugin-sql-api'

export interface MediaRecord {
  id: number
  filePath: string           // 网盘路径 (如 /movies/Inception.mkv)
  fileName: string
  dataSourceId: string       // 来自哪个数据源
  mediaType: 'movie' | 'tv'
  tmdbId?: number
  title?: string
  year?: number
  overview?: string
  rating?: number
  genres?: string            // JSON 数组
  directors?: string         // JSON 数组
  cast?: string              // JSON 数组
  posterPath?: string        // 本地缓存海报路径
  backdropPath?: string
  resolution?: string
  codec?: string
  audioCodec?: string
  releaseGroup?: string
  scrapedAt?: string
  fileModifiedAt: string
}

export class MetadataDB {
  private db!: Database

  async init() {
    this.db = await Database.load('sqlite:ohmycine.db')

    await this.db.execute(`
      CREATE TABLE IF NOT EXISTS media (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        file_path TEXT NOT NULL,
        file_name TEXT NOT NULL,
        data_source_id TEXT NOT NULL,
        media_type TEXT NOT NULL DEFAULT 'movie',
        tmdb_id INTEGER,
        title TEXT,
        year INTEGER,
        overview TEXT,
        rating REAL,
        genres TEXT,
        poster_path TEXT,
        backdrop_path TEXT,
        resolution TEXT,
        codec TEXT,
        audio_codec TEXT,
        directors TEXT,
        cast_list TEXT,
        release_group TEXT,
        scraped_at TEXT,
        file_modified_at TEXT NOT NULL,
        UNIQUE(file_path, data_source_id)
      )
    `)

    await this.db.execute(`
      CREATE INDEX IF NOT EXISTS idx_media_type ON media(media_type)
    `)
    await this.db.execute(`
      CREATE INDEX IF NOT EXISTS idx_media_year ON media(year)
    `)
    await this.db.execute(`
      CREATE INDEX IF NOT EXISTS idx_media_rating ON media(rating)
    `)
  }

  async upsert(record: Partial<MediaRecord>): Promise<void> {
    await this.db.execute(
      `INSERT INTO media (file_path, file_name, data_source_id, media_type, tmdb_id, title, year, overview, rating, genres, directors, cast_list, poster_path, backdrop_path, resolution, codec, audio_codec, release_group, scraped_at, file_modified_at)
       VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
       ON CONFLICT(file_path, data_source_id) DO UPDATE SET
         tmdb_id=$5, title=$6, year=$7, overview=$8, rating=$9, genres=$10,
         directors=$11, cast_list=$12, poster_path=$13, backdrop_path=$14,
         resolution=$15, codec=$16, audio_codec=$17, release_group=$18,
         scraped_at=$19, file_modified_at=$20`,
      [
        record.filePath, record.fileName, record.dataSourceId, record.mediaType,
        record.tmdbId, record.title, record.year, record.overview, record.rating,
        record.genres, record.directors, record.cast, record.posterPath,
        record.backdropPath, record.resolution, record.codec, record.audioCodec,
        record.releaseGroup, record.scrapedAt, record.fileModifiedAt,
      ]
    )
  }

  async search(keyword: string): Promise<MediaRecord[]> {
    return this.db.select(
      `SELECT * FROM media WHERE title LIKE $1 ORDER BY rating DESC`,
      [`%${keyword}%`]
    )
  }

  async list(options?: { type?: string; genre?: string; sort?: string; limit?: number; offset?: number }): Promise<MediaRecord[]> {
    let query = 'SELECT * FROM media WHERE 1=1'
    const params: any[] = []

    if (options?.type) {
      query += ' AND media_type = $' + (params.length + 1)
      params.push(options.type)
    }
    if (options?.genre) {
      query += ' AND genres LIKE $' + (params.length + 1)
      params.push(`%${options.genre}%`)
    }

    query += ` ORDER BY ${options?.sort || 'rating'} DESC`

    if (options?.limit) {
      query += ' LIMIT $' + (params.length + 1)
      params.push(options.limit)
    }
    if (options?.offset) {
      query += ' OFFSET $' + (params.length + 1)
      params.push(options.offset)
    }

    return this.db.select(query, params)
  }

  async needsScraping(filePath: string, fileModifiedAt: string): Promise<boolean> {
    const rows = await this.db.select(
      'SELECT scraped_at, file_modified_at FROM media WHERE file_path = $1',
      [filePath]
    )
    if (!rows.length) return true
    return !rows[0].scraped_at || rows[0].file_modified_at !== fileModifiedAt
  }
}
```

### 5.6 刮削引擎

```typescript
// src/services/scraper/engine.ts

export class ScrapingEngine {
  constructor(
    private db: MetadataDB,
    private tmdb: TmdbScraper,
    private posterCache: PosterCache,
  ) {}

  // 扫描数据源并刮削
  async scanAndScrape(dataSource: DataSource): Promise<void> {
    const files = await this.walkDirectory(dataSource)

    for (const file of files) {
      if (!this.isVideoFile(file.name)) continue

      const needsScrape = await this.db.needsScraping(file.path, file.modified)
      if (!needsScrape) continue

      // 解析文件名
      const parsed = parseFilename(file.name)

      // 查询 TMDB
      let tmdbData: TmdbResult | null = null
      try {
        tmdbData = await this.tmdb.search(parsed.title, parsed.year)
      } catch (e) {
        console.warn(`TMDB search failed for ${parsed.title}:`, e)
      }

      // 缓存海报
      let posterPath: string | undefined
      if (tmdbData?.posterUrl) {
        posterPath = await this.posterCache.cache(tmdbData.posterUrl, tmdbData.tmdbId)
      }

      // 写入数据库
      await this.db.upsert({
        filePath: file.path,
        fileName: file.name,
        dataSourceId: dataSource.id,
        mediaType: parsed.mediaType,
        tmdbId: tmdbData?.tmdbId,
        title: tmdbData?.title || parsed.title,
        year: tmdbData?.year || parsed.year,
        overview: tmdbData?.overview,
        rating: tmdbData?.rating,
        genres: tmdbData?.genres ? JSON.stringify(tmdbData.genres) : undefined,
        directors: tmdbData?.directors ? JSON.stringify(tmdbData.directors) : undefined,
        cast: tmdbData?.cast ? JSON.stringify(tmdbData.cast) : undefined,
        posterPath,
        backdropPath: tmdbData?.backdropUrl,
        resolution: parsed.resolution,
        codec: parsed.videoCodec,
        audioCodec: parsed.audioCodec,
        releaseGroup: parsed.releaseGroup,
        scrapedAt: new Date().toISOString(),
        fileModifiedAt: file.modified,
      })
    }
  }

  // 递归遍历目录
  private async walkDirectory(ds: DataSource, path?: string): Promise<FileEntry[]> {
    const items = await ds.list(path)
    const results: FileEntry[] = []

    for (const item of items) {
      if (item.type === 'folder') {
        const children = await this.walkDirectory(ds, item.path)
        results.push(...children)
      } else {
        results.push({
          name: item.name,
          path: item.path,
          modified: item.modified || '',
        })
      }
    }

    return results
  }

  private isVideoFile(name: string): boolean {
    return /\.(mkv|mp4|avi|mov|wmv|flv|webm|m4v|ts|rmvb)$/i.test(name)
  }
}
```

### 5.7 海报缓存

Player 的受控图片缓存由 Rust `player_get_cached_image` / `player_cache_image` 和 Vue `CachedImage` 共同实现。图片二进制写入当前存储档案的 `cache/images`：Windows 标准模式位于本机应用缓存目录，便携模式位于 EXE 同目录 `cache/images`，Android 位于应用私有 `cache/images`。`data` 目录只保存 SQLite、密钥和需要长期一致性的结构数据，不混放可重建图片。

缓存键使用 `sourceId + itemId + artwork role` 后再 SHA-256，文件名和 sidecar 只保存不可逆 key/source hash、MIME、字节数和最近访问时间，不写原始 URL、API Key、签名参数或 Header。下载仅接受 HTTP(S)、同源且最多三次重定向、单图 8 MiB 上限及 JPEG/PNG/WebP/GIF/AVIF 魔数。图片缓存总容量默认 500 MB，用户可在“存储 / 诊断”中设置 100-4096 MB；写入和降低上限时按 LRU 清理最久未使用图片。前端以 IntersectionObserver 在接近视口时读取/填充缓存，Rust 通过临时 `data:` URL 把应用私有图片交给 WebView；这些临时内容不写入 `settings.sqlite` 展示快照。清除播放缓存会连同 `cache/images` 一并清理，但不会删除数据源、凭据、播放记录或全局设置。

### 5.8 刮削调度

当前 Player MVP 使用 source/root-scoped 的 `rawSourceIndexScheduler`，并区分 `full` / `incremental` 两类状态、冷却和最近执行时间。app 启动后会按每个原始文件源的 `extra.rawSourceScanSchedule` 触发后台 best-effort 调度：全量扫描默认 6 小时一次，增量扫描默认 1 分钟一次。本地文件源额外启用 Tauri 文件系统 watcher，watcher 事件只标记对应 source/root dirty，实际刷新仍走 scheduler 和 DataSource `list()`；OpenList/Alist、CloudDrive2 与 WebDAV 使用增量 polling/diff。数据源页首次无缓存时会读取当前源/root 状态并启动或绑定正在运行的全量索引任务。手动扫描可选择全量或增量；所有扫描只读取 DataSource/Tauri 安全边界并写入本地 Player cache，不阻塞文件夹浏览和播放。Emby/Jellyfin 不进入此调度。

```typescript
// src/services/scraper/scheduler.ts

export class ScrapingScheduler {
  private intervalId: number | null = null

  constructor(private engine: ScrapingEngine) {}

  // 启动定时扫描 (每30分钟)
  start(dataSources: DataSource[]) {
    this.intervalId = window.setInterval(async () => {
      for (const ds of dataSources) {
        await this.engine.scanAndScrape(ds)
      }
    }, 30 * 60 * 1000)
  }

  // 手动触发扫描
  async triggerScan(dataSource: DataSource) {
    await this.engine.scanAndScrape(dataSource)
  }

  stop() {
    if (this.intervalId) {
      clearInterval(this.intervalId)
      this.intervalId = null
    }
  }
}
```

## 6. AI 推荐助手

### 6.1 设计理念

AI 助手基于 **RAG (检索增强生成)** 架构：
1. 将用户媒体库的元数据索引为向量
2. 用户提问时，先从库中检索相关内容
3. 将检索结果 + 用户问题发给 LLM 生成推荐

**关键点**：AI 只推荐用户**本地库中已有**的影片，不会推荐用户没有的内容。

### 6.2 架构

```
用户: "我想看一部烧脑的科幻片"
        │
        ▼
向量检索 (媒体库索引)
  → 匹配: 盗梦空间、星际穿越、信条、黑客帝国、降临...
        │
        ▼
构建 Prompt (系统提示 + 检索结果 + 用户问题)
        │
        ▼
调用 LLM (用户自配 API Key)
        │
        ▼
返回推荐: "推荐《盗梦空间》(2010)，理由是..."
```

### 6.3 媒体库索引

```typescript
// src/services/ai/indexer.ts

export class MediaIndexer {
  private db: MetadataDB

  constructor(db: MetadataDB) {
    this.db = db
  }

  // 为每部影片生成文本描述 (用于向量化)
  async buildDocuments(): Promise<IndexedDocument[]> {
    const records = await this.db.list({ limit: 10000 })

    return records.map(r => ({
      id: r.id.toString(),
      text: this.buildText(r),
      metadata: {
        title: r.title,
        year: r.year,
        genres: r.genres,
        rating: r.rating,
        resolution: r.resolution,
        filePath: r.filePath,
      },
    }))
  }

  private buildText(r: MediaRecord): string {
    const directors = r.directors ? JSON.parse(r.directors) : []
    const cast = r.cast ? JSON.parse(r.cast) : []
    const parts = [
      `片名: ${r.title}`,
      r.year ? `年份: ${r.year}` : '',
      r.genres ? `类型: ${JSON.parse(r.genres).join(', ')}` : '',
      r.overview ? `简介: ${r.overview}` : '',
      r.rating ? `评分: ${r.rating}/10` : '',
      r.resolution ? `分辨率: ${r.resolution}` : '',
      r.codec ? `编码: ${r.codec}` : '',
      directors.length ? `导演: ${directors.join(', ')}` : '',
      cast.length ? `主演: ${cast.join(', ')}` : '',
    ].filter(Boolean)

    return parts.join('\n')
  }
}
```

### 6.4 向量存储 (使用浏览器本地存储)

```typescript
// src/services/ai/vector-store.ts

// 轻量级向量搜索，无需外部依赖
// 使用余弦相似度进行本地搜索

export class LocalVectorStore {
  private documents: IndexedDocument[] = []
  private embeddings: number[][] = []

  // 使用用户的 OpenAI API Key 生成 embeddings
  async index(documents: IndexedDocument[], apiKey: string, baseURL?: string): Promise<void> {
    this.documents = documents

    // 分批生成 embeddings (避免超出 token 限制)
    const batchSize = 100
    for (let i = 0; i < documents.length; i += batchSize) {
      const batch = documents.slice(i, i + batchSize)
      const response = await fetch(`${baseURL || 'https://api.openai.com'}/v1/embeddings`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${apiKey}`,
        },
        body: JSON.stringify({
          model: 'text-embedding-3-small',
          input: batch.map(d => d.text),
        }),
      })

      const data = await response.json()
      this.embeddings.push(...data.data.map((d: any) => d.embedding))
    }
  }

  // 搜索最相关的 K 个结果
  search(queryEmbedding: number[], k: number = 10): SearchResult[] {
    const similarities = this.embeddings.map((emb, i) => ({
      index: i,
      score: this.cosineSimilarity(queryEmbedding, emb),
    }))

    similarities.sort((a, b) => b.score - a.score)

    return similarities.slice(0, k).map(s => ({
      document: this.documents[s.index],
      score: s.score,
    }))
  }

  private cosineSimilarity(a: number[], b: number[]): number {
    let dot = 0, normA = 0, normB = 0
    for (let i = 0; i < a.length; i++) {
      dot += a[i] * b[i]
      normA += a[i] * a[i]
      normB += b[i] * b[i]
    }
    return dot / (Math.sqrt(normA) * Math.sqrt(normB))
  }
}
```

### 6.5 AI 推荐服务

```typescript
// src/services/ai/recommend.ts

export class AIRecommendService {
  constructor(
    private vectorStore: LocalVectorStore,
    private apiKey: string,
    private model: string = 'gpt-4o',
    private baseURL?: string,
  ) {}

  async recommend(query: string): Promise<AIResponse> {
    // 1. 生成查询的 embedding
    const queryEmbedding = await this.getEmbedding(query)

    // 2. 从向量库中检索相关影片
    const results = this.vectorStore.search(queryEmbedding, 10)

    // 3. 构建上下文
    const context = results.map((r, i) =>
      `${i + 1}. ${r.document.metadata.title} (${r.document.metadata.year}) - ${r.document.text}`
    ).join('\n\n')

    // 4. 调用 LLM
    const response = await fetch(`${this.baseURL || 'https://api.openai.com'}/v1/chat/completions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${this.apiKey}`,
      },
      body: JSON.stringify({
        model: this.model,
        messages: [
          {
            role: 'system',
            content: `你是 OhMyCine 影视推荐助手。用户有一个本地媒体库，以下是库中与查询相关的影片：

${context}

请根据用户的查询，从上述列表中推荐最合适的 2-3 部影片。回复格式：
1. 片名 (年份) - 推荐理由
2. ...

用中文回答，语气亲切自然。只推荐列表中有的影片，不要推荐用户库中没有的。`,
          },
          { role: 'user', content: query },
        ],
        temperature: 0.7,
      }),
    })

    const data = await response.json()
    const answer = data.choices[0].message.content

    return {
      answer,
      sources: results.slice(0, 3).map(r => ({
        title: r.document.metadata.title,
        year: r.document.metadata.year,
        posterUrl: r.document.metadata.posterPath,
        filePath: r.document.metadata.filePath,
      })),
    }
  }

  // 生成 embedding
  private async getEmbedding(text: string): Promise<number[]> {
    const response = await fetch(`${this.baseURL || 'https://api.openai.com'}/v1/embeddings`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${this.apiKey}`,
      },
      body: JSON.stringify({
        model: 'text-embedding-3-small',
        input: text,
      }),
    })

    const data = await response.json()
    return data.data[0].embedding
  }
}

export interface AIResponse {
  answer: string
  sources: Array<{
    title: string
    year?: number
    posterUrl?: string
    filePath: string
  }>
}
```

### 6.6 AI 页面 UI

```
┌────────────────────────────────────────────────────────────────┐
│ AI 推荐助手                                                    │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  🤖 你好！我是你的影视推荐助手。                         │  │
│  │  我会根据你媒体库中的影片为你推荐。                       │  │
│  │  你可以问我类似：                                        │  │
│  │  • "我想看一部烧脑的科幻片"                              │  │
│  │  • "推荐一部适合周末看的轻松喜剧"                        │  │
│  │  • "有没有类似盗梦空间的电影"                            │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  👤 我想看一部烧脑的科幻片                               │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  🤖 根据你的媒体库，我推荐以下几部：                     │  │
│  │                                                          │  │
│  │  1. **盗梦空间** (2010)                                  │  │
│  │     诺兰执导的梦境层层嵌套，剧情节奏紧凑，              │  │
│  │     结局的陀螺至今让人回味无穷。                        │  │
│  │     ┌────┐                                              │  │
│  │     │海报│  ← 点击直接播放                              │  │
│  │     └────┘                                              │  │
│  │                                                          │  │
│  │  2. **星际穿越** (2014)                                  │  │
│  │     硬科幻与亲情的完美结合，黑洞和五维空间的            │  │
│  │     视觉呈现令人震撼。                                  │  │
│  │                                                          │  │
│  │  3. **信条** (2020)                                      │  │
│  │     时间逆转的概念非常新颖，需要多看几遍才能            │  │
│  │     完全理解。                                          │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  输入你的问题...                           [发送]        │  │
│  └──────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────┘
```

### 6.7 AI 配置

```typescript
// src/stores/settings.ts (AI 部分)

export const useSettingsStore = defineStore('settings', () => {
  const ai = ref({
    provider: 'openai' as 'openai' | 'claude' | 'custom',
    apiKey: '',
    model: 'gpt-4o',
    baseURL: '',           // 自定义 API 地址 (如 OpenAI 代理)
    embeddingModel: 'text-embedding-3-small',
  })

  return { ai }
})
```

**支持的 AI 提供商**：

| 提供商 | 模型 | Embedding | 说明 |
|--------|------|-----------|------|
| OpenAI | gpt-4o / gpt-4o-mini | text-embedding-3-small | 默认推荐 |
| Claude | claude-sonnet-4-6 | - | 需要第三方 embedding |
| 自定义 | 任意 OpenAI 兼容 | 任意 | 本地 LLM (Ollama等) |

## 7. 设计系统 — Cinema OS

### 应用图标

OhMyCine 使用“影院之眼”作为应用主标识：深黑圆角银幕底、白色 O 形镜头/光圈结构，以及暖红色播放光束。标记不使用胶片条、爆米花或场记板等常见影视模板，确保在 Windows 任务栏、安装包和未来移动端桌面图标的 16–32px 小尺寸下仍有清晰轮廓。

透明母版位于 `src-tauri/icons/icon.png`，`32x32.png`、`128x128.png`、`128x128@2x.png`、`icon.ico` 和 `icon.icns` 均由 Tauri CLI 从该母版生成，不直接手工修改派生文件。后续 Android/iOS 工程建立时继续使用同一母版生成平台资源，避免不同平台出现多套品牌标记。

### 7.1 设计Token (CSS Variables)

```css
/* src/styles/variables.css */

:root {
  /* === 色彩系统 === */
  /* 主色调 - 星空蓝 */
  --color-primary: #4A9EFF;
  --color-primary-light: #7BB8FF;
  --color-primary-dark: #2D7AE0;
  --color-primary-glow: rgba(74, 158, 255, 0.3);

  /* 强调色 - 极光紫 */
  --color-accent: #A855F7;
  --color-accent-light: #C084FC;
  --color-accent-dark: #7C3AED;

  /* 中性色 */
  --color-bg-deep: #0A0A0F;         /* 最深背景 */
  --color-bg-base: #111118;          /* 基础背景 */
  --color-bg-elevated: #1A1A24;      /* 抬升背景 */
  --color-bg-surface: #22222E;       /* 表面 */
  --color-bg-overlay: rgba(10, 10, 15, 0.85);

  /* 文字 */
  --color-text-primary: #F0F0F5;
  --color-text-secondary: #A0A0B0;
  --color-text-tertiary: #606070;
  --color-text-inverse: #0A0A0F;

  /* 语义色 */
  --color-success: #22C55E;
  --color-warning: #F59E0B;
  --color-error: #EF4444;
  --color-info: #3B82F6;

  /* === 液态玻璃 === */
  --glass-bg: rgba(255, 255, 255, 0.05);
  --glass-bg-hover: rgba(255, 255, 255, 0.08);
  --glass-bg-active: rgba(255, 255, 255, 0.12);
  --glass-border: rgba(255, 255, 255, 0.08);
  --glass-border-hover: rgba(255, 255, 255, 0.15);
  --glass-blur: 20px;
  --glass-blur-heavy: 40px;
  --glass-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
  --glass-shadow-elevated: 0 16px 48px rgba(0, 0, 0, 0.4);

  /* === 圆角 === */
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 16px;
  --radius-xl: 24px;
  --radius-full: 9999px;

  /* === 间距 === */
  --space-xs: 4px;
  --space-sm: 8px;
  --space-md: 16px;
  --space-lg: 24px;
  --space-xl: 32px;
  --space-2xl: 48px;
  --space-3xl: 64px;

  /* === 字体 === */
  --font-sans: 'Inter', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  --font-display: 'Sora', 'Noto Sans SC', sans-serif;     /* 标题字体 */
  --font-mono: 'JetBrains Mono', 'Fira Code', monospace;

  /* 字体大小 */
  --text-xs: 12px;
  --text-sm: 14px;
  --text-base: 16px;
  --text-lg: 18px;
  --text-xl: 20px;
  --text-2xl: 24px;
  --text-3xl: 30px;
  --text-4xl: 36px;
  --text-5xl: 48px;

  /* === 动画 === */
  --ease-out: cubic-bezier(0.16, 1, 0.3, 1);
  --ease-in-out: cubic-bezier(0.65, 0, 0.35, 1);
  --ease-spring: cubic-bezier(0.34, 1.56, 0.64, 1);
  --duration-fast: 150ms;
  --duration-normal: 250ms;
  --duration-slow: 400ms;

  /* === 布局 === */
  --sidebar-width: 240px;
  --sidebar-width-collapsed: 64px;
  --topbar-height: 56px;
  --player-controls-height: 80px;
  --content-max-width: 1600px;
}
```

### 7.2 液态玻璃组件样式

```css
/* src/styles/glass.css */

/* 基础液态玻璃 */
.glass {
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  -webkit-backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-lg);
  box-shadow: var(--glass-shadow);
}

.glass-elevated {
  background: var(--glass-bg-hover);
  backdrop-filter: blur(var(--glass-blur-heavy));
  border: 1px solid var(--glass-border-hover);
  box-shadow: var(--glass-shadow-elevated);
}

/* 液态玻璃卡片 - 悬停光晕效果 */
.glass-card {
  position: relative;
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  transition: all var(--duration-normal) var(--ease-out);
}

.glass-card::before {
  content: '';
  position: absolute;
  inset: 0;
  background: radial-gradient(
    600px circle at var(--mouse-x, 50%) var(--mouse-y, 50%),
    rgba(255, 255, 255, 0.06),
    transparent 40%
  );
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-out);
  pointer-events: none;
}

.glass-card:hover::before {
  opacity: 1;
}

.glass-card:hover {
  border-color: var(--glass-border-hover);
  transform: translateY(-2px);
  box-shadow: var(--glass-shadow-elevated);
}

/* 动态数据源侧栏玻璃 */
.sidebar-glass {
  background: rgba(10, 10, 15, 0.7);
  backdrop-filter: blur(40px) saturate(1.8);
  border-right: 1px solid var(--glass-border);
}

/* 播放控制条玻璃 */
.player-controls-glass {
  background: linear-gradient(
    to top,
    rgba(0, 0, 0, 0.85),
    rgba(0, 0, 0, 0.4) 60%,
    transparent
  );
  backdrop-filter: blur(12px);
}
```

### 7.3 核心UI页面设计

#### 首页 (HomeView)

首页是 **全部已绑定数据源的聚合入口**，不是固定电影/剧集分类页。它从用户已配置的数据源中按顺序和可用元数据拉取内容，形成类似 Emby/Jellyfin 首页但更沉浸的 Cinema OS 体验。

```
┌────────────────────────────────────────────────────────────────────┐
│  动态数据源侧栏                                                     │
│  ┌──┐   ┌──────────────────────────────────────────────────────┐   │
│  │🏠│   │ Hero Carousel                                         │   │
│  │E │   │ 背景: backdrop / 海报图                               │   │
│  │12│   │ 左侧: 标题 Logo / 标题 / 简介 / 年份 / 类型 / 评分     │   │
│  │☁ │   │ 操作: 播放 / 收藏 / 详情                              │   │
│  │⚙ │   │ 右侧: 轮播切换按钮 + 底部页码指示                    │   │
│  └──┘   └──────────────────────────────────────────────────────┘   │
│                                                                    │
│        ┌──────────────────────┐ ┌──────────────────────────────┐   │
│        │ 继续观看              │ │ 最新影片                      │   │
│        │ 最近播放记录/进度条    │ │ 海报 + 片名横向列表            │   │
│        └──────────────────────┘ └──────────────────────────────┘   │
└────────────────────────────────────────────────────────────────────┘
```

**Hero Carousel 数据来源**：
- 优先使用媒体源已有元数据：标题 Logo（类似 Emby logo image）、backdrop、poster、overview、year、genres、rating、duration。
- Emby/Jellyfin 直接使用服务端已刮削数据。
- OpenList/Alist、CloudDrive2、夸克网盘、123 云盘、WebDAV、本地文件若缺元数据，使用 Player 本地刮削结果补齐。
- 轮播支持左右切换，并按固定间隔自动切换；用户手动切换后短暂暂停自动轮播。
- Hero 选择逻辑优先使用：继续观看中的高优先级项目、最近添加、评分较高项目；避免展示缺少 backdrop/overview 的裸文件。

**动态数据源侧栏**：
- 侧栏固定在首页和媒体库页面左侧，采用液态玻璃竖向导航。
- 顶部固定为“首页/聚合首页”。
- 其后按 `DataSourceConfig.order` 展示用户已绑定的数据源，例如 Emby、123 云盘、OpenList/Alist、CloudDrive2、WebDAV、本地文件。
- 数据源图标和名称来自绑定配置；未配置图标时按类型使用默认图标。
- 点击某个数据源进入 `SourceLibraryView`，只浏览该数据源下的媒体库。
- 设置入口固定在底部；Server 连接作为可选数据源或增强入口，不阻塞首页。

**首页内容区**：
- 左下角为“继续观看”，显示播放历史、进度条和下一集/继续播放入口；没有记录时使用低存在感空状态。
- 右下角为“最新影片/最近添加”，显示来自全部数据源的最新项目，使用海报 + 名称。
- 首页可以后续扩展推荐、收藏、高分佳片等区块，但 MVP 优先实现 Hero、继续观看、最新影片三块，避免首页复杂度过高。

#### 单数据源媒体库页 (SourceLibraryView)

点击动态数据源侧栏的某个来源后进入该数据源自己的媒体库首页。布局参考 Emby/Jellyfin：

- 顶部保留与首页一致风格的 Hero Carousel，但数据只来自当前数据源。
- Hero 下方显示该数据源的媒体库分组，如电影、剧集、动画、文件夹等。
- 每个媒体库使用海报墙/横向行展示，支持进入库详情、筛选、搜索和播放。
- 云盘类数据源可以同时展示“媒体库视图”和“文件夹视图”，但默认优先展示已刮削/已识别的媒体内容。
- 不同数据源的页面结构统一，具体能力由 DataSource 接口返回值决定。
- 聚合首页和单数据源根页同时使用进程内会话快照与 `settings.sqlite` 安全展示快照：首次成功加载后，返回页面立即显示最近一次内容，5 分钟内不重复请求；Android Activity/进程重建后先恢复持久化展示内容，再在后台刷新。持久化快照清空媒体 `path`，丢弃带 token、key、auth、signature、sig、expires/exp 等敏感查询参数的图片 URL，不保存凭据、认证 Header、原始播放 URL、签名 URL或本地绝对路径。海报和背景由稳定的来源/媒体身份从 `cache/images` 单独恢复，因此不需要把敏感图片 URL 写入 SQLite。修改/删除数据源、手动清理缓存和明确播放进度刷新会精确失效对应快照。
- 应用内容实际滚动容器是 `AppLayout` 的主 `<main>`。路由切换以及媒体库内部的分类、文件夹、搜索和返回根页操作都必须复位该容器到顶部，不能只调用对当前布局无效的 `window.scrollTo()`。

#### 播放器页面 (PlayerView)

PlayerView 是播放 URL 与 header 的唯一解析边界：路由先提供媒体身份，PlayerView 再从内存播放上下文恢复本地路径或调用对应 DataSource 获取瞬时 `MediaStreamRequest`，随后直接交给 mpv。队列切换同样只更新媒体身份，不把已解析 URL 回写路由。

```
┌────────────────────────────────────────────────────────────────┐
│                                                                │
│                                                                │
│                                                                │
│                    ┌──────────────────────┐                    │
│                    │                      │                    │
│                    │     Video Output     │                    │
│                    │     (MPV Render)     │                    │
│                    │                      │                    │
│                    │                      │                    │
│                    └──────────────────────┘                    │
│                                                                │
│                                                                │
├────────────────────────────────────────────────────────────────┤
│ ← Inception (2010) ── 1080p BluRay H265 DTS-HD               │
│                                                                │
│  00:42:15 ━━━━━━━━━━━━━━━●━━━━━━━━━━━━━━━━━━━━━━ 02:28:00    │ <- 进度条
│                                                                │
│  ⏮  ⏪10s  ▶/⏸  ⏩10s  ⏭     🔊━━━━━●━━  🗨️  📺  ⚙️        │ <- 控制条
│                                                                │
│  字幕: 中文(默认) │ 音轨: DTS-HD 5.1 │ 速度: 1.0x             │
└────────────────────────────────────────────────────────────────┘
```

**播放器控制条功能**：
- 进度条：精确拖拽，预览缩略图（hover时）
- 播放/暂停：Space键
- 快进/快退：← → 方向键（10s），Ctrl+← →（60s）
- 音量：↑ ↓ 方向键，鼠标滚轮
- 字幕切换：S键
- 音轨切换：A键
- 画中画：P键
- 全屏：F键
- HDR/DV信息面板：I键
- 缓冲提示：libmpv 连续报告 `paused-for-cache` 约 500ms 后，在画面中央显示“正在缓冲”和 `cache-speed` 实时速率；普通暂停不得被误判为缓冲，恢复播放后立即隐藏。

#### 电影库 (MoviesView)

```
┌────────────────────────────────────────────────────────────────┐
│ Movies                                    Sort: Year ▼  Filter │
├────────┬───────────────────────────────────────────────────────┤
│        │                                                       │
│ Genres │  ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐         │
│ ────── │  │    │ │    │ │    │ │    │ │    │ │    │          │
│ All    │  │    │ │    │ │    │ │    │ │    │ │    │          │
│ Action │  └────┘ └────┘ └────┘ └────┘ └────┘ └────┘          │
│ Comedy │  ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐         │
│ Drama  │  │    │ │    │ │    │ │    │ │    │ │    │          │
│ Sci-Fi │  │    │ │    │ │    │ │    │ │    │ │    │          │
│ Horror │  └────┘ └────┘ └────┘ └────┘ └────┘ └────┘          │
│ ...    │  ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐         │
│        │  │    │ │    │ │    │ │    │ │    │ │    │          │
│ Years  │  │    │ │    │ │    │ │    │ │    │ │    │          │
│ ────── │  └────┘ └────┘ └────┘ └────┘ └────┘ └────┘          │
│ 2024   │                                                       │
│ 2023   │                                                       │
│ 2022   │                                                       │
│ ...    │                                                       │
├────────┴───────────────────────────────────────────────────────┤
│ Page 1 of 24  |  Showing 1-36 of 856                          │
└────────────────────────────────────────────────────────────────┘
```

## 8. 核心模块设计

### 8.1 MPV集成 (Tauri Commands)

```rust
// src-tauri/src/commands/player.rs

use tauri::command;

#[command]
pub async fn mpv_load(path: String, state: State<'_, MpvState>) -> Result<(), String> {
    let mpv = state.lock().map_err(|e| e.to_string())?;
    mpv.command("loadfile", &[&path]).map_err(|e| e.to_string())
}

#[command]
pub async fn mpv_pause(state: State<'_, MpvState>) -> Result<(), String> {
    let mpv = state.lock().map_err(|e| e.to_string())?;
    mpv.set_property("pause", true).map_err(|e| e.to_string())
}

#[command]
pub async fn mpv_seek(position: f64, state: State<'_, MpvState>) -> Result<(), String> {
    let mpv = state.lock().map_err(|e| e.to_string())?;
    mpv.command("seek", &[&position.to_string(), "absolute"]).map_err(|e| e.to_string())
}

#[command]
pub async fn mpv_get_property(prop: String, state: State<'_, MpvState>) -> Result<String, String> {
    let mpv = state.lock().map_err(|e| e.to_string())?;
    mpv.get_property(&prop).map_err(|e| e.to_string())
}

#[command]
pub async fn mpv_set_property(prop: String, value: String, state: State<'_, MpvState>) -> Result<(), String> {
    let mpv = state.lock().map_err(|e| e.to_string())?;
    mpv.set_property(&prop, &value).map_err(|e| e.to_string())
}
```

### 8.2 Vue MPV Composable

```typescript
// src/composables/useMpv.ts

import { ref, onUnmounted } from 'vue'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'

export function useMpv() {
  const isPlaying = ref(false)
  const currentTime = ref(0)
  const duration = ref(0)
  const volume = ref(100)
  const isMuted = ref(false)
  const subtitleTracks = ref<Track[]>([])
  const audioTracks = ref<Track[]>([])
  const currentSubtitle = ref<number>(0)
  const currentAudio = ref<number>(0)

  // 监听MPV事件
  const unlistenTime = listen<{ time: number }>('mpv:time-update', (e) => {
    currentTime.value = e.payload.time
  })

  const unlistenDuration = listen<{ duration: number }>('mpv:duration-change', (e) => {
    duration.value = e.payload.duration
  })

  const unlistenPause = listen('mpv:paused', () => {
    isPlaying.value = false
  })

  const unlistenResume = listen('mpv:resumed', () => {
    isPlaying.value = true
  })

  async function load(path: string) {
    await invoke('mpv_load', { path })
  }

  async function togglePause() {
    await invoke(isPlaying.value ? 'mpv_pause' : 'mpv_resume')
  }

  async function seek(position: number) {
    await invoke('mpv_seek', { position })
  }

  async function setVolume(vol: number) {
    await invoke('mpv_set_property', { prop: 'volume', value: vol.toString() })
    volume.value = vol
  }

  async function setSubtitle(index: number) {
    await invoke('mpv_set_property', { prop: 'sid', value: index.toString() })
    currentSubtitle.value = index
  }

  async function setAudio(index: number) {
    await invoke('mpv_set_property', { prop: 'aid', value: index.toString() })
    currentAudio.value = index
  }

  onUnmounted(() => {
    unlistenTime.then(fn => fn())
    unlistenDuration.then(fn => fn())
    unlistenPause.then(fn => fn())
    unlistenResume.then(fn => fn())
  })

  return {
    isPlaying, currentTime, duration, volume, isMuted,
    subtitleTracks, audioTracks, currentSubtitle, currentAudio,
    load, togglePause, seek, setVolume, setSubtitle, setAudio
  }
}
```

### 8.3 Server连接 Composable

```typescript
// src/composables/useServer.ts

import { ref, computed } from 'vue'
import { useFetch } from '../utils/fetch'

export function useServer() {
  const serverUrl = ref('')
  const apiKey = ref('')
  const isConnected = ref(false)

  const api = computed(() => useFetch(serverUrl.value, apiKey.value))

  async function connect(url: string, key?: string) {
    serverUrl.value = url
    apiKey.value = key || ''
    try {
      const res = await api.value('/api/v1/health')
      isConnected.value = true
      return res
    } catch {
      isConnected.value = false
      throw new Error('Failed to connect to server')
    }
  }

  async function getLibrary(type?: 'movie' | 'series') {
    return api.value('/api/v1/media', { params: { type } })
  }

  async function searchMedia(keyword: string) {
    return api.value('/api/v1/discovery/search', { method: 'POST', body: { keyword } })
  }

  async function getMediaDetail(id: string) {
    return api.value(`/api/v1/media/${id}`)
  }

  async function getCloudDrives() {
    return api.value('/api/v1/connections')
  }

  async function getDownloads() {
    return api.value('/api/v1/downloads')
  }

  return {
    serverUrl, isConnected,
    connect, getLibrary, searchMedia, getMediaDetail,
    getCloudDrives, getDownloads
  }
}
```

## 9. MPV 集成策略 (libmpv 嵌入方案)

### 9.1 架构选择：libmpv 嵌入 vs Sidecar

**放弃 Sidecar，采用 libmpv 嵌入方案**。原因：

```
Sidecar (独立进程)                    libmpv (嵌入库)
┌──────────────┐  ┌──────────────┐    ┌──────────────────────────┐
│  Tauri App   │  │  MPV Window  │    │  Tauri App               │
│  (UI only)   │  │  (独立窗口)  │    │  ┌────────────────────┐  │
│              │  │              │    │  │  libmpv 渲染区域    │  │
│  两个窗口    │  │  沉浸感差    │    │  │  (嵌入在WebView内)  │  │
└──────────────┘  └──────────────┘    │  └────────────────────┘  │
                                      │  UI覆盖在视频之上         │
                                      │  一个窗口，沉浸感极佳     │
                                      └──────────────────────────┘
```

### 9.2 libmpv 嵌入方案详解

libmpv 是 MPV 的 C API 库版本，可以直接编译链接到 Rust 项目中：

```
┌─────────────────────────────────────────────────────────────────┐
│  Tauri Window (无边框，全屏可选)                                  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  WebView (Vue UI 层)                                      │  │
│  │                                                           │  │
│  │  ┌─────────────────────────────────────────────────────┐  │  │
│  │  │  透明区域 (pointer-events: none)                     │  │  │
│  │  │  Vue 控制条/字幕/OSD 悬浮在视频上方                  │  │  │
│  │  │                                                     │  │  │
│  │  │  ┌───────────────────────────────────────────────┐  │  │  │
│  │  │  │                                               │  │  │  │
│  │  │  │       libmpv 原生渲染 (OpenGL/Vulkan/Metal)   │  │  │  │
│  │  │  │       视频画面直接绘制在窗口底层               │  │  │  │
│  │  │  │                                               │  │  │  │
│  │  │  └───────────────────────────────────────────────┘  │  │  │
│  │  └─────────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

**技术实现路径**：

```
Rust 侧                              Vue 侧
┌──────────────────────┐              ┌──────────────────────┐
│  libmpv-sys (FFI)    │              │  useMpv() composable │
│  ↓                   │              │  ↑                   │
│  libmpv C API 调用   │   Tauri      │  invoke() / listen() │
│  ↓                   │◄──Events────►│  ↑                   │
│  mpv_render_context  │              │  视频控制UI           │
│  ↓                   │              │  字幕叠加             │
│  OpenGL/Vulkan 渲染  │              │  OSD信息              │
└──────────────────────┘              └──────────────────────┘
```

### 9.3 Rust 侧：libmpv 绑定

```rust
// src-tauri/src/mpv/mod.rs

use libmpv::*;

pub struct MpvPlayer {
    ctx: Mpv,
    render_ctx: Option<MpvRenderContext>,
}

impl MpvPlayer {
    pub fn new() -> Result<Self, MpvError> {
        let ctx = Mpv::new()?;
        
        // 配置MPV
        ctx.set_property("vo", "gpu")?;           // GPU渲染
        ctx.set_property("gpu-context", "auto")?;  // 自动选择后端
        ctx.set_property("hwdec", "auto")?;        // 硬件解码
        ctx.set_property("keep-open", "yes")?;     // 播放完不退出
        ctx.set_property("osc", "no")?;            // 禁用内置OSC(用自定义UI)
        
        // HDR / Dolby Vision
        ctx.set_property("tone-mapping", "bt.2446a")?;
        ctx.set_property("allow-delayed-peak-detect", "yes")?;
        
        Ok(Self { ctx, render_ctx: None })
    }
    
    /// 初始化渲染上下文，绑定到窗口
    pub fn init_render(&mut self, window: &Window) -> Result<(), MpvError> {
        let render_ctx = MpvRenderContext::new(
            self.ctx.handle(),
            MpvRenderParam::ApiType(MpvRenderApiType::OpenGL),
            MpvRenderParam::AdvancedControl(true),
            // ... 窗口绑定参数
        )?;
        self.render_ctx = Some(render_ctx);
        Ok(())
    }
    
    pub fn load_file(&self, path: &str) -> Result<(), MpvError> {
        self.ctx.command("loadfile", &[path])?;
        Ok(())
    }
    
    pub fn seek(&self, position: f64) -> Result<(), MpvError> {
        self.ctx.command("seek", &[&position.to_string(), "absolute"])?;
        Ok(())
    }
    
    pub fn get_property(&self, prop: &str) -> Result<String, MpvError> {
        self.ctx.get_property_string(prop)
    }
    
    pub fn set_property(&self, prop: &str, value: &str) -> Result<(), MpvError> {
        self.ctx.set_property(prop, value)?;
        Ok(())
    }
    
    /// 注册事件回调，通过Tauri Event转发给Vue前端
    pub fn register_events(&self, app_handle: AppHandle) {
        // 播放时间更新
        self.ctx.observe_property("time-pos", move |val: f64| {
            app_handle.emit("mpv:time-update", val).ok();
        });
        
        // 时长变化
        self.ctx.observe_property("duration", move |val: f64| {
            app_handle.emit("mpv:duration-change", val).ok();
        });
        
        // 暂停/恢复
        self.ctx.observe_property("pause", move |val: bool| {
            if val {
                app_handle.emit("mpv:paused", ()).ok();
            } else {
                app_handle.emit("mpv:resumed", ()).ok();
            }
        });
    }
}
```

### 9.4 平台渲染后端

下表描述目标平台方案。Windows MVP 渲染路径已完成宿主实机验证；Android ARM64 已完成 `SurfaceView` + 官方 mpv-android runtime 的构建、APK 包内契约和首轮真机诊断，远程 HTTPS 播放仍需验证 Rust TLS 回环桥接后的结果。macOS、Linux 渲染后端仍未完成，不作为当前 CI/release 阻塞项。

| 平台 | 渲染后端 | 窗口嵌入方式 |
|------|----------|-------------|
| Windows | ANGLE (OpenGL ES → D3D11) | `HWND` 子窗口 |
| macOS | Metal (via ANGLE) | `NSView` 子视图 |
| Linux | Vulkan / OpenGL | `X11 Window` / `Wayland Surface` |
| Android | OpenGL ES + `gpu-next` | 原生 `SurfaceView` 位于透明 Tauri WebView 下方 |

### 9.5 Tauri Plugin 封装

将 libmpv 封装为 Tauri Plugin，Vue 侧通过标准 Tauri API 调用：

```rust
// src-tauri/src/plugins/mpv_plugin.rs

pub struct MpvPlugin;

impl Plugin for MpvPlugin {
    fn name(&self) -> &str { "mpv" }
    
    fn initialize(&mut self, app: &AppHandle, _config: serde_json::Value) -> Result<(), Box<dyn Error>> {
        let player = MpvPlayer::new()?;
        // 绑定到主窗口
        player.init_render(app.get_window("main").unwrap())?;
        app.manage(MpvState::new(player));
        Ok(())
    }
    
    fn extend_api(&mut self, invoke: Invoke) {
        match invoke.command() {
            "mpv_load" => load_file(invoke),
            "mpv_pause" => pause(invoke),
            "mpv_resume" => resume(invoke),
            "mpv_seek" => seek(invoke),
            "mpv_get_property" => get_property(invoke),
            "mpv_set_property" => set_property(invoke),
            _ => {}
        }
    }
}
```

### 9.6 Cargo 依赖

```toml
# src-tauri/Cargo.toml

[dependencies]
libmpv = "2.0"          # libmpv Rust 绑定
libmpv-sys = "3.1"      # libmpv C FFI 绑定

[build-dependencies]
# 编译时链接libmpv
```

### 9.7 构建时libmpv处理

桌面打包配置当前声明 Windows 运行期资源，配合 `x86_64-pc-windows-gnu` 构建。Android ARM64 预览构建通过独立脚本准备官方 mpv-android runtime；Linux/macOS runtime resources 和 Tauri 平台配置等对应渲染与打包链路完成后再接入。

```
src-tauri/
  lib/
    libmpv-2.dll          # Windows libmpv 运行期动态库
    libmpv-wrapper.dll    # Windows wrapper 运行期动态库
    libmpv.dll.a          # Windows GNU 链接用 import library，不打包为运行期资源
    LICENSE               # 第三方许可文本
```

Windows Player CI、manual build 和 beta release 使用 Windows libmpv 资源。每个 Windows Beta 发布三个程序包：NSIS 安装包、没有 `portable.flag` 且使用 LocalAppData 的标准免安装 ZIP、带 `portable.flag` 且使用 EXE 同目录独立数据的便携 ZIP；两种 ZIP 都只收集必需运行文件和许可证，不包含 target 构建中间产物。Windows 本地同时作为 Windows MSVC 与 Android ARM64 的权威构建环境；Android 不依赖 WSL，而是由跨平台 Node 入口发现 Windows 原生 Android SDK、NDK `27.2.12479018` 和 JDK 17。Android 预览通过 `npm run tauri:build:android:preview` 构建 ARM64 debug APK；脚本先删除旧 APK，再固定下载并校验 mpv-android `2026-04-25` 的 SHA-256，提取 `libmpv.so`、FFmpeg、JNI bridge、`libc++_shared.so` 与 CA 证书，并直接进入 `tauri android build`。不得在 Tauri 命令前单独调用 `gradlew clean`，因为 GitHub 干净 Runner 中被 `.gitignore` 排除的 `tauri.settings.gradle`、`tauri.build.gradle.kts` 和运行配置需要由 Tauri build 内部先生成。运行库和 Tauri 平台桥接生成物不提交到 Git。Player tag push 固定为 Beta，必须精确指向最新远端 `develop` 提交；手动 Beta 同样只允许从该提交发布。Stable 只在确认正式版、将 `develop` 合入并推送 `main` 后，从最新远端 `main` 提交显式手动发布。Windows release job 先发布安装包、标准 ZIP、便携 ZIP 和 updater 清单，Android job 再使用 GitHub Secrets 中的固定 preview keystore 构建可连续覆盖升级的 APK，并向同一 Release 追加 ARM64 APK 与 SHA-256。正式商店签名以及 macOS/Linux 资源链路后续接入。

Windows 无边框标题栏的空白拖动面在左键按下时立即进入原生窗口移动循环，不等待前端移动阈值，也不预先异步查询或还原最大化状态；窗口恢复拖动、跨屏移动与 Snap 全部交给 Windows，导航和窗口按钮则保持在拖动面上方并继续独立命中。透明 WebView 叠层与 mpv HWND 底层窗口按事件类型使用两条几何时序。纯移动不会改变 surface 在客户区内的矩形，因此原生 owner `Moved` 事件直接复用已确认的物理 surface bounds，并结合 owner 当前客户区屏幕原点同步 `SetWindowPos`，无需等待通常不会触发的 `ResizeObserver`；该即时路径不得从 Win32 客户区重新推导视频尺寸。窗口缩放和 DPI 变化仍由 WebView 在下一布局帧读取实际 surface rect，再把逻辑坐标传给 Rust 转换为物理像素；原生 owner `Resized` / `ScaleFactorChanged` 事件只同步必要的可见性和层级，不得抢先拉伸视频，否则视频画面会领先控制遮罩层缩放。窗口最大化/还原与 Player 全屏也是独立状态；从最大化进入全屏时临时还原窗口，退出全屏后恢复最大化。普通窗口的 WebView 根与 mpv 底层 HWND 使用相同 12px 圆角，最大化/全屏同时取消圆角；全屏底层窗口向四边额外覆盖 1 个物理像素，避免透明边缘漏出桌面。

桌面 mpv 事件轮询每 250ms 最多消费 64 个事件，不能在持有全局播放器锁时无限清空事件队列。耳机/音频设备热插拔可能产生密集音频重配置事件，剩余事件必须留到下一 tick，确保暂停、轨道、字幕和窗口命令仍能及时获得锁。播放页进入时保持海报/背景的高斯模糊占位；只有收到桌面 `VIDEO_RECONFIG` 或 Android `fileLoaded + videoFormat/playing` 后，才把 WebView 根链切成透明并显示原生视频层。

### 9.8 Android 策略

Android 使用相同命令协议的移动端实现：
1. **libmpv Android runtime** — 当前 ARM64 预览固定使用官方 [mpv-android](https://github.com/mpv-android/mpv-android) `2026-04-25` release；后续发布流水线可切换为可复现的自建 `.so`
2. Rust 注册 `com.ohmycine.player.mpv.MpvPlugin`，现有 Vue `mpv_*` 命令通过 Tauri mobile plugin 转发到 Kotlin/JNI
3. Kotlin 创建原生 `SurfaceView`，调用 `MPVLib.attachSurface`，以 `gpu-context=android`、`vo=gpu-next` 和 `mediacodec` 硬解候选渲染
4. Tauri WebView 透明叠放在视频层上方，继续复用现有播放控制、字幕/音轨、手势、快捷键和播放偏好 UI
5. Rust 每 250ms 读取原生 snapshot，并继续发出 `mpv:time-update`、`mpv:duration-change`、`mpv:paused`、`mpv:resumed`

Player Web UI 已进入独立的手机交互设计阶段，不再把桌面外壳简单压缩到窄屏。宽度不超过 `767px` 或粗指针环境使用固定的 `首页 / 媒体库 / 快捷 / 设置` 底部导航；媒体库选择和全局快捷操作通过底部抽屉展开，承接桌面数据源 hover 侧栏、打开本地视频、添加/管理数据源和主题切换。手机首页改为全宽英雄区与横向内容流，海报播放入口常驻；设置总览改为紧凑列表；媒体源扫描/文件入口在手机上常驻于底部导航上方，不依赖 hover。

手机播放器使用独立触摸覆盖层：顶部只保留返回、标题和必要工具，字幕只在右上工具区保留一个入口；画面中央常驻三个尺寸克制的主播放操作，底部使用一条低高度时间轴与工具坞，不重复放置字幕入口。轨道、倍速、队列和画面设置在横屏中使用受限宽度的右侧面板，在竖屏中使用底部面板；进度条使用 Pointer Events 和 pointer capture，同一实现兼容鼠标与触摸拖动。移动控制台继续使用 OhMyCine 液态玻璃语言，但 Android `SurfaceView` 与 WebView 分层时不能只依赖 `backdrop-filter`，还要用半透明中性色、内高光、细边缘和层叠阴影维持玻璃层次。桌面和 Android 播放画面本身始终保持透明、沉浸和不染色，顶部/底部渐变、控制条、传输按钮、菜单、抽屉、缓冲提示与画面设置面板则必须通过 Player 专用语义 token 跟随全局亮/暗主题，不能把播放页继续作为固定深色主题例外。普通浏览器预览不得因无 Tauri 窗口对象而中断 Vue 挂载，以便固定使用横屏/竖屏设备尺寸进行响应式截图回归。

播放画面手势按输入类型启用，而不是按平台或窗口宽度启用：只有 `PointerEvent.pointerType === 'touch'` 会进入手势状态机，因此 Android、手机浏览器和 Surface 等触控 PC 在桌面宽度下都可使用；鼠标与触控笔继续沿用桌面点击、悬停显隐和快捷键逻辑。触摸横向滑动预览并提交快退/快进，右半屏纵向滑动调整音量，单击切换控制 UI，双击切换播放/暂停。左半屏纵向滑动在 Android 和 Windows 都调整实际显示亮度：Android 通过 Activity `WindowManager.LayoutParams.screenBrightness` 调整当前播放窗口并在退出时恢复；Windows 根据播放器 HWND 选择所在显示器，外接显示器优先使用 DXVA2 DDC/CI，Surface/笔记本内屏回退到 `ROOT\\WMI`。Windows 的 Alt 模拟同样走显示亮度；显示器不支持时右上角明确提示，不得退回 mpv 亮度制造语义混淆。mpv 视频亮度作为独立的单视频画面设置保留在播放控制的画面面板中，并按媒体保存，不与设备屏幕亮度混用。手势开始于按钮、菜单、输入框或底部控制栏时不得抢占控件操作，顶部/底部系统手势保护区也不接管指针；真实触摸使用更高位移阈值和轴向优势判断，避免下拉通知栏或斜向滚动误调音量/亮度。触摸产生的合成 click 不得再次触发鼠标单击暂停。

没有触摸硬件时，按住 `Alt` 再使用播放画面的鼠标主键，可把当前操作映射到同一手势状态机。Alt 模拟只接管无控件遮挡的播放画面，并在实际操作期间暂停鼠标移动自动唤出控制 UI，以便验证触摸单击显隐；控制栏、菜单、输入框、链接和右键菜单始终使用桌面鼠标逻辑。松开并结束当前手势后立即恢复普通鼠标行为，不额外显示测试提示。

触摸或 Alt 模拟在左右半屏静止按住时，复用方向键长按状态机：左半屏等价于长按左方向键并连续后退，右半屏等价于长按右方向键并临时使用“方向键长按倍速”全局设置，松手后恢复当前媒体原倍速。手势移动超过轴向阈值前只是长按候选；超过阈值即取消候选并进入横向 seek 或左右纵向亮度/音量手势；一旦长按已经激活，本次指针会锁定为长按直到松手。键盘与触摸必须记录独立输入所有者，避免其中一方错误释放另一方的长按状态。

Android 响应式布局按实时可用窗口宽度划分，不依赖手机/平板型号判断：`compact` 用于手机竖屏和窄分屏，`medium` 用于平板分屏与小尺寸平板，`expanded` 用于平板全屏、桌面模式和大窗口。CSS media/container queries 负责内容重排，窗口尺寸变化必须即时生效；触摸/鼠标/键盘能力继续由 Pointer Events 与输入媒体查询独立判断。Android Activity 必须允许 resize，不全局锁定方向，并处理安全区、系统栏、横竖屏、多窗口和折叠屏窗口变化。播放器可由用户进入横屏或全屏，但返回浏览界面后仍恢复当前多窗口尺寸。

手机剧集详情的选集区域不复用桌面两侧渐隐和外层留白。全局设置可选择默认“横向卡片”或“竖向列表”：横向模式渲染当前季完整分集并使用原生惯性横滑、接近满宽的吸附卡片；竖向模式使用紧凑全宽列表和普通页面纵向滚动。手机隐藏桌面专用的底部快速定位滑条，避免与页面/系统手势冲突；桌面继续使用窗口化横向轨道、箭头、键盘和定位滑条，播放中的队列面板不受该设置影响。

当前已生成 Tauri Android Studio 工程，并可通过 `npm run tauri:build:android:preview` 构建 ARM64 debug APK。Activity 显式启用 `resizeableActivity` 且不全局锁定方向；开始播放时原生插件进入 sensor-landscape 沉浸模式、隐藏系统栏并保持亮屏，停止播放后恢复系统栏和默认方向策略。Android 不链接桌面 `libmpv-sys`，而是保持同名 `mpv_*` 命令并通过 Rust mobile plugin、Kotlin 与 mpv-android JNI runtime 完成加载、暂停、seek、属性、字幕和轨道操作；播放进度与暂停状态继续进入现有 Vue 事件流。构建命令会先清理旧 APK，并关闭 Rust debug info，避免预览包被调试符号膨胀。

Android 后台播放由全局开关控制。开启时 Kotlin 启动 `foregroundServiceType=mediaPlayback` 的非粘性前台服务，并通过 `MediaSessionCompat` 向系统通知/锁屏发布标题、时长、进度、播放/暂停、前后 10 秒和 seek；关闭时 Activity 进入后台立即暂停，且不保留播放服务。播放停止、离开播放页、自然结束、加载错误或插件销毁都会停止服务并移除通知。通知只包含安全展示元数据，不携带播放 URL、认证 Header 或 provider Token。

Android 本地媒体访问使用 Storage Access Framework，与桌面路径逻辑明确分离。快捷操作中的本地视频入口调用 `ACTION_OPEN_DOCUMENT`，所选 `content://` URI 只进入短生命周期 `PlaybackMediaContext`，路由仅保存 `contextId` 和媒体身份；Kotlin 播放桥通过 `ContentResolver.openFileDescriptor` 打开文件，并以 `fdclose://<fd>` 把描述符所有权交给 libmpv，不把大视频复制到缓存。设置页本地文件夹入口调用 `ACTION_OPEN_DOCUMENT_TREE` 并保存只读持久授权；`LocalMediaPlugin` 在授权树内查询子文档，继续向 DataSource 暴露 `/目录/文件` 逻辑路径，原始子文档 URI 不进入前端列表、扫描缓存或日志。授权被用户撤销时必须要求重新选择目录。Android 文档树无法复用桌面 `notify` watcher，因此 watcher 启动会安全降级，仍由现有短间隔增量扫描完成变化检测。桌面继续使用 Tauri dialog、绝对根路径和 Rust root-scoped 文件命令，不改变既有行为。

Android Surface 初始化是整组播放命令的前置屏障。Kotlin 将 `SurfaceView + 透明 WebView` 容器插回 Tauri 原 WebView 父节点，不替换 Activity 根内容；Rust 在调用 load 前等待 surface ready，并把初始化错误或超时返回前端。前端对 `initializing` 状态持续轮询，避免只在固定两秒窗口内判断一次。这样 load 后紧接的 resume、倍速、画面和字幕偏移命令不会因 Surface 尚未 attach 而失败，也不会把播放目标清空后错误显示成空播放器。

跨设备继续观看的 resume seek 不能只依赖起播后的固定短延时。媒体完成 load、但本机或路由续播位置尚未解析前，暂停所有历史与 provider 进度写入，避免起播事件抢先向 Emby 上报 `0`。Android 302/远程流可能在数秒后才进入 `video-ready`，Player 必须把目标位置保持为 pending，并在视频 ready 或 duration 可用时再次 seek；观察到当前时间抵达目标后才完成恢复。pending 期间写入本机历史和 Emby Sessions 的位置使用目标值，禁止用启动期临时 `0` 覆盖服务器上的有效进度；用户手动 seek 时立即取消旧恢复任务。

Emby/Jellyfin 等 provider 返回有效续播位置时，以 provider 位置作为跨设备权威值，本机 SQLite 仅在 provider 没有有效位置时兜底。首页聚合、剧集选择、详情页按钮与播放器起播使用同一优先级，避免手机或电脑的旧本地缓存反向覆盖较新的云端进度；本机记录仍保留明确来源标识，不能伪装成服务器记录。

Android 远程媒体的续播 seek 不能以前端乐观时间作为成功依据。桌面端保留现有正常的 optimistic seek 时序；只有 Android 自动续播调用关闭 optimistic 更新，`PlayerView` 在 `videoReady` 且原生 `time-pos` 落在目标前后 5 秒内之前持续保留待恢复位置。慢加载期间本机历史与 Emby session 上报均使用该待恢复位置，避免 `FILE_LOADED` 把早期 seek 重置为 0 后覆盖跨设备云端记录。用户主动拖动或手势 seek 会取消待恢复状态并继续使用交互式即时反馈。

Android 真机可能使用与 Rust HTTP 客户端不同的 libmpv/FFmpeg TLS 栈。所有初始 HTTP/HTTPS 媒体请求都由 Rust reqwest/rustls 拉取，再通过仅监听 `127.0.0.1` 随机端口的 axum 回环桥交给 libmpv；这也覆盖 Emby 先返回 HTTP 播放入口、再 302 到 HTTPS CDN 的情况，不能等初始 URL 已经是 HTTPS 才启用桥接。每个播放会话可注册最多 8 个独立的 24 字节 URL-safe 随机 token，使 Bilibili 等 DASH 视频轨与音频轨同时保留自己的 URL/Header；切换媒体、停止、加载失败都会回收整个会话，未知或过期 token 被拒绝。token 仍采用恒定时间比较。桥接保留 GET/HEAD、Range、If-Range、ETag、Last-Modified 和 Content-Range 等流媒体语义，手动限制跳转次数，并在跨 origin 302 前清除 Emby 等提供器私有 Header。原始直链、签名查询与认证 Header 不进入 URL、持久化、普通日志或播放诊断，且不得关闭 TLS 证书校验。

Android 原生播放页显式启用触摸优先控制布局，不以 `820px` 等竖屏断点作为唯一判断，因为手机横屏宽度经常超过该值。透明 WebView 中只要存在当前媒体，就必须保留覆盖整个视频面的独立触摸捕获层；该层不能因播放诊断进入 `error` 而移除。单击画面切换控制 UI，并对 Android 在短点击后产生的 `pointercancel` 保留受限兜底，不能让 SurfaceView 层吞掉控制唤出；播放加载失败或自然结束时前端必须同步退出“正在播放”状态，避免错误页继续自动隐藏控制 UI。移动控制栏提供独立方向锁图标，打开后可选择“自动横屏 / 锁定横屏 / 锁定竖屏”，锁图标反映自动或锁定状态，切换结果通过右上角短提示反馈；该状态不复用桌面全屏按钮。首页“继续观看”和“最新影片”横向媒体条只处理横向滚动，不得用纵向 overscroll containment 阻断页面上下滑动。空播放面统一只显示“等待播放中”，不展示桌面拖拽文件说明。

桌面和 Android 使用同一缓冲状态协议。Vue 在媒体加载后短间隔读取 `paused-for-cache` 与 `cache-speed`；Android Kotlin 属性桥必须分别以 Boolean 和 Double 读取这两个属性。缓冲提示独立于播放控制 chrome，即使控制栏已自动隐藏也能显示；移动端显示缓冲提示时暂时淡出中央三键，避免两个中央浮层重叠。

全局“播放与字幕”设置包含受控播放器引擎参数：视频输出仅允许 `gpu-next`（默认）或 `gpu`；解码策略仅允许自动安全、硬件优先或纯软件；缓存允许自动、开启、关闭和 64/128/256/512 MB 上限；同步允许以音频为准、显示重采样或显示丢帧。设置写入 Player SQLite app settings，Vue 在原生渲染初始化前和每次媒体加载前通过 `mpv_apply_engine_settings` 下发。Windows 映射到 libmpv `vo/hwdec/cache/demuxer-max-bytes/video-sync`，Android 映射到 `gpu-next/gpu`、MediaCodec/软件解码及相同缓存同步参数。不得从 UI 接受任意 mpv 参数名或自定义原始字符串。

视频插帧采用独立于单帧 FSR 的跨平台受控协议。设置只允许 `off | auto`、目标 `auto | 48 | 60 | 120` 和质量 `auto | quality | balanced | performance`，旧设置升级后固定为关闭。进入 active 前必须同时确认当前媒体的真实 `hwdec-current`、平台 GPU 后端、FP16 中间表面、目标 HDR 输出和模型完整性；任一条件消失都先恢复原始 libmpv 画面，再释放插帧资源，禁止 CPU 插帧和静默 SDR 回退。Windows 后端边界为 mpv D3D11 源 HWND → WGC `R16G16B16A16_FLOAT` → DirectML flow/mask → D3D12 FP16 合成与 scRGB/HDR10 输出；Android 边界为 MediaCodec → RGBA_FP16 AHardwareBuffer → ncnn Vulkan flow/mask → Vulkan FP16 合成与 HDR Surface。运动估计只读取色调压缩代理帧，最终像素必须从原始高精度帧 warp/composite。HDR10+ 与 Dolby Vision 的动态映射先由 mpv/libplacebo 应用，输出不宣称合成新的 Dolby Vision RPU。模型和原生后端尚未同时通过许可、校验值与真机矩阵时，设置界面只显示能力原因并禁用“自动”，诊断不得报告 active。

Windows 的 active 还必须是持续条件，不能由单次隐藏 `Present` 永久锁定：输出层至少连续完成两个无过期 tick 的源帧对后才可显示，并作为 mpv 源 HWND 的 owned overlay 固定在 Tauri/WebView 与源画面之间。WGC 纹理必须用 `SystemRelativeTime` 映射到 mpv 媒体时钟，禁止用队列消费时刻给积压帧编号。native 记录真实 Present、生成帧、推理耗时和过期 tick；过期输出直接丢弃，禁止追补历史帧。连续两个帧对无法维持目标节奏、输出层丢失安全 Z-order，或可见输出超过 350ms 没有新 Present 时，必须先隐藏输出层、撤销音频前视补偿并恢复 mpv 原画面；单次 DirectML Run 超过 250ms 还必须由独立 watchdog 请求 ONNX Runtime 取消，避免停止路径无限等待同一个同步推理。状态降为性能旁路，稳定帧序列重新通过门控后才允许恢复 active。

这些工作不代表 Android 已可正式发布。当前 ARM64 APK 已进入真机验证：包内包含 libmpv、FFmpeg、JNI bridge、OhMyCine Rust 库和 CA 证书，JNI 导出符号与 `is.xyz.mpv.MPVLib` 匹配；首轮日志确认 SurfaceView 与 `gpu-next` 已完成配置，同时暴露了原生 FFmpeg TLS 握手兼容问题，因此加入 Rust TLS 回环桥，仍需在同一远程媒体上复测画面、音频和 Range seek。远程 header、字幕、暂停/seek、MediaCodec 兼容性、SAF 文件/目录授权恢复、横竖屏切换和 Activity 生命周期仍需继续真机覆盖。Android 启动图标使用与桌面相同的“影院之眼”母版，并生成 mdpi 至 xxxhdpi 的普通、圆形和 Android 8+ adaptive icon 资源。预览渠道已具备固定签名、Release APK 校验下载和系统安装确认；正式商店签名、系统返回键、系统栏避让，以及 armeabi-v7a/x86_64 等额外 ABI 仍待完成。桌面窗口拖拽、最小化、最大化与关闭按钮在移动环境中不得作为导航依赖。

### 9.9 GPL 合规说明

libmpv 是 GPL-2.0 协议，嵌入使用时**需要你的项目也开源**。由于 OhMyCine 本身采用 GPL-3.0 协议，这没有冲突。但需要注意：
- 最终发布包必须包含 libmpv 的源码或提供获取途径
- 需在 LICENSE 文件中注明 libmpv 的 GPL-2.0 协议
- 修改过的 libmpv 源码必须公开

## 10. 快捷键系统

当前 Player 已实现以下基础交互：点击视频区域或按 `Space` 切换播放/暂停；左右方向键短按后退/前进 5 秒；长按右方向键临时切换到设置中的长按倍速，松开恢复当前视频原速度；长按左方向键连续后退；上下方向键按 5% 步长调节音量。播放器固定按键在输入框、选择框和可编辑区域聚焦时不得触发，也不得被用户快捷键覆盖。

播放器控制与页面导航使用两套普通全局快捷键映射。播放器映射仅在播放页生效，`H` 默认立即隐藏控制 UI，完整控制栏只由鼠标/触摸移动恢复。默认 `QWERTYUIOP` 依次执行上一集、后退 10 秒、播放/暂停、前进 10 秒、下一集、静音、切换倍速、切换字幕、切换音轨和显示当前队列状态；播放设置状态与全屏动作仍可由用户另行绑定，但不额外占用默认按键。

键盘动作不得唤醒完整控制栏，只在右上角显示约 1.8 秒的紧凑 OSD。倍速、字幕和音轨通过重复按键循环可用值；队列和画面设置只显示当前状态，避免键盘打开需要鼠标操作的大面板。鼠标已经唤醒控制栏时，键盘动作不得强制隐藏现有界面；控制栏原本隐藏时，键盘动作必须保持其隐藏状态。播放器页发生同键冲突时播放动作优先，其他页面仍可执行导航动作。

桌面控制 UI 的自动隐藏不能只依赖控制条子元素的 `mouseleave`。鼠标从 WebView 直接移到应用窗口外时，文档根节点必须释放 `controlsInteracting` 等临时交互锁、关闭字幕/音轨等临时菜单并重新启动 3 秒隐藏计时；窗口 blur 同样执行该清理。否则子控件丢失离开事件后，控制层会永久停留。

首页、设置、数据源管理和每个动态媒体源入口使用独立的导航快捷键映射。两组映射保存到 `settings.sqlite`，可在设置页捕获组合键、检测同组重复占用、清空或恢复默认；它们不得覆盖 `Space`、任一方向键和 `Escape` 等播放器固定按键。删除媒体源时同步删除该动态入口的快捷键映射。

每个视频按 `sourceId + mediaIdentity` 在 `player_preferences.sqlite` 保存字幕选择、音轨、字幕偏移、播放速度、画面比例和填充模式。播放速度只属于单视频偏好，新媒体先回到 1.0x，再恢复该媒体自己的记录，不使用第二套全局倍速覆盖。字幕/音轨优先按同一媒体内的数字轨道 ID 精确恢复，ID 不可用时再使用语言、标题、编码和声道指纹；保存过程持有已加载或用户明确选择的稳定草稿，不能被启动期尚未完成的轨道快照覆盖成“字幕关闭/音轨为空”。本地下载字幕只保存 Player `cache/subtitles` 内的受控缓存路径，禁止保存远程字幕 URL、签名播放 URL 或 Header。删除媒体源时同步删除来源播放记录、单视频偏好和来源拥有的字幕缓存。设置页“清除播放缓存”清除媒体/扫描/字幕缓存和全部单视频偏好，但保留数据源、凭据、播放记录与全局软件设置。

字幕/音轨菜单使用加载阶段已经缓存的轨道列表，打开菜单或选择已知轨道时只做乐观 UI 更新，不立即同步读取 libmpv 完整 `track-list`。完整轨道刷新只允许在媒体加载稳定阶段或受控后台刷新中执行，避免轨道切换期间的同步属性查询占住共享 mpv 锁，导致视频继续播放但全部控制命令失去响应。

字幕菜单按来源分组：Player 本地搜索下载并保存在本机 cache 的字幕位于顶部“本地下载”组；其后用分割线展示视频内嵌轨道、同目录字幕和媒体源提供的字幕。Emby 搜索下载到服务端的字幕仍属于媒体源组，不得与 Player 本地下载缓存混淆。

本地文件、OpenList/Alist、CloudDrive2 与 WebDAV 的 `getDetail` 在数据源边界内按需重新检查视频同目录字幕，不使用跨播放永久缓存，支持 `.srt`、`.ass`、`.ssa`、`.vtt`、`.sub`。只接受与视频文件同名，或在完整视频主文件名后追加点号、横线、下划线、空格、方括号及语言/版本后缀的字幕；例如 `Movie.zh-CN.srt` 匹配 `Movie.mkv`，`Movie2.srt` 不得误匹配。远程字幕地址按当前 DataSource 的受控流地址流程临时生成，不持久化签名 URL、Header 或凭据；单个字幕解析失败不得阻断视频详情和播放。

单视频偏好恢复分为两段：倍速、字幕偏移和画面模式可立即恢复；字幕、音轨和缓存外部字幕必须等待播放/轨道元数据稳定后再匹配。Android 缓存外部字幕还必须明确等待当前媒体进入 `video-ready`，不能在远程流仍执行 `loadfile` 时提前调用同步 `sub-add`，否则会阻塞 Android UI 命令线程并触发输入 ANR。Android 恢复只消费 `useMpv` 在 duration/load 稳定阶段产生的轨道数组、`video-ready` 和后续响应式更新，禁止在 `PlayerView` 用短间隔循环调用原生 `mpv_track_state`；该同步调用会逐条读取完整 `track-list`，并可能与播放命令串行后卡住 Android Rust → Kotlin → libmpv 控制通道。用户选择字幕/音轨、载入或下载字幕后立即写入偏好；倍速、偏移和画面连续设置仍可短延迟合并写入，切换媒体、离开路由和卸载前必须 flush。已知 `sid`、`aid` 切换和用户触发的 `sub-add` 使用 libmpv 同步命令并直接返回真实执行结果，但不得在这些交互后同步读取完整 `track-list`。禁止为短生命周期 C 字符串建立跨调用异步命令队列。用户手动选择字幕/音轨或下载字幕时取消尚未完成的旧轨道偏好恢复，避免旧选择覆盖新操作。

手机字幕搜索使用独立全屏工具页，不复用桌面居中弹窗的尺寸约束。原生 Android 通过显式 mobile layout 状态进入该页面，不依赖横屏宽度或 hover/pointer 查询；竖屏上下排列，横屏使用左侧条件栏和右侧独立结果区。页面显示本次实际参与的 OpenSubtitles、迅雷关键词和射手哈希提供器；新档案默认开启无需凭据的迅雷关键词搜索，避免 Android 没有桌面 OpenSubtitles 凭据时退化为仅哈希搜索。来源、关键词模式、语言和搜索按钮位于稳定控制区，并明确显示搜索中、结果数量、无结果、下载中和错误状态。

Android Activity 使用 edge-to-edge 布局时，状态栏和导航栏前景固定使用适合深色 Cinema OS 表面的浅色图标，不跟随系统日间主题切换为黑色；普通浏览页、全屏搜索页和播放器退出后的页面都必须保持顶部时间、网络与电量信息可读。

所有交给 `sub-add` 的外部字幕先由 Tauri 准备到 `cache/mpv-subtitles/<URL或路径哈希>.<ext>` 短路径运行缓存：本地字幕通过 Rust 文件 API 从受控原路径复制，兼容超过 Windows `MAX_PATH` 的 `\\?\` 规范化路径；Emby 和其他媒体源的 HTTP(S) 字幕由 Tauri 限时下载，限制重定向、响应大小和字幕扩展名。mpv 只读取短本地路径，不直接处理长路径、签名 URL 或 API Token；运行缓存文件名只保存不可逆哈希，不持久化原 URL 或凭据。

字幕菜单提供“载入本地字幕”。用户通过系统文件选择器显式选择 `.srt`、`.ass`、`.ssa`、`.vtt` 或 `.sub` 后，Rust 规范化并校验文件、拒绝空文件和超过 12 MiB 的内容，再复制到当前 `sourceId + mediaIdentity` 所属的 `cache/subtitles` 哈希目录。播放偏好只保存这份受控缓存路径，绝不保存用户原始字幕绝对路径；载入后保持视频画面和播放状态不变。

离开播放路由必须执行真正的 `mpv_stop`，而不是仅设置暂停。停止命令先将 Windows mpv HWND 标记为非播放激活并隐藏，再向 libmpv 发送 `stop` 卸载当前媒体；窗口移动、缩放、恢复和焦点事件仅能同步处于播放激活状态的 HWND。这样返回首页或设置页后旧视频帧不会继续显示，也不会在滚动/缩放时作为滞后的原生底层窗口跟随。

点击视频画面空白区域可以切换播放/暂停，但底部控制栏的按钮、进度条、菜单以及控制栏自身的空白区域都属于交互 chrome，不得冒泡触发画面点击暂停。

```typescript
// src/composables/useKeyboard.ts

export const shortcuts = {
  // 播放控制
  'Space':        { action: 'player.togglePause', label: '播放/暂停' },
  'ArrowLeft':    { action: 'player.seekBackward', args: 10, label: '后退10秒' },
  'ArrowRight':   { action: 'player.seekForward', args: 10, label: '前进10秒' },
  'Ctrl+ArrowLeft':  { action: 'player.seekBackward', args: 60, label: '后退1分钟' },
  'Ctrl+ArrowRight': { action: 'player.seekForward', args: 60, label: '前进1分钟' },
  'ArrowUp':      { action: 'player.volumeUp', label: '增大音量' },
  'ArrowDown':    { action: 'player.volumeDown', label: '减小音量' },
  'M':            { action: 'player.toggleMute', label: '静音' },

  // 字幕/音轨
  'S':            { action: 'player.nextSubtitle', label: '切换字幕' },
  'A':            { action: 'player.nextAudio', label: '切换音轨' },

  // 弹幕
  'D':            { action: 'player.toggleDanmaku', label: '弹幕开/关' },
  'Shift+D':      { action: 'player.danmakuSettings', label: '弹幕设置' },
  'Shift+ArrowUp':   { action: 'player.danmakuOpacityUp', label: '弹幕透明度+' },
  'Shift+ArrowDown': { action: 'player.danmakuOpacityDown', label: '弹幕透明度-' },

  // 窗口
  'F':            { action: 'player.toggleFullscreen', label: '全屏' },
  'Escape':       { action: 'player.exitFullscreen', label: '退出全屏' },
  'P':            { action: 'player.togglePiP', label: '画中画' },

  // 导航
  'Ctrl+F':       { action: 'ui.openSearch', label: '搜索' },
  'Ctrl+,':       { action: 'ui.openSettings', label: '设置' },

  // 信息
  'I':            { action: 'player.showInfo', label: '显示媒体信息' },
  'Ctrl+Shift+I': { action: 'ui.openDevTools', label: '开发者工具' },
}
```

## 11. 弹幕系统 (Danmaku)

弹幕是视频播放器的核心社交功能之一。OhMyCine 的弹幕系统支持从外部弹幕源加载弹幕数据，以滚动字幕的形式叠加在视频上方。

### 11.1 弹幕数据格式

采用兼容 [DanmakuFactory](https://github.com/hihkm/DanmakuFactory) 的通用格式，同时支持 XML 和 JSON 两种来源：

**B 站 XML 格式**（最广泛的弹幕格式）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<i>
  <d p="时间,模式,字号,颜色,发送时间,弹幕池,用户Hash,弹幕ID">弹幕内容</d>
</i>
```

`p` 属性字段说明：

| 位置 | 含义 | 示例 |
|------|------|------|
| 0 | 出现时间 (秒) | `12.345` |
| 1 | 弹幕模式 | `1`=滚动, `4`=底部, `5`=顶部, `7`=高级 |
| 2 | 字号 | `25` |
| 3 | 颜色 (十进制) | `16777215`=白色 |
| 4 | 发送时间 (Unix时间戳) | `1609459200` |
| 5 | 弹幕池 | `0`=普通, `1`=字幕 |
| 6 | 用户Hash | `abc123def` |
| 7 | 弹幕ID | `1234567890` |

**JSON 格式**（用于自定义弹幕和 API 返回）：

```typescript
interface DanmakuItem {
  time: number        // 出现时间 (秒)
  mode: number        // 弹幕模式: 1=滚动, 4=底部, 5=顶部
  size: number        // 字号 (默认 25)
  color: number       // 颜色 (十进制, 默认白色 16777215)
  text: string        // 弹幕内容
  sender?: string     // 发送者 (可选)
  timestamp?: number  // 发送时间戳 (可选)
}
```

### 11.2 弹幕来源与加载策略

弹幕来源通过 DataSource 思路统一管理：

```typescript
interface DanmakuSource {
  id: string
  type: 'local-xml' | 'local-json' | 'api'
  name: string

  // 加载弹幕 (按时间范围分段或全量)
  load(mediaId: string, options?: DanmakuLoadOptions): Promise<DanmakuItem[]>

  // 测试连通性 (API 类型)
  test?(): Promise<boolean>
}

interface DanmakuLoadOptions {
  startTime?: number   // 从第几秒开始加载
  endTime?: number     // 到第几秒结束
}
```

**支持的来源类型**：

| 来源 | 说明 | 典型用法 |
|------|------|----------|
| `local-xml` | 本地 XML 弹幕文件 | 从 B 站下载的弹幕 XML |
| `local-json` | 本地 JSON 弹幕文件 | 第三方工具导出的弹幕 |
| `api` | 远程弹幕 API | 弹弹Play / 自建弹幕服务器 |

**弹幕文件自动匹配**：

Player 在浏览本地文件或 Emby/Jellyfin 媒体时，自动在同目录下查找匹配的弹幕文件：

```
/media/movies/
  Inception (2010).mkv
  Inception (2010).xml        ← 自动加载
  Inception (2010).danmaku.json  ← 自动加载
```

匹配规则：去掉视频扩展名，在同目录下查找 `.xml` / `.json` / `.danmaku.json` 后缀的同名文件。

### 11.3 弹弹Play API 兼容

[弹弹Play](https://www.dandanplay.com/) 是国内主流的弹幕共享平台，提供丰富的弹幕 API。OhMyCine 兼容其 API 格式，支持用户自建弹幕服务器。

当前实现使用弹弹play开放弹幕网络 v2：先调用 `POST /api/v2/match`，再调用 `GET /api/v2/comment/{episodeId}?withRelated=true&chConvert=1`，解析 `p="时间,模式,颜色,用户ID"` 标准字段。官方 API 凭据在构建时注入，由 Rust 生成时间戳签名；前端包、普通设置和日志不保存 AppSecret。获取弹幕时的 302 加速跳转由独立原生客户端限次跟随，不复用也不修改 Android 播放 302 桥接。

匹配请求只包含逻辑媒体标题/文件名和时长，不包含本地绝对路径、`content://` URI、播放 URL、Emby API Key、请求头或签名地址。自定义 API 只允许配置不含 userinfo、query、fragment 的 HTTP(S) 根地址。

**弹弹Play API 模式**：

```typescript
// src/services/danmaku/dandanplay.ts

export class DandanplaySource implements DanmakuSource {
  readonly type = 'api' as const
  private baseURL: string
  private episodeId?: number

  async load(mediaId: string): Promise<DanmakuItem[]> {
    // 弹弹Play 匹配 API
    // POST /api/v2/match
    // 请求体: { fileName, fileHash, fileSize, videoDuration }
    // 返回: 匹配到的弹幕列表

    const res = await fetch(`${this.baseURL}/api/v2/match`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        fileName: this.extractFileName(mediaId),
        fileHash: '',
        fileSize: 0,
        videoDuration: 0,
      }),
    })

    const data = await res.json()
    return this.parseDanDanPlay(data.matches)
  }
}
```

**弹弹Play 弹幕格式转换**：

```typescript
private parseDanDanPlay(matches: any[]): DanmakuItem[] {
  return matches.flatMap(match =>
    match.danmakus.map((d: any) => ({
      time: d.time,
      mode: d.mode === 4 ? 4 : d.mode === 5 ? 5 : 1,
      size: d.fontSize || 25,
      color: d.color || 16777215,
      text: d.text,
      sender: d.userId,
    }))
  )
}
```

### 11.4 弹幕渲染引擎

弹幕渲染在 Vue 前端使用 Canvas 实现，与 libmpv 视频画面分层叠加：

```
┌─────────────────────────────────────┐
│ 播放器窗口                           │
│ ┌─────────────────────────────────┐ │
│ │ libmpv 视频渲染层 (底层)         │ │
│ ├─────────────────────────────────┤ │
│ │ Canvas 弹幕层 (透明叠加)         │ │
│ │ ┌───┐    ┌────────┐            │ │
│ │ │弹1│───→│  弹幕2  │───→ 弹幕3  │ │
│ │ └───┘    └────────┘            │ │
│ │                    ┌────┐      │ │
│ │                    │弹4 │───→  │ │
│ │                    └────┘      │ │
│ ├─────────────────────────────────┤ │
│ │ Vue UI 控制层 (字幕/进度/OSD)   │ │
│ └─────────────────────────────────┘ │
└─────────────────────────────────────┘
```

**渲染层架构**：

```typescript
// src/services/danmaku/renderer.ts

export class DanmakuRenderer {
  private canvas: HTMLCanvasElement
  private ctx: CanvasRenderingContext2D
  private trackManager: TrackManager
  private animationId: number | null = null

  constructor(container: HTMLElement) {
    this.canvas = document.createElement('canvas')
    this.canvas.style.cssText = `
      position: absolute;
      top: 0; left: 0;
      width: 100%; height: 100%;
      pointer-events: none;
    `
    container.appendChild(this.canvas)
    this.ctx = this.canvas.getContext('2d')!
    this.trackManager = new TrackManager()
  }

  // 发射一条弹幕到渲染轨道
  emit(item: DanmakuItem) {
    const track = this.trackManager.allocate(item)
    if (!track) return // 轨道已满，丢弃

    const el: RunningDanmaku = {
      ...item,
      track: track.index,
      x: this.canvas.width,
      y: track.y,
      width: this.measureText(item.text, item.size),
      speed: this.calcSpeed(item),
      opacity: 1,
    }

    this.trackManager.addRunning(el)
  }

  // 动画循环
  start() {
    const loop = () => {
      this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height)
      this.trackManager.update(this.ctx)
      this.animationId = requestAnimationFrame(loop)
    }
    loop()
  }

  stop() {
    if (this.animationId) {
      cancelAnimationFrame(this.animationId)
      this.animationId = null
    }
  }

  resize(width: number, height: number) {
    this.canvas.width = width
    this.canvas.height = height
  }
}
```

**轨道管理**：

弹幕按轨道分配，避免重叠：

```typescript
class TrackManager {
  private tracks: Track[]

  allocate(item: DanmakuItem): Track | null {
    // 找到有足够空间的轨道
    // 滚动弹幕: 从右往左滚，需要检查轨道内是否还有未滚完的弹幕
    // 顶部/底部弹幕: 直接找空轨道
    for (const track of this.tracks) {
      if (track.canFit(item)) return track
    }
    return null // 所有轨道已满
  }
}
```

### 11.5 弹幕设置

弹幕设置集成在 Player 设置页面中：

当前桌面和 Android 播放控制层都提供相邻的“弹”开关与设置按钮；设置面板支持滚动/顶部/底部类型、不透明度、字号、速度、显示区域、密度、粗体描边和关键词屏蔽。全局设置页可在官方 API 与自定义兼容 API 之间切换。

```typescript
interface DanmakuSettings {
  enabled: boolean              // 是否开启弹幕
  opacity: number               // 弹幕透明度 (0-1, 默认 0.8)
  fontSize: number              // 字号缩放 (0.5-2.0, 默认 1.0)
  speed: number                 // 滚动速度 (0.5-2.0, 默认 1.0)
  maxLines: number              // 最大轨道数 (默认根据屏幕高度自动计算)
  showTop: boolean              // 显示顶部固定弹幕
  showBottom: boolean           // 显示底部固定弹幕
  showScroll: boolean           // 显示滚动弹幕
  blockKeywords: string[]       // 屏蔽关键词
  blockUsers: string[]          // 屏蔽用户
  sources: DanmakuSource[]      // 弹幕来源列表
}
```

### 11.6 弹幕快捷键

| 快捷键 | 功能 |
|--------|------|
| `D` | 开启/关闭弹幕 |
| `Shift+D` | 弹幕设置面板 |
| `Shift+↑` | 增大弹幕透明度 |
| `Shift+↓` | 减小弹幕透明度 |

### 11.7 Tauri 弹幕 Commands

```rust
// src-tauri/src/commands/danmaku.rs

#[command]
pub async fn danmaku_load_xml(path: String) -> Result<Vec<DanmakuItem>, String> {
    let content = tokio::fs::read_to_string(&path).await
        .map_err(|e| format!("Failed to read danmaku file: {}", e))?;
    parse_bilibili_xml(&content)
}

#[command]
pub async fn danmaku_load_json(path: String) -> Result<Vec<DanmakuItem>, String> {
    let content = tokio::fs::read_to_string(&path).await
        .map_err(|e| format!("Failed to read danmaku file: {}", e))?;
    serde_json::from_str(&content)
        .map_err(|e| format!("Failed to parse danmaku JSON: {}", e))
}

#[command]
pub async fn danmapi_fetch(url: String, params: serde_json::Value) -> Result<String, String> {
    let client = reqwest::Client::new();
    let resp = client.post(&url)
        .json(&params)
        .timeout(std::time::Duration::from_secs(10))
        .send().await
        .map_err(|e| format!("Danmaku API request failed: {}", e))?;
    resp.text().await
        .map_err(|e| format!("Failed to read danmaku response: {}", e))
}
```

### 11.8 与 04-hub-design.md 插件类型的关系

弹幕来源扩展可通过 Hub 插件系统实现。`04-hub-design.md` 中已定义 `player` 类型插件：

```
player — 播放器扩展 — 弹幕、歌词、特效
```

第三方弹幕源可以作为 `player` 类型插件分发，通过 `DanmakuSource` 接口集成。

## 12. Server 在线插件媒体源

Player 不安装或执行 Server 插件，也不包含 Bilibili 等提供方专用分支。`ServerDataSource` 将 Server 发布的在线媒体库作为动态子来源，消费统一的导航、Feed、详情、作品/分集/版本、播放方案和动作 DTO；插件失效只影响其来源，Player 的本地与其它直连数据源保持可用。

插件页面由 Player 原生组件渲染。插件可贡献 Hero、横向卡片、海报墙、列表、搜索、刷新与标准动作，但不能传入 HTML、JavaScript、CSS 或访问 Pinia/Tauri。用户在设备侧决定哪些来源栏目参与总主页及其顺序。

Server 来源入口先展示其媒体库卡片。普通本地/115 媒体库进入后按 Server 分类规则展示华语电影、外语电影、剧集等标准分类，再进入内容；声明 `hierarchical` 的插件在线库则可使用任意深度的 `branch/feed` 导航。Player 只解释通用节点类型和 Server 签名 token，不硬编码 Bilibili 栏目名称，也不允许普通物理媒体库借此变成不受约束的插件树。

媒体库封面同样保持 Player 独立优先：Player 自己索引的本地、OpenList/Alist、CloudDrive2 等原始目录从本地扫描结果选取最多 9 张不重复海报；只有 1～8 张时先确定性循环补齐九个展示槽位，再以 1920×1080 语义的“风格 3”构图呈现——左侧标题区、海报主题色渐变背景、右侧三列阴影圆角海报、整列 -15.8° 倾斜和 `315426987` 显眼位顺序——并用稳定 revision 失效缓存，不在用户媒体目录写文件；直连 Emby/Jellyfin 优先使用 provider 自带库图。Server 专区和物理/插件媒体库最外层入口使用固定图，进入后的物理分类与插件导航节点才消费 Server 生成的同源 `artworkUrl/artworkRevision/artworkSource`；Player 不接收或二次组合 Server 候选。Server 生成图的短时签名 query 只存在运行时，图片缓存 sidecar 仍只保存不可逆 URL hash。

播放身份分为：

```text
MediaWork → MediaSegment → MediaVersion → StreamVariant
作品          集/分P          版本/片源          清晰度/码率
```

选集菜单只负责集数、分 P 和媒体版本；独立清晰度按钮只切换当前版本的可用 `StreamVariant`。短时 URL、敏感 Header 和刷新令牌不写入持久状态；跨来源播放继续清除 Server Bearer 与提供方私有 Header。

Bilibili 是首个真实在线插件，用它验证声明式导航、主页推荐、搜索、详情、DASH、多清晰度、字幕、弹幕和下载入口。该名称与任何站点 API 细节不得进入 Player 核心数据源分支。

## 13. 平台适配

当前平台适配状态以 Windows MVP 为先；下表中的非 Windows 项是目标能力，不代表当前 CI 或 beta release 会构建对应 Player 包。

| 功能 | Windows | macOS | Linux | Android |
|------|---------|-------|-------|---------|
| 播放引擎 | 已完成 Windows MVP libmpv 嵌入 | 后续 libmpv 嵌入 | 后续 libmpv 嵌入 | ARM64 SurfaceView + mpv-android 已构建，待真机验证 |
| HDR | Windows HDR 目标/验证继续推进 | 后续 HDR10/DV | 后续部分支持 | 后续设备相关 |
| 窗口风格 | 无边框 + 自定义标题栏 | 后续原生标题栏 | 后续 GTK/Qt 适配 | 后续全屏 |
| 通知 | Windows 通知 | 后续 macOS 通知 | 后续 libnotify | 后台播放媒体通知与系统控制已接入，待真机复验 |
| 快捷键 | 全局快捷键 | 后续全局快捷键 | 后续全局快捷键 | 触摸手势已接入，待真机验证 |
| 文件关联 | .mkv/.mp4 等 | 后续 .mkv/.mp4 等 | 后续 .mkv/.mp4 等 | 后续 Intent Filter |

## 14. Player 下载管理与完整离线播放

Player 下载是独立本机能力，不等同于 Server 的“下载入库”。媒体详情、剧集和播放上下文统一通过媒体操作系统建立只含 `sourceId + itemId + mediaSourceId + variantId` 等稳定身份的任务；临时播放 URL、302 Location、Authorization、Cookie、API Key、Server device token 和 Provider Header 仅在 Rust 单次解析内存中存在，不进入任务 SQLite、事件、路由或日志。

```text
MediaAction / 当前版本与静态清晰度
  → Pinia Download Store / 下载中心
  → Tauri 常驻调度器
  → 稳定身份重新解析 → Range/单流传输 → 原子完成
  → offline_media.sqlite + data/offline/<package>/assets
  → OfflineDataSource → 本地优先播放 → 在线回退
```

下载中心提供进行中、已完成、失败和设置分页；同时任务数、单任务桌面分段数与全局限速分别配置。调度器公平领取队列，用户暂停不占槽且重启后保持暂停；进程中断任务按安全 checkpoint 恢复。取消不是失败状态：worker 收敛后删除精确拥有的 partial/segment/task 并从 UI 消失，暂时无法清理的路径只进入内部 cleanup 队列。

桌面仅在总长度、`206 Content-Range` 与实体身份可信时使用随机写分段；其它情况退化为安全单流。续传以强 ETag，或 Last-Modified + 总大小证明同一实体；身份变化、Range 不可信或区间覆盖不完整时只清除当前任务残片并从零开始。跨源重定向清空 Provider Header，HTTPS 不允许降级到 HTTP。Android 通过 SAF 持久树授权写入并保持单流，但复用同一并发、限速、暂停、取消与恢复语义。

视频原子完成并写入离线索引后即为可播放；海报、背景、单集图、外置字幕和 Provider 弹幕保存到不受普通 LRU 影响的包资产目录，并允许独立重试。附件先完整校验并原子登记新文件，再回收旧文件；失败重试不得删除已有有效资产。离线资源的路径为包内受控相对路径，写入、读取和删除都拒绝 traversal、符号链接、junction 与 Windows reparse point 逃逸。

内置 `OfflineDataSource` 使用 `__offline__` 路由身份，在全部远程来源不可用时仍能按作品、季、集展示快照和资产。播放前先按原来源精确媒体版本查询本地文件，由 Rust 复验 root/SAF 所有权、大小与指纹；有效时始终本地优先，缺失或被替换时纠正离线索引并在来源可用时在线回退。离线播放的历史、进度与完成状态仍写回原 `sourceId + itemId`。当前跨 Provider 父剧集稳定 ID 尚未进入公共 `MediaItem`，因此离线剧集层级只在同一来源内按剧名/季号投影，不能把显示标题宣称为全局稳定身份。

Server 物理媒体与 Emby/Jellyfin 可以按稳定 entry/item/media-source 身份重新解析短期地址。Server 在线插件在缺少用途受限、可重复解析的离线流契约时保持禁用，不以播放专用 URL 绕过权限或临时地址边界。
