# OhMyCine Server — 后端设计文档

## 1. 概述

OhMyCine Server 是一个**以媒体流水线为核心**的自托管后端，负责：
- **发现** — 聚合PT站点搜索，自动匹配元数据
- **下载** — 管理qBittorrent/Transmission下载任务
- **转移** — 下载完成后自动转移到分类目录（本地/网盘）
- **入库** — 自动生成STRM文件，通知Emby/Jellyfin刷新媒体库
- **追更** — 定时追踪剧集更新，自动下载缺少的集数
- **代理** — 302直连播放，零带宽消耗

**核心设计理念**：设置好媒体分类 → 发现页选择视频下载/追更 → 程序自动下载 → 自动转移到指定目录 → 自动通知媒体服务器刷新 → Player客户端展示新媒体。

## 2. 技术栈

| 组件 | 技术 | 说明 |
|------|------|------|
| 语言 | Go 1.22+ | 并发模型好，交叉编译简单 |
| Web框架 | Gin | 高性能HTTP |
| ORM | GORM | 数据库操作 |
| 数据库 | SQLite (默认) / PostgreSQL (可选) | 轻量默认，可扩展 |
| 任务调度 | robfig/cron | 定时任务（追更、STRM同步） |
| 配置管理 | Viper | YAML配置 |
| 日志 | zerolog | 结构化日志 |
| CLI框架 | Cobra | 命令行参数 (仅CLI组件) |
| 容器化 | Docker + docker-compose | 部署 |

## 3. 项目结构

```
ohmycine-server/
├── cmd/
│   └── server/              # 主服务入口
│       └── main.go
│
├── internal/
│   ├── config/              # 配置管理
│   ├── database/            # 数据库连接与迁移
│   ├── models/              # 数据模型 (GORM)
│   ├── handlers/            # HTTP Handlers (API层)
│   │   ├── connection.go    # 连接管理 API
│   │   ├── destination.go   # 存储目标 API
│   │   ├── category.go      # 分类规则 API
│   │   ├── site.go          # 站点管理 API
│   │   ├── download.go      # 下载管理 API
│   │   ├── discovery.go     # 发现页 API
│   │   ├── transfer.go      # 转移任务 API
│   │   ├── strm.go          # STRM管理 API
│   │   ├── file.go          # 文件管理 API
│   │   ├── user.go          # 用户管理 API
│   │   ├── media.go         # 媒体库 API
│   │   ├── sync.go          # Player↔Server同步 API
│   │   └── settings.go      # 系统设置 API
│   ├── services/            # 业务逻辑层
│   │   ├── connection.go    # 连接管理服务
│   │   ├── destination.go   # 存储目标服务
│   │   ├── category.go      # 分类规则服务
│   │   ├── site.go          # 站点管理服务
│   │   ├── download.go      # 下载服务
│   │   ├── discovery.go     # 发现页服务
│   │   ├── transfer.go      # 转移引擎
│   │   ├── strm.go          # STRM管理服务
│   │   ├── scraper.go       # 元数据刮削
│   │   ├── follow.go        # 追更服务
│   │   ├── user.go          # 用户服务
│   │   └── notify.go        # 通知服务 (Emby/Player)
│   ├── middleware/           # HTTP中间件
│   │   ├── auth.go          # JWT认证
│   │   ├── cors.go          # CORS
│   │   └── logger.go        # 请求日志
│   └── scheduler/           # 定时任务调度
│       ├── follow.go        # 追更定时任务
│       └── strm.go          # STRM定时同步
│
├── pkg/
│   ├── cloud/               # 网盘驱动抽象层
│   │   ├── driver.go        # 驱动接口定义
│   │   ├── registry.go      # 驱动注册
│   │   ├── aliyun/          # 阿里云盘
│   │   ├── pan115/          # 115网盘
│   │   ├── quark/           # 夸克网盘
│   │   ├── baidu/           # 百度网盘
│   │   ├── tianyi/          # 天翼云盘
│   │   ├── uc/              # UC网盘
│   │   ├── xunlei/          # 迅雷网盘
│   │   └── webdav/          # WebDAV通用
│   ├── mediaserver/         # 媒体服务器API客户端
│   │   ├── client.go        # 通用接口
│   │   ├── emby.go          # Emby REST API
│   │   └── jellyfin.go      # Jellyfin REST API
│   ├── downloader/          # 下载客户端抽象层
│   │   ├── client.go        # 下载器接口
│   │   ├── qbittorrent/     # qBittorrent API
│   │   └── transmission/    # Transmission API
│   ├── scraper/             # PT站点刮削器
│   │   ├── site.go          # 站点接口定义
│   │   ├── mteam/           # M-Team
│   │   ├── hdsky/           # HDSky
│   │   ├── ourbits/         # OurBits
│   │   └── ...
│   ├── metadata/            # 元数据刮削
│   │   ├── tmdb.go          # TMDB API
│   │   └── parser.go        # 文件名解析
│   ├── proxy/               # 302代理引擎
│   │   ├── engine.go        # 代理核心
│   │   └── cache.go         # URL缓存
│   └── strm/                # STRM文件生成
│       ├── generator.go     # 生成器
│       └── cleaner.go       # 无效STRM清理
│
├── api/
│   └── openapi.yaml         # OpenAPI 3.0 规范
├── configs/
│   └── config.example.yaml
├── docker/
│   ├── Dockerfile
│   └── docker-compose.yaml
├── scripts/
│   └── build.sh
├── go.mod
└── go.sum
```

## 4. 核心架构 — 三层设计

```
┌─────────────────────────────────────────────────────────────┐
│  ③ 分类规则 (Category Rules)                                 │
│  定义: 什么类型 → 去哪个存储目标                              │
│  电影 → 电影库(网盘)  |  剧集 → 剧集库(本地)  |  ...         │
│  含: 命名规则、目录模板、转移策略(硬链接/移动/复制)           │
├─────────────────────────────────────────────────────────────┤
│  ② 存储目标 (Storage Destinations)                           │
│  定义: 文件最终存放的位置                                     │
│  电影库 → Alist:/media/movies  (网盘, 开启STRM)              │
│  剧集库 → /nas/disk1/tv        (本地)                        │
│  纪录片 → 115:/docs            (网盘, 开启STRM)              │
│  含: 网盘目标可开启STRM → 配置STRM本地路径/策略              │
├─────────────────────────────────────────────────────────────┤
│  ① 连接管理 (Connections)                                    │
│  定义: 纯粹的"我能连上这个服务"                               │
│  Emby: URL+APIKey  |  Alist: URL+认证  |  115: Cookie  | ... │
│  含: 连接测试、状态监控、配额查询                             │
└─────────────────────────────────────────────────────────────┘
```

**数据流闭环**：

```
发现页(聚合搜索) ──→ 选择下载/追更
       │
       ▼
下载器(qBit/Transmission) 下载到本地下载目录
       │
       ▼ 下载完成触发
分类规则 判断媒体类型 → 找到对应存储目标
       │
       ├──→ 目标是本地 → 硬链接/移动/复制到本地目录
       │
       └──→ 目标是网盘 → 上传到网盘目录
              │
              └──→ 开启了STRM？ → 生成STRM到指定本地目录
       │
       ▼
通知 Emby/Jellyfin 刷新媒体库 (REST API)
       │
       ▼
通知 Player 客户端刷新 (WebSocket)
       │
       ▼
Player 展示新媒体
```

## 5. 连接管理 (Connections)

### 5.1 Emby/Jellyfin 连接

通过 Emby/Jellyfin 原生 REST API 管理：

