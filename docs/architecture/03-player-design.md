# OhMyCine Player — 播放器设计文档

## 1. 概述

OhMyCine Player 是一款**独立可用**的跨平台沉浸式家庭影院播放器，核心特点：
- **独立运行** — 无需 Server，原生连接 Emby/Jellyfin/OpenList/Alist/CloudDrive2
- **Cinema OS 风格 UI** — 液态玻璃设计语言，深色主题，电影感排版
- **libmpv 引擎** — 全格式支持，硬件解码，HDR/Dolby Vision，沉浸式嵌入渲染
- **全平台** — Windows, macOS, Linux (桌面), Android (移动)
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
ohmycine-player/
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
│   │   ├── player/               # 播放器相关组件
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

Player 的核心设计是 **DataSource 抽象层** — 每种媒体源（Emby、Jellyfin、OpenList/Alist 等）都是一个 DataSource 实现，通过统一接口访问。Server 也只是其中一个可选的 DataSource。

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
│  │ (原生API)    │ │(原生API) │ │(WebDAV)│ │ (WebDAV)   │  │
│  └──────────────┘ └──────────┘ └───────┘ └────────────┘  │
│              │          │          │          │             │
│  ┌───────────▼──────────▼──────────▼──────────▼────────┐  │
│  │  CloudDriveDataSource (占位)                          │  │
│  │  ├─ 115网盘  (待实现)                                │  │
│  │  ├─ 123盘    (待实现)                                │  │
│  │  └─ 夸克网盘 (待实现)                                │  │
│  └─────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  ServerDataSource (可选 - 连接 OhMyCine Server)       │   │
│  │  连接后: 获取Server的媒体库、下载任务、AI推荐等       │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 4.2 DataSource 接口定义

```typescript
// src/services/datasource/types.ts

export interface MediaItem {
  id: string
  sourceId: string
  libraryId?: string
  name: string
  titleLogoUrl?: string
  type: 'movie' | 'series' | 'episode' | 'folder' | 'file'
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

export interface AudioTrack {
  index: number
  language: string
  codec: string
  channels: number
  isDefault: boolean
}

export type DataSourceType = 'emby' | 'jellyfin' | 'alist' | 'clouddrive2' | 'server' | '115' | '123' | 'quark'

export interface DataSourceConfig {
  id: string
  type: DataSourceType
  name: string
  displayName?: string
  iconUrl?: string
  order: number
  url: string
  apiKey?: string
  username?: string
  password?: string
  // 扩展配置
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

  // 配置导出（用于同步给Server）
  exportConfig(): DataSourceConfig
}
```

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
  async searchAll(keyword: string): Promise<{ source: DataSource; results: MediaItem[] }[]> {
    const tasks = Array.from(this.sources.values()).map(async source => ({
      source,
      results: await source.search(keyword).catch(() => []),
    }))
    return Promise.all(tasks)
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
      case 'server': return new ServerDataSource()
      // 占位
      case '115': throw new Error('115网盘支持即将推出')
      case '123': throw new Error('123盘支持即将推出')
      case 'quark': throw new Error('夸克网盘支持即将推出')
      default: throw new Error(`Unknown data source type: ${type}`)
    }
  }
}
```

### 4.6 配置存储

Player 配置存储在 Tauri 的 `app_data_dir` 下：

```json
// {app_data_dir}/config.json
{
  "datasources": [
    {
      "type": "emby",
      "name": "家庭Emby",
      "url": "http://nas:8096",
      "apiKey": "xxx"
    },
    {
      "type": "alist",
      "name": "NAS OpenList/Alist",
      "url": "http://nas:5244",
      "password": ""
    }
  ],
  "server": {
    "url": "",
    "apiKey": ""
  },
  "ai": {
    "provider": "openai",
    "apiKey": "",
    "model": "gpt-4o"
  },
  "ui": {
    "theme": "dark",
    "language": "zh-CN",
    "homeLayout": [...]
  }
}
```

### 4.7 配置同步机制

Player 连接 Server 时，双向同步配置：

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

  // 连接时自动同步
  async autoSync(): Promise<void> {
    await this.pushToServer()
    await this.pullFromServer()
  }
}
```