```go
// pkg/mediaserver/client.go

package mediaserver

type MediaServerClient interface {
    // 测试连接
    TestConnection(ctx context.Context) error
    // 获取系统信息
    GetSystemInfo(ctx context.Context) (*SystemInfo, error)
    // 触发媒体库扫描
    RefreshLibrary(ctx context.Context, libraryID string) error
    // 获取媒体库列表
    GetLibraries(ctx context.Context) ([]*Library, error)
    // 获取媒体项目
    GetItems(ctx context.Context, libraryID string, query ItemQuery) ([]*Item, error)
    // 搜索媒体
    Search(ctx context.Context, keyword string) ([]*Item, error)
}
```

```go
// pkg/mediaserver/emby.go

type EmbyClient struct {
    baseURL string
    apiKey  string
    client  *http.Client
}

func (c *EmbyClient) TestConnection(ctx context.Context) error {
    resp, err := c.get(ctx, "/System/Info")
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return fmt.Errorf("emby connection failed: %d", resp.StatusCode)
    }
    return nil
}

func (c *EmbyClient) RefreshLibrary(ctx context.Context, libraryID string) error {
    _, err := c.post(ctx, fmt.Sprintf("/Items/%s/Refresh", libraryID), nil)
    return err
}
```

**连接配置**：

```yaml
# 连接管理中的 Emby 配置
name: "家庭Emby"
type: emby                    # emby / jellyfin
url: "http://nas:8096"
api_key: "xxxxxxxxxxxxxxx"
# 自动刷新: 转移完成后自动调用 RefreshLibrary
auto_refresh: true
```

### 5.2 网盘/Alist/CloudDrive2 连接

```go
// pkg/cloud/driver.go

package cloud

type Driver interface {
    Name() string
    Init(config map[string]string) error
    List(ctx context.Context, path string) ([]*File, error)
    Get(ctx context.Context, path string) (*File, error)
    Upload(ctx context.Context, localPath string, remotePath string) error
    GetDownloadURL(ctx context.Context, path string) (*DownloadURL, error)
    Search(ctx context.Context, keyword string) ([]*File, error)
    Delete(ctx context.Context, path string) error
    IsAlive(ctx context.Context) bool
    GetQuota(ctx context.Context) (*Quota, error)
}

type File struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    Path      string    `json:"path"`
    Size      int64     `json:"size"`
    IsDir     bool      `json:"is_dir"`
    MimeType  string    `json:"mime_type"`
    Hash      string    `json:"hash"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type DownloadURL struct {
    URL       string            `json:"url"`
    Headers   map[string]string `json:"headers"`
    ExpiresAt time.Time         `json:"expires_at"`
}
```

**Alist 连接配置**：

```yaml
name: "NAS Alist"
type: alist
url: "http://nas:5244"
username: "admin"
password: "xxx"
```

**115网盘连接配置**：

```yaml
name: "115网盘"
type: "115"
# 115使用Cookie认证
cookie: "xxxx"
# 115需要代理API（社区维护的115 API服务）
api_proxy: ""
```

### 5.3 连接状态管理

```
┌──────────────────────────────────────────────────────────┐
│ 连接管理                                                  │
├──────────┬──────────┬──────────┬──────────┬──────────────┤
│ 名称     │ 类型     │ 状态     │ 配额     │ 操作         │
├──────────┼──────────┼──────────┼──────────┼──────────────┤
│ 家庭Emby │ Emby     │ ● 在线   │ —        │ 测试 │ 编辑  │
│ NAS Alist│ Alist    │ ● 在线   │ 1.2T/2T  │ 测试 │ 编辑  │
│ 115网盘  │ 115      │ ● 在线   │ 8T/15T   │ 测试 │ 编辑  │
│ CloudDrv │ CD2      │ ○ 离线   │ —        │ 测试 │ 编辑  │
└──────────┴──────────┴──────────┴──────────┴──────────────┘
```

## 6. 存储目标 (Storage Destinations)

存储目标定义了"文件最终放在哪里"。每条记录对应一个物理存储位置。

### 6.1 存储目标模型

```go
type StorageDestination struct {
    ID           int64  `json:"id"`
    Name         string `json:"name"`          // 显示名称: "电影库", "剧集库"
    Type         string `json:"type"`          // "local" / "cloud"
    ConnectionID int64  `json:"connection_id"` // 关联的连接 (网盘类型必填)
    RemotePath   string `json:"remote_path"`   // 网盘路径 (网盘类型) 或 本地路径 (本地类型)

    // STRM 配置 (仅网盘类型可开启)
    StrmEnabled    bool   `json:"strm_enabled"`      // 是否开启STRM生成
    StrmOutputPath string `json:"strm_output_path"`  // STRM文件输出目录
    StrmBaseURL    string `json:"strm_base_url"`     // STRM中的代理URL前缀
}
```

### 6.2 存储目标示例

```
┌──────────────────────────────────────────────────────────────────────┐
│ 存储目标管理                                                         │
├──────────┬────────┬──────────┬─────────────────┬─────────────────────┤
│ 名称     │ 类型   │ 关联连接 │ 路径            │ STRM配置            │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 电影库   │ 网盘   │ Alist    │ /media/movies   │ ● 开启              │
│          │        │          │                 │ 输出: /strm/movies  │
│          │        │          │                 │ 代理: http://s:3000 │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 剧集库   │ 网盘   │ 115      │ /tv             │ ● 开启              │
│          │        │          │                 │ 输出: /strm/tv      │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 纪录片   │ 本地   │ —        │ /nas/disk1/docs │ — (本地无需STRM)    │
├──────────┼────────┼──────────┼─────────────────┼─────────────────────┤
│ 综艺     │ 网盘   │ Alist    │ /media/variety  │ ○ 关闭              │
└──────────┴────────┴──────────┴─────────────────┴─────────────────────┘
```

### 6.3 STRM 管理器

STRM 管理有独立的管理页面，提供定时任务配置：

```
┌────────────────────────────────────────────────────────────┐
│ STRM 管理                                                  │
├────────────────────────────────────────────────────────────┤
│                                                            │
│  定时任务配置                                               │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ 增量同步: 每 30 分钟    [0 */30 * * *]               │  │
│  │ 全量扫描: 每天 03:00    [0 3 * * *]                  │  │
│  │ 无效清理: 每周日 04:00  [0 4 * * 0]                  │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                            │
│  同步状态                                                   │
│  ┌──────────┬────────┬────────┬────────┬────────────────┐  │
│  │ 目标     │ 总文件 │ 已同步 │ 待同步 │ 上次同步       │  │
│  ├──────────┼────────┼────────┼────────┼────────────────┤  │
│  │ 电影库   │ 1,234  │ 1,230  │ 4      │ 5分钟前        │  │
│  │ 剧集库   │ 5,678  │ 5,678  │ 0      │ 2小时前        │  │
│  └──────────┴────────┴────────┴────────┴────────────────┘  │
│                                                            │
│  操作: [立即增量同步] [立即全量扫描] [清理无效STRM]         │
└────────────────────────────────────────────────────────────┘
```

**STRM 生成逻辑**：

```go
// pkg/strm/generator.go

package strm

type Generator struct {
    driver      cloud.Driver
    baseURL     string // Server代理URL前缀
    outputPath  string // STRM输出目录
}

// IncrementalSync 增量同步 — 只处理新增/修改的文件
func (g *Generator) IncrementalSync(ctx context.Context, remotePath string, lastSync time.Time) error {
    files, err := g.listMediaFiles(ctx, remotePath)
    if err != nil {
        return err
    }

    for _, file := range files {
        // 跳过未修改的文件
        if file.UpdatedAt.Before(lastSync) {
            continue
        }

        if err := g.generateOne(file); err != nil {
            log.Warn().Err(err).Str("file", file.Path).Msg("STRM生成失败")
            continue
        }
    }
    return nil
}