## 5. 网盘自动刮削系统

### 5.1 设计背景

Emby/Jellyfin 自带刮削功能，但 OpenList/Alist/CloudDrive2 这类网盘数据源**没有元数据**——只有原始文件名。Player 需要自己实现刮削，为网盘文件生成海报墙。

本系统只面向“原始文件源”：OpenList/Alist、CloudDrive2、本地文件以及未来类似的自定义文件源。Emby/Jellyfin 已经由服务端维护媒体库和元数据，默认不套用 Player 本地刮削分类规则。

刮削系统必须遵守三条边界：

1. **只读远端**：Player 不对 OpenList/Alist、CloudDrive2 或本地源执行上传、重命名、移动、删除、创建目录等写操作。
2. **本地缓存**：扫描日志、匹配结果、海报、背景图、用户修正和分类结果都保存在 Player 本地 app data。
3. **任意根目录**：从用户选择的根目录开始自动识别结构，不要求物理目录顶层必须叫 `movie`、`tv`、`Movies` 或 `TV`。

### 5.2 刮削流程

```
用户选择的根目录 (OpenList/Alist rootPath / CloudDrive2 WebDAV / 本地文件夹)
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

### 5.2.1 标准目录模式

标准目录模式不是固定目录名，而是扫描器对用户选择根目录下面的结构进行评分后的结果。以下结构都可以作为标准模式信号：

- `分类/片名 (年份)/片名.mkv`
- `片名 (年份)/片名.mkv`
- `分类/剧名/Season 01/S01E01.mkv`
- `剧名/Season 01/S01E01.mkv`
- `剧名/第01集.mkv`

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

### 5.2.2 非标准目录模式

非标准目录模式默认目录信息不可靠，适合所有影片、剧集、综艺混在一个或多个文件夹里的情况。此模式主要依赖文件名解析和 TMDB 匹配：

- 每个视频文件先作为独立候选项。
- 解析标题、年份、季、集、分辨率、片源、编码、音频和制作组。
- 剧集文件按标题 + 季集号在本地聚合成剧集。
- 无法识别的文件保留为 `未识别`，仍然可以从文件夹视图或未识别列表播放。
- 用户修正写入本地 override 表，不写回网盘。

### 5.2.3 通用分类规则

分类规则是刮削后的**本地逻辑分组**，不是物理目录约束。它影响海报墙分组、筛选、媒体库标签、聚合首页和未来 AI 推荐上下文，但不移动或重命名远端文件。

规则适用于 OpenList/Alist、CloudDrive2、本地文件等原始文件源；Emby/Jellyfin 默认使用服务端已有分类与元数据。

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

### 5.2.4 分类设置页

设置页中新增“刮削与分类”，与“管理数据源”同级，进入后管理 TMDB 配置、默认语言地区、扫描模式和分类规则。

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
  private apiKey: string
  private baseURL = 'https://api.themoviedb.org/3'
  private imageBase = 'https://image.tmdb.org/t/p'

  constructor(apiKey: string) {
    this.apiKey = apiKey
  }

  async search(title: string, year?: number): Promise<TmdbResult | null> {
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

```typescript
// src/services/scraper/poster-cache.ts

export class PosterCache {
  private cacheDir: string

  constructor(cacheDir: string) {
    this.cacheDir = cacheDir
  }

  async cache(url: string, tmdbId: number): Promise<string> {
    const localPath = `${this.cacheDir}/${tmdbId}.jpg`

    // 检查缓存是否存在
    try {
      await invoke('fs_exists', { path: localPath })
      return localPath
    } catch {
      // 不存在，下载
    }

    const res = await fetch(url)
    const buffer = await res.arrayBuffer()
    await invoke('fs_write', { path: localPath, data: Array.from(new Uint8Array(buffer)) })

    return localPath
  }