// FullSync 全量扫描 — 重新生成所有STRM
func (g *Generator) FullSync(ctx context.Context, remotePath string) error {
    files, err := g.listMediaFiles(ctx, remotePath)
    if err != nil {
        return err
    }

    for _, file := range files {
        if err := g.generateOne(file); err != nil {
            log.Warn().Err(err).Str("file", file.Path).Msg("STRM生成失败")
            continue
        }
    }
    return nil
}

// CleanInvalid 清理无效STRM — 删除指向不存在文件的STRM
func (g *Generator) CleanInvalid(ctx context.Context) (int, error) {
    cleaned := 0
    err := filepath.Walk(g.outputPath, func(path string, info os.FileInfo, err error) error {
        if filepath.Ext(path) != ".strm" {
            return nil
        }

        content, err := os.ReadFile(path)
        if err != nil {
            return nil
        }
        remotePath := g.parseSTRMPath(string(content))

        // 检查远端文件是否存在
        _, err = g.driver.Get(ctx, remotePath)
        if err != nil {
            os.Remove(path)
            cleaned++
        }
        return nil
    })
    return cleaned, err
}

// generateOne 生成单个STRM文件 + NFO + 海报
func (g *Generator) generateOne(file *cloud.File) error {
    // 解析文件名获取媒体信息
    info := parseMediaFilename(file.Name)

    // 构建本地目录结构
    localDir := g.buildOutputPath(info)
    os.MkdirAll(localDir, 0755)

    // 生成STRM文件 (内容为302代理URL)
    strmURL := fmt.Sprintf("%s/proxy/%s/%s", g.baseURL, g.driver.Name(), strings.TrimPrefix(file.Path, "/"))
    strmPath := filepath.Join(localDir, info.FileName+".strm")
    os.WriteFile(strmPath, []byte(strmURL), 0644)

    return nil
}
```

**生成的目录结构**：

```
/strm/movies/
  Inception (2010)/
    Inception (2010).strm      → http://server:3000/proxy/alist/media/movies/Inception.2010.mkv
    Inception (2010).nfo       → TMDB元数据
    poster.jpg                 → 海报
    fanart.jpg                 → 背景图
    Inception (2010).zh.srt    → 中文字幕(如果有)
```

## 7. 分类规则 (Category Rules)

分类规则定义了"下载完成后，这个文件属于什么类型，应该放到哪个存储目标"。

### 7.1 分类规则模型

```go
type CategoryRule struct {
    ID              int64  `json:"id"`
    Name            string `json:"name"`             // 规则名称: "电影", "国产剧", "纪录片"
    MediaType       string `json:"media_type"`       // "movie" / "tv" / "documentary" / "variety"
    DestinationID   int64  `json:"destination_id"`   // 关联的存储目标
    TransferMode    string `json:"transfer_mode"`    // "move" / "hardlink" / "copy" / "symlink"

    // 目录结构模板
    // 电影: "{title} ({year})/{title} ({year})"
    // 剧集: "{title} ({year})/Season {season:02d}/{title} S{season:02d}E{episode:02d}"
    DirTemplate     string `json:"dir_template"`

    // 命名规则模板
    // 电影: "{title} ({year}) - {resolution}"
    // 剧集: "{title} S{season:02d}E{episode:02d}"
    NamingTemplate  string `json:"naming_template"`

    // 自动匹配规则 (JSON)
    // 用于判断下载的文件属于哪个分类
    MatchRules      string `json:"match_rules"`      // {"category": ["Movie"], "keywords": []}
}
```

### 7.2 分类规则示例

```
┌──────────────────────────────────────────────────────────────────────────┐
│ 分类规则管理                                                             │
├──────────┬────────┬──────────┬──────────┬────────────────────────────────┤
│ 名称     │ 类型   │ 存储目标 │ 转移策略 │ 目录模板                       │
├──────────┼────────┼──────────┼──────────┼────────────────────────────────┤
│ 电影     │ movie  │ 电影库   │ 移动     │ {title} ({year})               │
│ 国产剧   │ tv     │ 剧集库   │ 移动     │ {title} ({year})/Season {S}    │
│ 纪录片   │ doc    │ 纪录片库 │ 硬链接   │ {title} ({year})               │
│ 综艺     │ var    │ 综艺库   │ 移动     │ {title}/Season {S}             │
└──────────┴────────┴──────────┴──────────┴────────────────────────────────┘
```

### 7.3 自动分类匹配

下载完成后，系统根据以下信息自动判断媒体类型：

1. **站点分类** — PT站点返回的种子分类 (Movie/TV/Documentary等)
2. **文件名解析** — parse-torrent-name 解析出 season/episode → 剧集，否则 → 电影
3. **TMDB 查询** — 通过标题查询TMDB，返回的 media_type 确认最终分类

```go
func (s *CategoryService) AutoClassify(torrent *Torrent, parsed *ParsedFilename) *CategoryRule {
    // 1. 优先用站点分类
    if rule := s.matchBySiteCategory(torrent.Category); rule != nil {
        return rule
    }

    // 2. 文件名解析判断
    if parsed.Season > 0 {
        return s.getRuleByMediaType("tv")
    }

    // 3. TMDB 查询确认
    tmdbResult, _ := s.tmdb.Search(parsed.Title, parsed.Year)
    if tmdbResult != nil && tmdbResult.MediaType == "tv" {
        return s.getRuleByMediaType("tv")
    }

    // 4. 默认归为电影
    return s.getRuleByMediaType("movie")
}
```

## 8. 站点管理 (PT Sites)

### 8.1 站点接口

```go
// pkg/scraper/site.go

type Site interface {
    Name() string
    Init(config SiteConfig) error
    Search(ctx context.Context, req *SearchRequest) ([]*Torrent, error)
    GetDetail(ctx context.Context, torrentURL string) (*TorrentDetail, error)
    GetCategories() []Category
}

type SiteConfig struct {
    Cookie    string `json:"cookie"`
    Passkey   string `json:"passkey"`
    UserID    string `json:"user_id"`
    BaseURL   string `json:"base_url"`
    UserAgent string `json:"user_agent"`
}

type SearchRequest struct {
    Keyword   string `json:"keyword"`    // 关键词搜索
    IMDBID    string `json:"imdb_id"`    // IMDB ID搜索
    DoubanID  string `json:"douban_id"`  // 豆瓣ID搜索
    Category  string `json:"category"`   // 分类过滤
    SortBy    string `json:"sort_by"`    // 排序: size/seeders/upload_time
    PageSize  int    `json:"page_size"`
    Page      int    `json:"page"`
}