  // 获取海报 (优先本地缓存，否则返回TMDB URL)
  getPosterUrl(record: MediaRecord): string {
    if (record.posterPath) {
      return `asset://localhost/${record.posterPath}`
    }
    return '/assets/default-poster.jpg'
  }
}
```

### 5.8 刮削调度

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
- OpenList/Alist、CloudDrive2、本地文件若缺元数据，使用 Player 本地刮削结果补齐。
- 轮播支持左右切换，并按固定间隔自动切换；用户手动切换后短暂暂停自动轮播。
- Hero 选择逻辑优先使用：继续观看中的高优先级项目、最近添加、评分较高项目；避免展示缺少 backdrop/overview 的裸文件。

**动态数据源侧栏**：
- 侧栏固定在首页和媒体库页面左侧，采用液态玻璃竖向导航。
- 顶部固定为“首页/聚合首页”。
- 其后按 `DataSourceConfig.order` 展示用户已绑定的数据源，例如 Emby、123 云盘、OpenList/Alist、CloudDrive2、本地文件。
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

#### 播放器页面 (PlayerView)

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

| 平台 | 渲染后端 | 窗口嵌入方式 |
|------|----------|-------------|
| Windows | ANGLE (OpenGL ES → D3D11) | `HWND` 子窗口 |
| macOS | Metal (via ANGLE) | `NSView` 子视图 |
| Linux | Vulkan / OpenGL | `X11 Window` / `Wayland Surface` |
| Android | OpenGL ES 2.0 | `SurfaceView` / `TextureView` |

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

```
src-tauri/
  libs/
    windows-x64/
      libmpv-2.dll      # Windows libmpv 动态库
      mpv.lib            # 导入库
    darwin-x64/
      libmpv.dylib       # macOS Intel
    darwin-arm64/
      libmpv.dylib       # macOS Apple Silicon
    linux-x64/
      libmpv.so          # Linux
```

构建脚本自动下载对应平台的libmpv二进制库，链接到最终产物中。

### 9.8 Android 策略

Android 使用相同方案的移动端版本：
1. **libmpv Android** — 交叉编译libmpv为 `.so` 库 (arm64-v8a / armeabi-v7a)
2. 通过 Tauri Android Plugin 调用
3. 渲染到 `SurfaceView`，Vue UI 覆盖在上方
4. 参考项目：[mpv-android](https://github.com/mpv-android/mpv-android)

### 9.9 GPL 合规说明

libmpv 是 GPL-2.0 协议，嵌入使用时**需要你的项目也开源**。由于 OhMyCine 本身采用 GPL-3.0 协议，这没有冲突。但需要注意：
- 最终发布包必须包含 libmpv 的源码或提供获取途径
- 需在 LICENSE 文件中注明 libmpv 的 GPL-2.0 协议
- 修改过的 libmpv 源码必须公开

## 10. 快捷键系统

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

## 12. 平台适配

| 功能 | Windows | macOS | Linux | Android |
|------|---------|-------|-------|---------|
| 播放引擎 | libmpv (嵌入) | libmpv (嵌入) | libmpv (嵌入) | libmpv Android |
| HDR | 支持 (Windows HDR) | 支持 (HDR10/DV) | 部分支持 | 设备相关 |
| 窗口风格 | 无边框 + 自定义标题栏 | 原生标题栏 | GTK/Qt适配 | 全屏 |
| 通知 | Windows通知 | macOS通知 | libnotify | Android通知 |
| 快捷键 | 全局快捷键 | 全局快捷键 | 全局快捷键 | 手势 |
| 文件关联 | .mkv/.mp4等 | .mkv/.mp4等 | .mkv/.mp4等 | Intent Filter |