type Torrent struct {
    SiteName    string    `json:"site_name"`
    Title       string    `json:"title"`
    IMDBID      string    `json:"imdb_id"`
    Size        int64     `json:"size"`
    Seeders     int       `json:"seeders"`
    Leechers    int       `json:"leechers"`
    DownloadURL string    `json:"download_url"`
    DetailURL   string    `json:"detail_url"`
    Category    string    `json:"category"`     // Movie/TV/Documentary
    UploadTime  time.Time `json:"upload_time"`
    Team        string    `json:"team"`          // 制作组
    Resolution  string    `json:"resolution"`    // 2160p/1080p/720p
    Codec       string    `json:"codec"`         // H265/AV1
    Source      string    `json:"source"`        // BluRay/WEB-DL
    Tags        []string  `json:"tags"`          // 标签: 中字, 国语, HDR...
}
```

### 8.2 站点管理 UI

```
┌──────────────────────────────────────────────────────────────┐
│ 站点管理                                                     │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────┬──────────┬────────┬────────┬────────────────┐  │
│  │ 站点     │ 状态     │ 用户   │ 上传量 │ 操作           │  │
│  ├──────────┼──────────┼────────┼────────┼────────────────┤  │
│  │ M-Team   │ ● 正常   │ VIP    │ 12.5TB │ 测试│编辑│日志 │  │
│  │ HDSky    │ ● 正常   │ PU     │ 3.2TB  │ 测试│编辑│日志 │  │
│  │ OurBits  │ ○ 过期   │ —      │ —      │ 测试│编辑│日志 │  │
│  └──────────┴──────────┴────────┴────────┴────────────────┘  │
│                                                              │
│  [+ 添加站点]  [批量导入]                                     │
└──────────────────────────────────────────────────────────────┘
```

## 9. 下载器管理 (Download Clients)

### 9.1 下载器接口

```go
// pkg/downloader/client.go

type DownloadClient interface {
    Name() string
    AddTorrent(ctx context.Context, req *AddRequest) (*Task, error)
    AddURL(ctx context.Context, url string, savePath string) (*Task, error)
    GetTask(ctx context.Context, taskID string) (*Task, error)
    ListTasks(ctx context.Context) ([]*Task, error)
    PauseTask(ctx context.Context, taskID string) error
    ResumeTask(ctx context.Context, taskID string) error
    DeleteTask(ctx context.Context, taskID string, deleteFiles bool) error
}

type AddRequest struct {
    TorrentURL string // 种子下载链接或磁力链接
    SavePath   string // 保存目录
    Category   string // 分类标签 (用于后续自动转移识别)
    Name       string // 任务名称
}

type Task struct {
    ID         string    `json:"id"`
    Name       string    `json:"name"`
    Status     string    `json:"status"`      // downloading/seeding/completed/paused/error
    Progress   float64   `json:"progress"`    // 0-100
    Size       int64     `json:"size"`
    Speed      int64     `json:"speed"`       // bytes/s
    ETA        int64     `json:"eta"`         // seconds
    SavePath   string    `json:"save_path"`
    Category   string    `json:"category"`
    CreatedAt  time.Time `json:"created_at"`
}
```

### 9.2 下载器配置

```yaml
# 下载器管理配置
download_clients:
  - name: "主下载器"
    type: qbittorrent           # qbittorrent / transmission
    url: "http://localhost:8080"
    username: admin
    password: ""
    # 下载目录 (种子下载到这里)
    download_path: "/downloads"
    # 是否默认下载器
    is_default: true
```

### 9.3 下载器管理 UI

```
┌──────────────────────────────────────────────────────────────┐
│ 下载器管理                                                   │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────┬──────────┬──────────┬────────────────────┐  │
│  │ 名称         │ 类型     │ 状态     │ 操作               │  │
│  ├──────────────┼──────────┼──────────┼────────────────────┤  │
│  │ 主下载器     │ qBit     │ ● 在线   │ 测试│编辑│任务列表 │  │
│  │ 备用下载器   │ Trans    │ ○ 离线   │ 测试│编辑│任务列表 │  │
│  └──────────────┴──────────┴──────────┴────────────────────┘  │
│                                                              │
│  [+ 添加下载器]                                               │
│                                                              │
│  配置:                                                       │
│  下载目录: /downloads                                        │
│  完成后默认操作: 移动到分类目录                                │
└──────────────────────────────────────────────────────────────┘
```

## 10. 发现页 (Discovery)

发现页是用户与系统交互的核心页面，聚合多个PT站点的资源。

### 10.1 聚合搜索

```go
// internal/services/discovery.go

type DiscoveryService struct {
    siteMgr    *SiteManager
    tmdb       *TmdbScraper
    downloader *DownloadService
    category   *CategoryService
}

// Search 聚合搜索 — 同时查询所有已配置站点
func (d *DiscoveryService) Search(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
    sites := d.siteMgr.GetActiveSites()

    // 并发搜索所有站点
    type siteResult struct {
        siteName string
        torrents []*Torrent
        err      error
    }

    ch := make(chan siteResult, len(sites))
    for _, site := range sites {
        go func(s Site) {
            torrents, err := s.Search(ctx, req)
            ch <- siteResult{s.Name(), torrents, err}
        }(site)
    }

    var allResults []*SearchResult
    for range sites {
        res := <-ch
        if res.err != nil {
            continue
        }
        for _, t := range res.torrents {
            allResults = append(allResults, &SearchResult{
                Torrent:  t,
                SiteName: res.siteName,
            })
        }
    }

    // 自动匹配TMDB元数据
    for _, r := range allResults {
        if r.Torrent.IMDBID != "" {
            r.TMDBInfo, _ = d.tmdb.GetByIMDBID(ctx, r.Torrent.IMDBID)
        } else {
            parsed := parseFilename(r.Torrent.Title)
            r.TMDBInfo, _ = d.tmdb.Search(parsed.Title, parsed.Year)
        }
    }

    // 按相关度排序
    sortResults(allResults, req)

    return allResults, nil
}
```

### 10.2 一键下载

```go
// 一键下载: 选择种子 → 自动分类 → 下载到正确目录
func (d *DiscoveryService) Download(ctx context.Context, userID int64, torrent *Torrent) (*DownloadTask, error) {
    // 1. 自动匹配媒体类型和分类规则
    parsed := parseFilename(torrent.Title)
    rule := d.category.AutoClassify(torrent, parsed)

    // 2. 确定下载目录
    dest, _ := d.category.GetDestination(rule.DestinationID)
    downloadPath := getDownloadPath(dest, rule)

    // 3. 提交到下载器
    task, err := d.downloader.AddTorrent(ctx, &AddRequest{
        TorrentURL: torrent.DownloadURL,
        SavePath:   downloadPath,
        Category:   rule.MediaType,
        Name:       torrent.Title,
    })

    // 4. 记录下载任务 (关联用户ID)
    d.saveDownloadTask(userID, task, torrent, rule)

    return task, nil
}
```

### 10.3 发现页 UI

```
┌──────────────────────────────────────────────────────────────────────────┐
│ 发现页                                                                   │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  搜索: [盗梦空间__________________________] [搜索]  筛选: [全部 ▼]        │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │ 盗梦空间 Inception (2010)                                          │  │
│  │ TMDB: 8.8/10 | 科幻/冒险/动作 | 诺兰 | 莱昂纳多                    │  │
│  │ ┌────────────────────────────────────────────────────────────────┐ │  │
│  │ │ 来源   │ 标题                        │ 大小    │ 做种 │ 操作  │ │  │
│  │ ├────────┼─────────────────────────────┼─────────┼──────┼───────┤ │  │
│  │ │ M-Team │ Inception.2010.2160p.UHD... │ 45.2GB  │ 128  │ ⬇下载 │ │  │
│  │ │ HDSky  │ 盗梦空间.2010.BluRay.1080p  │ 12.8GB  │ 56   │ ⬇下载 │ │  │
│  │ │ OurBits│ Inception.2010.1080p.x265   │ 8.5GB   │ 23   │ ⬇下载 │ │  │
│  │ └────────┴─────────────────────────────┴─────────┴──────┴───────┘ │  │
│  │                                                                    │  │
│  │ 星际穿越 Interstellar (2014)                                       │  │
│  │ TMDB: 8.7/10 | 科幻/冒险/剧情 | 诺兰 | 马修·麦康纳                 │  │
│  │ ...                                                                │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│                                                                          │
│  追更列表:                                                               │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────────────────┐   │
│  │ 剧名     │ 当前进度 │ 站点     │ 下次检查 │ 操作                 │   │
│  ├──────────┼──────────┼──────────┼──────────┼──────────────────────┤   │
│  │ 三体     │ S01E08   │ M-Team   │ 明天3:00 │ [暂停] [编辑] [删除] │   │
│  │ 庆余年2  │ S02E05   │ HDSky    │ 明天3:00 │ [暂停] [编辑] [删除] │   │
│  └──────────┴──────────┴──────────┴──────────┴──────────────────────┘   │
└──────────────────────────────────────────────────────────────────────────┘
```

### 10.4 追更 (Follow/Subscribe)

```go
// internal/services/follow.go

type FollowService struct {
    siteMgr    *SiteManager
    tmdb       *TmdbScraper
    downloader *DownloadService
    category   *CategoryService
    scheduler  *cron.Cron
}

// Follow 创建追更任务
func (f *FollowService) Follow(ctx context.Context, userID int64, req *FollowRequest) error {
    // 1. 查询TMDB获取剧集信息
    tmdbInfo, err := f.tmdb.GetDetail(ctx, req.TMDBID)
    if err != nil {
        return err
    }

    // 2. 创建追更记录
    follow := &FollowTask{
        UserID:       userID,
        TMDBID:       req.TMDBID,
        Title:        tmdbInfo.Title,
        IMDBID:       tmdbInfo.IMDBID,
        Season:       req.Season,
        TotalEpisodes: tmdbInfo.NumberOfEpisodes,
        // 匹配规则
        SiteFilter:   req.SiteFilter,    // 只在指定站点搜索
        QualityFilter: req.QualityFilter, // 质量偏好: "1080p以上, H265"
        GroupFilter:  req.GroupFilter,    // 制作组偏好
        // 搜索间隔
        CronExpr:     req.CronExpr,      // 默认每天3:00
        // 状态
        Status:       "active",
        LastEpisode:  req.StartEpisode,   // 从第几集开始追
    }

    return f.saveFollowTask(follow)
}

// ExecuteFollow 执行追更 — 定时调用
func (f *FollowService) ExecuteFollow(ctx context.Context, task *FollowTask) error {
    // 1. 在指定站点搜索剧名
    searchReq := &SearchRequest{
        IMDBID:   task.IMDBID,
        Keyword:  task.Title,
        Category: "TV",
    }

    sites := f.getSites(task.SiteFilter)
    var allTorrents []*Torrent
    for _, site := range sites {
        torrents, _ := site.Search(ctx, searchReq)
        allTorrents = append(allTorrents, torrents...)
    }

    // 2. 过滤: 只要缺少的集数
    missing := f.getMissingEpisodes(task)
    matched := f.filterTorrents(allTorrents, missing, task)

    // 3. 匹配质量偏好
    best := f.selectBest(matched, task.QualityFilter, task.GroupFilter)

    // 4. 下载
    for _, torrent := range best {
        rule := f.category.AutoClassify(torrent, parseFilename(torrent.Title))
        f.downloader.AddTorrent(ctx, &AddRequest{
            TorrentURL: torrent.DownloadURL,
            SavePath:   getDownloadPath(rule),
            Category:   "tv",
            Name:       torrent.Title,
        })
    }

    // 5. 更新追更状态
    task.LastCheck = time.Now()
    f.saveFollowTask(task)

    return nil
}

// getMissingEpisodes 获取缺少的集数
// 对比 TMDB 上的总集数和本地已有集数
func (f *FollowService) getMissingEpisodes(task *FollowTask) []int {
    // 查询本地已有集数 (从媒体库记录中获取)
    local := f.getExistingEpisodes(task.TMDBID, task.Season)

    // TMDB 上的总集数
    total := task.TotalEpisodes

    var missing []int
    for ep := 1; ep <= total; ep++ {
        if !local[ep] {
            missing = append(missing, ep)
        }
    }
    return missing
}
```

## 11. 文件转移引擎 (Transfer Engine)

下载完成后自动触发转移流程。

### 11.1 转移引擎

```go
// internal/services/transfer.go

type TransferService struct {
    category   *CategoryService
    metadata   *MetadataService
    strm       *STRMService
    notifier   *NotifyService
}

// OnDownloadComplete 下载完成回调 — 核心转移流程
func (t *TransferService) OnDownloadComplete(ctx context.Context, task *DownloadTask) error {
    // 1. 自动分类
    parsed := parseFilename(task.TorrentName)
    rule := t.category.AutoClassify(task.Torrent, parsed)

    // 2. 查询TMDB元数据
    tmdbInfo, _ := t.metadata.Search(parsed.Title, parsed.Year)

    // 3. 构建目标路径
    dest, _ := t.category.GetDestination(rule.DestinationID)
    targetPath := buildTargetPath(dest, rule, parsed, tmdbInfo)

    // 4. 执行转移 (根据策略)
    var err error
    switch rule.TransferMode {
    case "move":
        err = moveFile(task.SavePath, targetPath)
    case "hardlink":
        err = os.Link(task.SavePath, targetPath)
    case "copy":
        err = copyFile(task.SavePath, targetPath)
    case "symlink":
        err = os.Symlink(task.SavePath, targetPath)
    }
    if err != nil {
        return fmt.Errorf("transfer failed: %w", err)
    }

    // 5. 如果目标是网盘且开启了STRM → 生成STRM
    if dest.Type == "cloud" && dest.StrmEnabled {
        t.strm.GenerateOne(ctx, dest, targetPath)
    }

    // 6. 通知媒体服务器刷新
    t.notifier.RefreshMediaServers(ctx, dest)

    // 7. 通知Player客户端
    title := parsed.Title
    if tmdbInfo != nil {
        title = tmdbInfo.Title
    }
    t.notifier.NotifyPlayer(ctx, &PlayerNotification{
        Type:      "media_added",
        Title:     title,
        MediaType: rule.MediaType,
    })

    return nil
}

// buildTargetPath 构建目标路径
// 电影: /movies/Inception (2010)/Inception (2010).mkv
// 剧集: /tv/三体 (2023)/Season 01/三体 S01E08.mkv
func buildTargetPath(dest *StorageDestination, rule *CategoryRule, parsed *ParsedFilename, tmdb *TmdbResult) string {
    title := parsed.Title
    year := parsed.Year
    if tmdb != nil {
        title = tmdb.Title
        year = tmdb.Year
    }

    dirName := rule.DirTemplate
    dirName = strings.ReplaceAll(dirName, "{title}", title)
    dirName = strings.ReplaceAll(dirName, "{year}", fmt.Sprintf("%d", year))
    dirName = strings.ReplaceAll(dirName, "{season:02d}", fmt.Sprintf("%02d", parsed.Season))

    fileName := rule.NamingTemplate
    fileName = strings.ReplaceAll(fileName, "{title}", title)
    fileName = strings.ReplaceAll(fileName, "{year}", fmt.Sprintf("%d", year))
    fileName = strings.ReplaceAll(fileName, "{season:02d}", fmt.Sprintf("%02d", parsed.Season))
    fileName = strings.ReplaceAll(fileName, "{episode:02d}", fmt.Sprintf("%02d", parsed.Episode))
    fileName = strings.ReplaceAll(fileName, "{resolution}", parsed.Resolution)
    fileName += filepath.Ext(parsed.FileName) // 保留原始扩展名

    return filepath.Join(dest.RemotePath, dirName, fileName)
}
```

### 11.2 转移流程图

```
下载完成
  │
  ▼
自动分类 (站点分类 + 文件名解析 + TMDB查询)
  │
  ▼
查询TMDB元数据 (标题、年份、海报、简介)
  │
  ▼
构建目标路径 (根据分类规则的模板)
  │
  ▼
执行转移策略
  │
  ├── move (默认) ──→ 移动文件到目标目录
  │                    原下载目录清空
  │
  ├── hardlink ──→ 创建硬链接
  │                 保留原文件 (保种需求)
  │
  ├── copy ──→ 复制文件
  │             保留原文件
  │
  └── symlink ──→ 创建软链接
  │
  ▼
目标是网盘 + 开启STRM？
  │
  ├── 是 → 生成STRM文件到指定目录 + NFO + 海报
  │
  └── 否 → 跳过
  │
  ▼
通知 Emby/Jellyfin 刷新媒体库 (REST API)
  │
  ▼
通知 Player 客户端 (WebSocket)
```

## 12. 302代理引擎 (302 Proxy)

播放网盘上的STRM文件时，302代理将请求重定向到云盘CDN：

```go
// pkg/proxy/engine.go

type Engine struct {
    drivers map[string]cloud.Driver
    cache   *URLCache
    logger  *zerolog.Logger
}

func (e *Engine) HandlePlayback(w http.ResponseWriter, r *http.Request) {
    driverName, filePath := parsePlaybackPath(r.URL.Path)

    driver, ok := e.drivers[driverName]
    if !ok {
        http.Error(w, "Driver not found", http.StatusNotFound)
        return
    }

    // 检查缓存
    cacheKey := driverName + ":" + filePath
    if cached, ok := e.cache.Get(cacheKey); ok {
        http.Redirect(w, r, cached.URL, http.StatusFound)
        return
    }

    // 获取真实下载URL
    downloadURL, err := driver.GetDownloadURL(r.Context(), filePath)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // 缓存URL
    e.cache.Set(cacheKey, downloadURL, downloadURL.ExpiresAt)

    // 302重定向
    for key, value := range downloadURL.Headers {
        w.Header().Set(key, value)
    }
    http.Redirect(w, r, downloadURL.URL, http.StatusFound)
}
```

**302播放流程**：

```
Emby/Jellyfin 扫描STRM库
  │
  ▼ 播放请求
GET /proxy/alist/media/movies/Inception.2010.mkv
  │
  ▼
OhMyCine Server (302 Proxy)
  │ 1. 查找alist驱动
  │ 2. 调用 GetDownloadURL()
  │ 3. 获取真实CDN URL
  │ 4. 缓存URL
  │
  ▼ HTTP 302 Found
  Location: https://cdn.example.com/real-url?token=xxx
  │
  ▼
客户端直接从CDN串流 (不经Server，零带宽消耗)
```

## 13. 用户管理

### 13.1 用户模型

```go
type User struct {
    ID           int64  `json:"id"`
    Username     string `json:"username"`
    PasswordHash string `json:"-"`              // bcrypt哈希
    Role         string `json:"role"`           // "admin" / "user"
    Permissions  string `json:"permissions"`    // JSON: 可访问的页面列表
    CreatedAt    time.Time `json:"created_at"`
}
```

### 13.2 权限设计

- **管理员** — 可以看到所有页面、所有下载任务、所有追更任务
- **普通用户** — 共享媒体库，但只能看到自己创建的下载/追更任务

```go
// 权限检查
func (s *UserService) CanAccess(user *User, page string) bool {
    if user.Role == "admin" {
        return true
    }
    var perms []string
    json.Unmarshal([]byte(user.Permissions), &perms)
    return contains(perms, page)
}

// 任务可见性
func (s *DownloadService) ListTasks(user *User) []*DownloadTask {
    if user.Role == "admin" {
        return s.getAllTasks()
    }
    return s.getTasksByUser(user.ID)
}
```

### 13.3 用户管理 UI

```
┌──────────────────────────────────────────────────────────────────┐
│ 用户管理                                                         │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────┬────────┬──────────────────────────┬──────────────┐ │
│  │ 用户名   │ 角色   │ 可访问页面               │ 操作         │ │
│  ├──────────┼────────┼──────────────────────────┼──────────────┤ │
│  │ admin    │ 管理员 │ 全部                     │ 编辑         │ │
│  │ 张三     │ 用户   │ 发现页,媒体库,设置       │ 编辑│删除    │ │
│  │ 李四     │ 用户   │ 媒体库                   │ 编辑│删除    │ │
│  └──────────┴────────┴──────────────────────────┴──────────────┘ │
│                                                                  │
│  [+ 添加用户]                                                    │
└──────────────────────────────────────────────────────────────────┘
```

## 14. 文件管理

文件管理页面让用户浏览和管理各个连接中的文件。

```
┌──────────────────────────────────────────────────────────────────┐
│ 文件管理                                                         │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  数据源: [Alist ▼]                                               │
│                                                                  │
│  /media/movies/                                                  │
│  ├── Inception (2010)/                                           │
│  │   ├── Inception.2010.2160p.UHD.BluRay.x265.mkv  45.2GB      │
│  │   ├── Inception.2010.zh.srt                      128KB      │
│  │   └── poster.jpg                             2.1MB          │
│  ├── Interstellar (2014)/                                        │
│  │   └── ...                                                    │
│  └── ...                                                         │
│                                                                  │
│  操作: [上传] [新建文件夹] [刷新] [返回上级]                      │
└──────────────────────────────────────────────────────────────────┘
```

## 15. 系统设置

### 15.1 设置页面结构

```
┌──────────────────────────────────────────────────────────────────┐
│ 设置                                                             │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  基础设置                                                        │
│  ├─ 服务器: 端口/主机/HTTPS                                      │
│  ├─ 数据库: SQLite/PostgreSQL                                    │
│  └─ 日志: 级别/路径/轮转                                         │
│                                                                  │
│  元数据                                                          │
│  ├─ TMDB API Key: [________________________]                     │
│  ├─ 语言偏好: [中文 ▼]                                           │
│  └─ 图片质量: [原始 ▼]                                           │
│                                                                  │
│  AI 助手                                                         │
│  ├─ Provider: [OpenAI ▼]                                         │
│  ├─ API Key: [________________________]                          │
│  ├─ Model: [gpt-4o ▼]                                           │
│  └─ Base URL: [https://api.openai.com] (可选自定义)              │
│                                                                  │
│  302代理                                                         │
│  ├─ 监听端口: [3000]                                             │
│  ├─ URL缓存时间: [30分钟]                                        │
│  └─ CORS: [✓] 启用                                              │
│                                                                  │
│  [保存设置]                                                      │
└──────────────────────────────────────────────────────────────────┘
```

## 16. REST API 设计

### 16.1 API 端点总览

```yaml
# ====== 认证 ======
POST   /api/v1/auth/login                    # 登录 (返回JWT)
POST   /api/v1/auth/logout                   # 登出
GET    /api/v1/auth/me                       # 当前用户信息

# ====== 连接管理 ======
GET    /api/v1/connections                   # 连接列表
POST   /api/v1/connections                   # 添加连接
PUT    /api/v1/connections/{id}              # 更新连接
DELETE /api/v1/connections/{id}              # 删除连接
POST   /api/v1/connections/{id}/test         # 测试连接
GET    /api/v1/connections/{id}/quota        # 获取配额 (网盘)

# ====== 存储目标 ======
GET    /api/v1/destinations                  # 存储目标列表
POST   /api/v1/destinations                  # 添加存储目标
PUT    /api/v1/destinations/{id}             # 更新存储目标
DELETE /api/v1/destinations/{id}             # 删除存储目标

# ====== 分类规则 ======
GET    /api/v1/categories                    # 分类规则列表
POST   /api/v1/categories                    # 添加分类规则
PUT    /api/v1/categories/{id}              # 更新分类规则
DELETE /api/v1/categories/{id}              # 删除分类规则

# ====== 站点管理 ======
GET    /api/v1/sites                         # 站点列表
POST   /api/v1/sites                         # 添加站点
PUT    /api/v1/sites/{id}                    # 更新站点
DELETE /api/v1/sites/{id}                    # 删除站点
POST   /api/v1/sites/{id}/test               # 测试站点连接
GET    /api/v1/sites/{id}/categories         # 获取站点分类

# ====== 下载器管理 ======
GET    /api/v1/downloaders                   # 下载器列表
POST   /api/v1/downloaders                   # 添加下载器
PUT    /api/v1/downloaders/{id}             # 更新下载器
DELETE /api/v1/downloaders/{id}             # 删除下载器
POST   /api/v1/downloaders/{id}/test        # 测试下载器连接

# ====== 发现页 ======
POST   /api/v1/discovery/search              # 聚合搜索
POST   /api/v1/discovery/download            # 一键下载
GET    /api/v1/discovery/trending            # 热门资源
GET    /api/v1/discovery/latest              # 最新资源

# ====== 追更 ======
GET    /api/v1/follows                       # 追更列表 (用户隔离)
POST   /api/v1/follows                       # 创建追更
PUT    /api/v1/follows/{id}                  # 更新追更
DELETE /api/v1/follows/{id}                  # 删除追更
POST   /api/v1/follows/{id}/pause            # 暂停追更
POST   /api/v1/follows/{id}/resume           # 恢复追更
POST   /api/v1/follows/{id}/execute          # 立即执行追更

# ====== 下载任务 ======
GET    /api/v1/downloads                     # 下载任务列表 (用户隔离)
POST   /api/v1/downloads                     # 添加下载任务
GET    /api/v1/downloads/{id}                # 任务详情
DELETE /api/v1/downloads/{id}                # 删除任务
POST   /api/v1/downloads/{id}/pause          # 暂停
POST   /api/v1/downloads/{id}/resume         # 恢复

# ====== 转移任务 ======
GET    /api/v1/transfers                     # 转移任务列表
GET    /api/v1/transfers/{id}                # 转移任务详情
POST   /api/v1/transfers/{id}/retry          # 重试失败的转移

# ====== STRM管理 ======
GET    /api/v1/strm/status                   # STRM同步状态
POST   /api/v1/strm/sync/incremental         # 立即增量同步
POST   /api/v1/strm/sync/full                # 立即全量同步
POST   /api/v1/strm/clean                    # 清理无效STRM
GET    /api/v1/strm/config                   # STRM定时任务配置
PUT    /api/v1/strm/config                   # 更新STRM定时任务配置

# ====== 302代理 (非REST，播放用) ======
GET    /proxy/{driver}/{path...}             # 302重定向播放

# ====== 元数据 ======
POST   /api/v1/metadata/search               # 搜索元数据
POST   /api/v1/metadata/match                # 自动匹配
GET    /api/v1/metadata/{tmdb_id}            # 获取元数据

# ====== 媒体库 ======
GET    /api/v1/media                         # 媒体列表
GET    /api/v1/media/{id}                    # 媒体详情
PUT    /api/v1/media/{id}                    # 更新媒体信息
DELETE /api/v1/media/{id}                    # 删除媒体

# ====== 文件管理 ======
GET    /api/v1/files/{connection_id}/list    # 浏览文件
POST   /api/v1/files/{connection_id}/upload  # 上传文件
DELETE /api/v1/files/{connection_id}/delete  # 删除文件

# ====== 用户管理 ======
GET    /api/v1/users                         # 用户列表 (管理员)
POST   /api/v1/users                         # 添加用户 (管理员)
PUT    /api/v1/users/{id}                    # 更新用户
DELETE /api/v1/users/{id}                    # 删除用户 (管理员)

# ====== 系统设置 ======
GET    /api/v1/settings                      # 获取设置
PUT    /api/v1/settings                      # 更新设置
GET    /api/v1/settings/tmdb/test            # 测试TMDB连接

# ====== 配置同步 (Player ↔ Server) ======
POST   /api/v1/sync/push                     # Player推送数据源配置
GET    /api/v1/sync/pull                     # Player拉取Server配置
GET    /api/v1/sync/status                   # 同步状态

# ====== WebSocket ======
WS     /ws/events                            # 实时事件推送
```

### 16.2 WebSocket 事件

```json
// 下载进度
{"type": "download.progress", "data": {"task_id": "xxx", "progress": 45.2, "speed": "12MB/s"}}

// 下载完成
{"type": "download.completed", "data": {"task_id": "xxx", "title": "Inception"}}

// 转移进度
{"type": "transfer.progress", "data": {"task_id": "xxx", "progress": 80.0}}

// 转移完成
{"type": "transfer.completed", "data": {"title": "Inception", "destination": "电影库"}}

// STRM同步进度
{"type": "strm.progress", "data": {"total": 1000, "current": 567}}

// 新媒体入库
{"type": "media.added", "data": {"id": "xxx", "title": "Inception", "type": "movie"}}

// 追更发现新集
{"type": "follow.new_episode", "data": {"title": "三体", "episode": "S01E09"}}

// 站点状态变化
{"type": "site.status_changed", "data": {"site": "mteam", "status": "connected"}}
```

## 17. 数据库设计

```sql
-- ========================================
-- 连接管理
-- ========================================

CREATE TABLE connections (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,                    -- 显示名称
    type        TEXT NOT NULL,                    -- emby/jellyfin/alist/clouddrive2/115/quark/...
    config      TEXT NOT NULL,                    -- JSON配置 (加密存储认证信息)
    status      TEXT DEFAULT 'unknown',           -- online/offline/error
    quota_total INTEGER DEFAULT 0,
    quota_used  INTEGER DEFAULT 0,
    last_check  DATETIME,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- 存储目标
-- ========================================

CREATE TABLE storage_destinations (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL,               -- "电影库", "剧集库"
    type             TEXT NOT NULL,               -- "local" / "cloud"
    connection_id    INTEGER,                     -- 关联的连接 (网盘类型必填)
    remote_path      TEXT NOT NULL,               -- 网盘路径或本地路径
    strm_enabled     BOOLEAN DEFAULT false,       -- 是否开启STRM生成
    strm_output_path TEXT,                         -- STRM文件输出目录
    strm_base_url    TEXT,                         -- STRM代理URL前缀
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (connection_id) REFERENCES connections(id)
);

-- ========================================
-- 分类规则
-- ========================================

CREATE TABLE category_rules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,                -- "电影", "国产剧"
    media_type      TEXT NOT NULL,                -- "movie"/"tv"/"documentary"/"variety"
    destination_id  INTEGER NOT NULL,             -- 关联的存储目标
    transfer_mode   TEXT DEFAULT 'move',          -- "move"/"hardlink"/"copy"/"symlink"
    dir_template    TEXT NOT NULL,                -- 目录模板
    naming_template TEXT NOT NULL,                -- 命名模板
    match_rules     TEXT,                         -- JSON: 自动匹配规则
    sort_order      INTEGER DEFAULT 0,            -- 排序 (匹配时从前往后)
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (destination_id) REFERENCES storage_destinations(id)
);

-- ========================================
-- PT站点
-- ========================================

CREATE TABLE sites (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    site_type   TEXT NOT NULL,                    -- 站点标识: mteam/hdsky/ourbits/...
    config      TEXT NOT NULL,                    -- JSON: Cookie/Passkey等 (加密)
    status      TEXT DEFAULT 'unknown',           -- online/offline/expired
    user_info   TEXT,                             -- JSON: 用户等级/上传量等
    last_check  DATETIME,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- 下载器
-- ========================================

CREATE TABLE downloaders (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    type          TEXT NOT NULL,                  -- qbittorrent/transmission
    config        TEXT NOT NULL,                  -- JSON: URL/用户名/密码
    download_path TEXT NOT NULL,                  -- 下载目录
    is_default    BOOLEAN DEFAULT false,
    status        TEXT DEFAULT 'unknown',
    last_check    DATETIME,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- 下载任务
-- ========================================

CREATE TABLE download_tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,             -- 创建者
    site_id         INTEGER,
    downloader_id   INTEGER,                      -- 使用的下载器
    torrent_name    TEXT,
    torrent_url     TEXT,
    imdb_id         TEXT,                          -- IMDB ID (用于匹配)
    tmdb_id         INTEGER,                      -- TMDB ID
    save_path       TEXT,                          -- 下载目录
    status          TEXT DEFAULT 'pending',        -- pending/downloading/seeding/completed/failed/transferring
    progress        REAL DEFAULT 0,
    size            INTEGER DEFAULT 0,
    speed           INTEGER DEFAULT 0,
    client_task_id  TEXT,                          -- 下载器中的任务ID
    category_rule_id INTEGER,                     -- 匹配到的分类规则
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id),
    FOREIGN KEY (site_id) REFERENCES sites(id),
    FOREIGN KEY (downloader_id) REFERENCES downloaders(id)
);

-- ========================================
-- 转移任务
-- ========================================

CREATE TABLE transfer_tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    download_task_id INTEGER,                     -- 关联的下载任务
    source_path     TEXT NOT NULL,                 -- 源文件路径
    target_path     TEXT NOT NULL,                 -- 目标文件路径
    transfer_mode   TEXT NOT NULL,                 -- move/hardlink/copy/symlink
    status          TEXT DEFAULT 'pending',        -- pending/transferring/completed/failed
    error_message   TEXT,                          -- 失败原因
    destination_id  INTEGER,                       -- 关联的存储目标
    strm_generated  BOOLEAN DEFAULT false,         -- 是否已生成STRM
    emby_notified   BOOLEAN DEFAULT false,         -- 是否已通知Emby
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (download_task_id) REFERENCES download_tasks(id),
    FOREIGN KEY (destination_id) REFERENCES storage_destinations(id)
);

-- ========================================
-- 追更任务
-- ========================================

CREATE TABLE follow_tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL,             -- 创建者
    tmdb_id         INTEGER NOT NULL,
    title           TEXT NOT NULL,
    imdb_id         TEXT,
    season          INTEGER DEFAULT 1,
    total_episodes  INTEGER,
    last_episode    INTEGER DEFAULT 0,            -- 已追到第几集
    site_filter     TEXT,                         -- JSON: 指定站点列表
    quality_filter  TEXT,                         -- 质量偏好: "1080p+, H265"
    group_filter    TEXT,                         -- 制作组偏好
    cron_expr       TEXT DEFAULT '0 3 * * *',     -- 默认每天3:00
    status          TEXT DEFAULT 'active',        -- active/paused/completed
    last_check      DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- ========================================
-- 媒体库
-- ========================================

CREATE TABLE media (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title           TEXT NOT NULL,
    original_title  TEXT,
    year            INTEGER,
    type            TEXT NOT NULL,                -- movie/series/episode
    imdb_id         TEXT,
    tmdb_id         INTEGER,
    douban_id       TEXT,
    overview        TEXT,
    rating          REAL,
    genres          TEXT,                         -- JSON数组
    directors       TEXT,                         -- JSON数组
    cast_list       TEXT,                         -- JSON数组
    poster_url      TEXT,
    fanart_url      TEXT,
    local_path      TEXT,                         -- 本地文件路径 (转移后的目标路径)
    strm_path       TEXT,                         -- STRM文件路径 (网盘类型)
    cloud_path      TEXT,                         -- 云盘原始路径
    destination_id  INTEGER,                      -- 关联的存储目标
    status          TEXT DEFAULT 'active',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (destination_id) REFERENCES storage_destinations(id)
);

-- ========================================
-- 用户
-- ========================================

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT DEFAULT 'user',            -- admin/user
    permissions   TEXT,                           -- JSON: 可访问的页面列表
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- ========================================
-- STRM定时任务配置
-- ========================================

CREATE TABLE strm_schedules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    destination_id  INTEGER NOT NULL,
    incremental_cron TEXT DEFAULT '*/30 * * * *', -- 增量同步频率
    full_cron       TEXT DEFAULT '0 3 * * *',     -- 全量扫描频率
    clean_cron      TEXT DEFAULT '0 4 * * 0',     -- 无效清理频率 (每周日)
    last_incremental DATETIME,
    last_full       DATETIME,
    last_clean      DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (destination_id) REFERENCES storage_destinations(id)
);

-- ========================================
-- 搜索历史
-- ========================================

CREATE TABLE search_history (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER,
    keyword     TEXT NOT NULL,
    results     INTEGER DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

-- ========================================
-- 系统设置
-- ========================================

CREATE TABLE settings (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## 18. 配置文件格式

```yaml
# configs/config.example.yaml

server:
  host: 0.0.0.0
  port: 3000
  mode: release                 # debug/release
  jwt_secret: "change-me"       # JWT签名密钥

database:
  driver: sqlite
  dsn: ./data/ohmycine.db
  # driver: postgres
  # dsn: "host=localhost user=omc password=xxx dbname=ohmycine port=5432"

proxy:
  # 302 URL缓存过期时间
  cache_ttl: 30m
  # 是否允许CORS
  cors: true

# 元数据
metadata:
  tmdb_api_key: ""
  language: "zh-CN"             # TMDB返回语言
  image_quality: "original"     # 海报质量: original/w500/w300

# 日志
log:
  level: info                   # debug/info/warn/error
  file: ./logs/ohmycine.log
  max_size: 100                 # MB
  max_backups: 3

# 首次启动自动创建的管理员账号
admin:
  username: admin
  password: admin               # 首次登录后应修改
```

## 19. Docker 部署

```yaml
# docker/docker-compose.yaml

services:
  ohmycine-server:
    image: ohmycine/server:latest
    container_name: ohmycine-server
    restart: unless-stopped
    ports:
      - "3000:3000"
    volumes:
      - ./data:/app/data                    # 数据库
      - ./configs:/app/configs              # 配置
      - ./strm:/app/strm                    # STRM输出目录
      - ./downloads:/app/downloads          # 下载临时目录
      - ./logs:/app/logs                    # 日志
    environment:
      - TZ=Asia/Shanghai

  # 可选: Emby
  emby:
    image: emby/embyserver:latest
    container_name: ohmycine-emby
    restart: unless-stopped
    ports:
      - "8096:8096"
    volumes:
      - ./emby/config:/config
      - ./strm:/media/library               # 共享STRM库
    environment:
      - TZ=Asia/Shanghai

  # 可选: qBittorrent
  qbittorrent:
    image: linuxserver/qbittorrent:latest
    container_name: ohmycine-qbittorrent
    restart: unless-stopped
    ports:
      - "8080:8080"
      - "6881:6881"
    volumes:
      - ./qbittorrent/config:/config
      - ./downloads:/downloads
```
