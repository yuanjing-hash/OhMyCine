# OhMyCine 开发规范

> 本文档是 AI 辅助开发的核心参考，所有代码生成和修改必须遵守本规范。

## 1. 项目总览

采用 **Monorepo** 结构，所有组件在同一 Git 仓库下：

```
ohmycine/                    ← 一个 Git 仓库
├── player/                  — Tauri v2 + Vue 3 + libmpv 播放器
├── server/                  — Go + Gin 后端服务
├── hub/                     — VitePress 插件市场
├── cli/                     — Go + Cobra 命令行工具 (与 server 共享 pkg/)
├── docs/                    — 架构文档
├── .github/                 — CI/CD Actions
├── DEVELOPMENT.md           — 本文件
├── README.md
├── LICENSE
└── .gitignore
```

**Monorepo 优势**：
- Player/Server/Hub/CLI 共享类型定义、API 规范、配置格式
- 一个 PR 可以同时修改前后端，保持一致性
- CI/CD 按目录过滤触发，只构建变更的部分
- Docker Compose 本地开发无需 clone 多个仓库

## 2. Git 工作流

### 2.1 分支策略

```
main                    # 生产分支，始终可发布
├── develop             # 开发分支，功能合入目标
├── feature/xxx         # 功能分支，从 develop 拉出
├── fix/xxx             # 修复分支，从 develop 拉出
└── release/x.x.x      # 发布分支，从 develop 拉出
```

### 2.2 Commit 规范

采用 [Conventional Commits](https://www.conventionalcommits.org/) 格式：

```
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

**Type 类型**：

| Type | 说明 | 示例 |
|------|------|------|
| `feat` | 新功能 | `feat(player): add Emby DataSource` |
| `fix` | Bug 修复 | `fix(server): fix 302 redirect cache invalidation` |
| `docs` | 文档 | `docs: update architecture overview` |
| `style` | 格式 (不影响逻辑) | `style(player): fix ESLint warnings` |
| `refactor` | 重构 | `refactor(server): extract transfer engine` |
| `perf` | 性能优化 | `perf(player): lazy load media grid` |
| `test` | 测试 | `test(server): add DataSource manager tests` |
| `chore` | 构建/工具 | `chore: update Tauri to v2.1` |
| `ci` | CI/CD | `ci: add Docker build workflow` |

**Scope 范围**：

| Scope | 说明 |
|-------|------|
| `player` | 播放器相关 |
| `server` | 后端相关 |
| `hub` | 插件市场相关 |
| `cli` | CLI 工具相关 |
| `docs` | 文档相关 |
| `api` | API 接口变更 |
| `db` | 数据库变更 |

**语言规范**：

- `type` 和 `scope` 保留英文，以兼容 Conventional Commits 和自动化工具
- 冒号后的简短描述使用中文
- Commit body 使用中文说明变更原因和影响
- Footer 中的标准字段可保留英文，如 `Closes #123`、`Co-Authored-By: ...`

**示例**：

```
feat(server): 添加剧集追更引擎

- 实现基于 cron 的 FollowService 调度
- 通过 TMDB 对比缺失集数
- 支持分辨率、编码和制作组偏好过滤

Closes #123
```

### 2.3 PR 规范

PR 标题使用与 Commit 相同的格式。PR 描述必须包含：

```markdown
## 变更说明
- 变更点1
- 变更点2

## 关联 Issue
Closes #xxx

## 测试
- [ ] 单元测试通过
- [ ] 手动测试通过
- [ ] 无 breaking change / 已标注

## 截图 (UI 变更时)
```

## 3. Go 后端开发规范 (Server / CLI)

### 3.1 项目结构

```
ohmycine-server/
├── cmd/
│   ├── server/main.go          # 入口，只做初始化和启动
│   └── omc/main.go
├── internal/                   # 私有代码，不可被外部导入
│   ├── config/                 # 配置加载
│   ├── database/               # 数据库连接和迁移
│   ├── models/                 # GORM 数据模型
│   ├── handlers/               # HTTP 处理器 (薄层，只做参数解析和响应)
│   ├── services/               # 业务逻辑 (核心代码在这里)
│   ├── middleware/              # HTTP 中间件
│   └── scheduler/              # 定时任务
├── pkg/                        # 公共代码，可被外部导入
│   ├── cloud/                  # 网盘驱动
│   ├── mediaserver/            # 媒体服务器客户端
│   ├── downloader/             # 下载客户端
│   ├── scraper/                # PT 站点
│   ├── metadata/               # 元数据刮削
│   ├── proxy/                  # 302 代理
│   └── strm/                   # STRM 生成
├── api/                        # API 定义 (OpenAPI)
├── configs/                    # 配置文件
└── docker/                     # Docker 相关
```

### 3.2 编码规范

**命名**：
- 包名: 小写单词，不使用下划线 (`cloud`, `metadata`)
- 接口: 动词+名词 (`DownloadClient`, `MediaServerClient`)
- 结构体: 大驼峰 (`StorageDestination`, `CategoryRule`)
- 函数: 大驼峰 (`GetDownloadURL`, `RefreshLibrary`)
- 常量: 全大写下划线 (`MAX_RETRY_COUNT`)
- 变量: 小驼峰 (`driverName`, `filePath`)

**错误处理**：
```go
// 好: 包装错误信息
if err != nil {
    return fmt.Errorf("failed to connect to Emby: %w", err)
}

// 好: 使用 sentinel errors
var ErrDriverNotFound = errors.New("driver not found")

// 好: 使用 zerolog 记录错误
log.Error().Err(err).Str("driver", name).Msg("Failed to get download URL")
```

**接口设计**：
```go
// 好: 小接口，单一职责
type DownloadClient interface {
    AddTorrent(ctx context.Context, req *AddRequest) (*Task, error)
    ListTasks(ctx context.Context) ([]*Task, error)
    PauseTask(ctx context.Context, taskID string) error
}

// 好: 使用 context 传播取消和超时
func (s *TransferService) OnDownloadComplete(ctx context.Context, task *DownloadTask) error {
    // ...
}
```

**数据库**：
```go
// 好: 使用事务
func (s *Service) CreateWithRelations(ctx context.Context, dest *StorageDestination) error {
    return s.db.Transaction(func(tx *gorm.DB) error {
        if err := tx.Create(dest).Error; err != nil {
            return err
        }
        // ...
        return nil
    })
}

// 好: 使用预加载
func (s *Service) GetDestination(id int64) (*StorageDestination, error) {
    var dest StorageDestination
    err := s.db.Preload("Connection").First(&dest, id).Error
    return &dest, err
}
```

### 3.3 API 设计规范

**响应格式**：
```json
// 成功
{
    "code": 0,
    "message": "success",
    "data": { ... }
}

// 分页
{
    "code": 0,
    "message": "success",
    "data": {
        "list": [ ... ],
        "total": 100,
        "page": 1,
        "page_size": 20
    }
}

// 错误
{
    "code": 40001,
    "message": "invalid parameter: url is required",
    "data": null
}
```

**HTTP 状态码**：
- `200` — 成功
- `201` — 创建成功
- `400` — 参数错误
- `401` — 未认证
- `403` — 无权限
- `404` — 资源不存在
- `500` — 服务器内部错误

**路由命名**：
```
GET    /api/v1/connections          # 列表
POST   /api/v1/connections          # 创建
GET    /api/v1/connections/{id}     # 详情
PUT    /api/v1/connections/{id}     # 更新
DELETE /api/v1/connections/{id}     # 删除
POST   /api/v1/connections/{id}/test # 自定义操作
```

### 3.4 依赖管理

```go
// go.mod 中固定主版本号
require (
    github.com/gin-gonic/gin v1.10.0
    gorm.io/gorm v1.25.12
    github.com/rs/zerolog v1.33.0
    github.com/spf13/viper v1.19.0
    github.com/robfig/cron/v3 v3.0.1
)
```

### 3.5 测试规范

```go
// 单元测试: 使用 testify
func TestCategoryService_AutoClassify(t *testing.T) {
    svc := NewCategoryService(mockDB)

    tests := []struct {
        name     string
        torrent  *Torrent
        parsed   *ParsedFilename
        expected string
    }{
        {
            name:     "movie from site category",
            torrent:  &Torrent{Category: "Movie"},
            parsed:   &ParsedFilename{Title: "Inception"},
            expected: "movie",
        },
        {
            name:     "tv from season number",
            torrent:  &Torrent{Category: ""},
            parsed:   &ParsedFilename{Title: "三体", Season: 1},
            expected: "tv",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := svc.AutoClassify(tt.torrent, tt.parsed)
            assert.Equal(t, tt.expected, result.MediaType)
        })
    }
}
```

## 4. Vue 前端开发规范 (Player)

### 4.1 项目结构

```
ohmycine-player/
├── src-tauri/                   # Rust 后端
│   ├── src/
│   │   ├── main.rs
│   │   ├── commands/           # Tauri Commands
│   │   ├── mpv/                # libmpv 集成
│   │   └── plugins/            # Tauri Plugins
│   ├── Cargo.toml
│   └── tauri.conf.json
├── src/                         # Vue 前端
│   ├── main.ts                 # 入口
│   ├── App.vue                 # 根组件
│   ├── components/             # 组件
│   │   ├── ui/                 # 基础 UI 组件
│   │   ├── layout/             # 布局组件
│   │   ├── player/             # 播放器组件
│   │   ├── media/              # 媒体展示组件
│   │   └── common/             # 通用组件
│   ├── views/                  # 页面
│   ├── stores/                 # Pinia 状态管理
│   ├── composables/            # 组合式函数
│   ├── services/               # 业务服务
│   │   ├── datasource/         # 数据源抽象层
│   │   ├── scraper/            # 刮削服务
│   │   ├── ai/                 # AI 服务
│   │   └── sync/               # 配置同步
│   ├── styles/                 # 全局样式
│   ├── router/                 # 路由
│   ├── i18n/                   # 国际化
│   └── utils/                  # 工具函数
├── package.json
├── tsconfig.json
├── vite.config.ts
└── unocss.config.ts
```

### 4.2 编码规范

**Vue 组件**：
```vue
<script setup lang="ts">
// 1. 导入
import { ref, computed, onMounted } from 'vue'
import { usePlayerStore } from '@/stores/player'
import { useMpv } from '@/composables/useMpv'

// 2. Props & Emits
const props = defineProps<{
  src: string
  autoplay?: boolean
}>()

const emit = defineEmits<{
  play: []
  pause: []
  ended: []
}>()

// 3. Store
const playerStore = usePlayerStore()

// 4. Composables
const { isPlaying, currentTime, duration, load, togglePause } = useMpv()

// 5. 状态
const isLoading = ref(false)

// 6. 计算属性
const progress = computed(() => {
  if (!duration.value) return 0
  return (currentTime.value / duration.value) * 100
})

// 7. 方法
async function handlePlay() {
  isLoading.value = true
  try {
    await load(props.src)
    emit('play')
  } finally {
    isLoading.value = false
  }
}

// 8. 生命周期
onMounted(() => {
  if (props.autoplay) {
    handlePlay()
  }
})
</script>

<template>
  <!-- 模板 -->
</template>

<style scoped>
/* 样式 */
</style>
```

**命名规范**：
- 组件文件: 大驼峰 (`MediaCard.vue`, `PlayerControls.vue`)
- 组件名: 与文件名一致
- Composable: `use` 前缀 (`useMpv`, `useServer`, `useTheme`)
- Store: `use` 前缀 + `Store` 后缀 (`usePlayerStore`, `useSettingsStore`)
- 类型文件: `.ts` 后缀 (`types.ts`, `api.ts`)
- 工具函数: 小驼峰 (`formatDuration`, `parseFilename`)

**TypeScript**：
```typescript
// 好: 使用 interface 定义对象结构
interface MediaItem {
  id: string
  name: string
  type: 'movie' | 'series' | 'episode' | 'folder' | 'file'
  posterUrl?: string
  year?: number
}

// 好: 使用 type 定义联合类型
type DataSourceType = 'emby' | 'jellyfin' | 'alist' | 'clouddrive2' | 'server'

// 好: 使用 enum 定义枚举
enum TransferMode {
  Move = 'move',
  Hardlink = 'hardlink',
  Copy = 'copy',
  Symlink = 'symlink',
}

// 好: 泛型约束
async function fetchAPI<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, options)
  return res.json() as Promise<T>
}

// 好: 使用 as const 断言
const MEDIA_TYPES = ['movie', 'series', 'episode'] as const
type MediaType = typeof MEDIA_TYPES[number]
```

### 4.3 状态管理 (Pinia)

```typescript
// stores/player.ts
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export const usePlayerStore = defineStore('player', () => {
  // 状态
  const currentMedia = ref<MediaItem | null>(null)
  const playlist = ref<MediaItem[]>([])
  const currentIndex = ref(-1)

  // 计算属性
  const hasNext = computed(() => currentIndex.value < playlist.value.length - 1)
  const hasPrev = computed(() => currentIndex.value > 0)

  // 方法
  function play(media: MediaItem) {
    currentMedia.value = media
  }

  function next() {
    if (hasNext.value) {
      currentIndex.value++
      currentMedia.value = playlist.value[currentIndex.value]
    }
  }

  return { currentMedia, playlist, currentIndex, hasNext, hasPrev, play, next }
})
```

### 4.4 Composable 规范

```typescript
// composables/useMpv.ts
import { ref, onUnmounted } from 'vue'
import { invoke } from '@tauri-apps/api/core'
import { listen } from '@tauri-apps/api/event'

export function useMpv() {
  // 状态
  const isPlaying = ref(false)
  const currentTime = ref(0)
  const duration = ref(0)
  const volume = ref(100)

  // 事件监听 (自动清理)
  const unlistenTime = listen<{ time: number }>('mpv:time-update', (e) => {
    currentTime.value = e.payload.time
  })

  const unlistenDuration = listen<{ duration: number }>('mpv:duration-change', (e) => {
    duration.value = e.payload.duration
  })

  // 方法
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

  // 清理
  onUnmounted(() => {
    unlistenTime.then(fn => fn())
    unlistenDuration.then(fn => fn())
  })

  return {
    isPlaying, currentTime, duration, volume,
    load, togglePause, seek, setVolume,
  }
}
```

### 4.5 样式规范

```css
/* 使用 CSS Variables 而非硬编码值 */
.card {
  background: var(--glass-bg);
  backdrop-filter: blur(var(--glass-blur));
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-lg);
  padding: var(--space-md);
  transition: all var(--duration-normal) var(--ease-out);
}

/* 使用 UnoCSS 原子类 */
/* 好: */
<div class="flex items-center gap-2 p-4 text-sm text-secondary">

/* 好: 组件样式用 scoped */
<style scoped>
.media-card {
  /* 组件专属样式 */
}
</style>
```

### 4.6 错误处理

```typescript
// 好: 统一错误处理
async function safeFetch<T>(url: string): Promise<T | null> {
  try {
    const res = await fetch(url)
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    }
    return await res.json()
  } catch (err) {
    console.error(`Failed to fetch ${url}:`, err)
    // 显示用户友好的错误提示
    showToast(`请求失败: ${err.message}`)
    return null
  }
}

// 好: Tauri Command 错误处理
async function loadVideo(path: string) {
  try {
    await invoke('mpv_load', { path })
  } catch (err) {
    console.error('Failed to load video:', err)
    showToast(`播放失败: ${err}`)
  }
}
```

## 5. Rust 后端开发规范 (Tauri)

### 5.1 项目结构

```
src-tauri/
├── src/
│   ├── main.rs              # 入口
│   ├── commands/            # Tauri Commands (暴露给前端)
│   │   ├── mod.rs
│   │   ├── player.rs        # 播放器命令
│   │   ├── file.rs          # 文件操作命令
│   │   ├── system.rs        # 系统信息命令
│   │   └── window.rs        # 窗口控制命令
│   ├── mpv/                 # libmpv 集成
│   │   ├── mod.rs
│   │   ├── player.rs        # MpvPlayer 结构体
│   │   └── render.rs        # 渲染上下文
│   ├── plugins/             # Tauri Plugins
│   │   └── mpv_plugin.rs
│   └── utils/               # 工具函数
├── lib/                     # 当前只声明 Windows 运行期资源；macOS/Linux 渲染和打包后续完成后再接入
├── Cargo.toml
├── build.rs                 # 构建脚本
└── tauri.conf.json
```

### 5.2 编码规范

**命名**：
- 模块: 小写下划线 (`mpv_player`, `file_commands`)
- 结构体: 大驼峰 (`MpvPlayer`, `RenderContext`)
- 函数: 小写下划线 (`load_file`, `get_property`)
- 常量: 全大写下划线 (`MAX_VOLUME`)
- 枚举: 大驼峰 (`PlaybackState`)

**错误处理**：
```rust
// 好: 使用 thiserror 定义错误类型
#[derive(Debug, thiserror::Error)]
pub enum MpvError {
    #[error("Failed to initialize MPV: {0}")]
    InitError(String),

    #[error("Failed to load file: {path}")]
    LoadError { path: String },

    #[error("Property not found: {0}")]
    PropertyNotFound(String),
}

// 好: Tauri Command 返回 Result
#[tauri::command]
pub async fn mpv_load(path: String, state: State<'_, MpvState>) -> Result<(), String> {
    let mpv = state.lock().map_err(|e| e.to_string())?;
    mpv.load_file(&path).map_err(|e| e.to_string())
}
```

**Tauri Command 规范**：
```rust
// 好: 命令名使用 snake_case
#[tauri::command]
pub async fn mpv_get_property(prop: String, state: State<'_, MpvState>) -> Result<String, String> {
    // 实现
}

// 好: 使用 State 管理共享状态
#[tauri::command]
pub async fn mpv_seek(position: f64, state: State<'_, MpvState>) -> Result<(), String> {
    let mpv = state.lock().map_err(|e| e.to_string())?;
    mpv.seek(position).map_err(|e| e.to_string())
}
```

### 5.3 依赖管理

```toml
[dependencies]
tauri = { version = "2", features = ["devtools"] }
tauri-plugin-shell = "2"
serde = { version = "1", features = ["derive"] }
serde_json = "1"
tokio = { version = "1", features = ["full"] }
thiserror = "1"

# libmpv
libmpv = "2.0"
libmpv-sys = "3.1"
```

## 6. 通用规范

### 6.1 文件编码

- 所有源代码文件: **UTF-8** 无 BOM
- 换行符: **LF** (Unix 风格)
- 缩进: **2 空格** (Vue/TypeScript/CSS), **4 空格** (Go), **4 空格** (Rust)
- 文件末尾: 保留一个空行

### 6.2 注释规范

```go
// 好: 解释 WHY，而非 WHAT
// 115 网盘的下载链接有时效性，需要在过期前刷新缓存
func (e *Engine) refreshCache(key string) {
    // ...
}

// 好: 公开 API 必须有注释
// GetDownloadURL 获取302重定向下载链接
// 返回的 URL 有时效性，调用方应缓存并关注 ExpiresAt
func (d *Driver) GetDownloadURL(ctx context.Context, path string) (*DownloadURL, error) {
    // ...
}
```

```typescript
// 好: 解释复杂逻辑
// 自动分类匹配优先级:
// 1. 站点分类 (最可靠)
// 2. 文件名解析 (有 season → tv)
// 3. TMDB 查询确认 (兜底)
function autoClassify(torrent: Torrent, parsed: ParsedFilename): CategoryRule {
    // ...
}
```

### 6.3 日志规范

**Go (zerolog)**:
```go
// 好: 结构化日志
log.Info().
    Str("driver", "alist").
    Str("path", "/media/movies").
    Int("count", len(files)).
    Msg("Directory listed successfully")

log.Error().
    Err(err).
    Str("task_id", task.ID).
    Msg("Transfer failed")
```

**Vue (console)**:
```typescript
// 好: 带前缀的调试日志
console.log('[DataSource] Connected to Emby:', url)
console.warn('[Scraper] TMDB search failed for:', title)
console.error('[Player] Failed to load video:', err)
```

### 6.4 API 版本管理

- 所有 API 使用版本前缀: `/api/v1/`
- Breaking change 时递增版本号: `/api/v2/`
- 旧版本保留至少一个大版本周期

### 6.5 安全规范

- [ ] 密码使用 bcrypt 哈希存储
- [ ] 敏感配置 (API Key, Cookie) 使用 AES-GCM 加密存储
- [ ] JWT Token 设置合理过期时间 (24h)
- [ ] API 输入参数必须验证
- [ ] SQL 查询使用参数化 (GORM 自动处理)
- [ ] CORS 配置限制允许的域名
- [ ] 文件路径操作防止目录遍历攻击

### 6.6 测试覆盖率目标

| 模块 | 目标覆盖率 | 说明 |
|------|-----------|------|
| pkg/cloud/ | 80% | 网盘驱动核心逻辑 |
| pkg/downloader/ | 80% | 下载客户端核心逻辑 |
| internal/services/ | 70% | 业务逻辑 |
| internal/handlers/ | 60% | API 处理器 |
| src/services/ (Vue) | 60% | 前端业务服务 |

## 7. 构建与发布

### 7.1 版本号

采用 [Semantic Versioning](https://semver.org/)：

```
MAJOR.MINOR.PATCH

MAJOR: 不兼容的 API 变更
MINOR: 向后兼容的功能新增
PATCH: 向后兼容的 Bug 修复
```

### 7.2 发布流程

1. 从 `develop` 创建 `release/x.x.x` 分支
2. 在 release 分支上修复最后的 Bug
3. 更新版本号和 CHANGELOG
4. 合并到 `main` 并打 tag
5. CI 自动构建并发布
6. 合并回 `develop`

### 7.3 Player Beta 自动发版

Player beta 版本使用 `vMAJOR.MINOR.BETA` 规则：

```text
v0.0.1  # 0.0 阶段第 1 个 beta
v0.0.2  # 0.0 阶段第 2 个 beta
v0.1.1  # 0.1 阶段第 1 个 beta
```

推送 `v*.*.*` tag 或手动触发 `Player Beta Release` workflow 时，CI 会：

1. 将 Player 的 `package.json`、`src-tauri/tauri.conf.json` 和 `src-tauri/Cargo.toml` 临时同步为 tag 版本号
2. 使用 Windows GNU target 构建 NSIS 安装包
3. 从 `player/src-tauri/target/x86_64-pc-windows-gnu/release` 整理 portable zip
4. 自动生成 GitHub Release notes
5. 创建 GitHub prerelease，并上传安装包、portable zip 和 SHA-256 校验文件

Beta Release notes 规则：

- CI 按版本排序查找当前 tag 之前的上一个 `v*.*.*` tag；如果没有上一个 tag，则从仓库初始提交统计到当前提交。
- Commit 只使用 subject 生成 Markdown，并按 Conventional Commit type 粗略分组：`feat`、`fix`、`docs`、`ci`、`chore`、`refactor`、`test`、`other`；scope 保留在条目里。
- 手动触发时如果填写 `release_notes`，CI 会作为 `Extra Notes` 附加到自动日志中。
- Release notes 保留 beta 版本规则、资产说明和 SHA-256 校验文件说明。
- 生成逻辑不输出 secret、token 或 GitHub Actions 环境变量；不要在 commit subject 或手动补充说明中写入敏感信息。

示例：

```bash
git tag v0.0.1
git push origin v0.0.1
```

当前普通 `Player CI`、`Manual Build` 和 beta release 中的 Player 包构建只验证/发布 Windows GNU。Linux/macOS Player 渲染器和打包链路完成前，不把 Linux/macOS Player 包加入 CI 阻塞项。

Windows GNU 构建使用同一条 cross-build 链路：

```bash
cd player
npm run setup:libmpv -- windows
RUSTC="$(rustup which rustc)" npm run tauri:build:windows
```

因此 CI 不再依赖 `windows-latest`/MSVC 来构建 Windows Player 包；Windows 桌面运行、签名和真实播放仍需要在 Windows 宿主环境最终验证。

### 7.4 Docker 镜像

```dockerfile
# 多阶段构建
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o server ./cmd/server

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/configs ./configs
EXPOSE 3000
CMD ["./server"]
```

## 8. 文档规范

### 8.1 代码文档

- Go: 所有公开函数/类型必须有 godoc 注释
- TypeScript: 所有公开接口/类型必须有 JSDoc 注释
- Rust: 所有公开函数必须有 `///` 文档注释

### 8.2 架构文档

- 所有架构变更必须更新 `docs/architecture/` 目录
- 新模块必须有设计文档
- API 变更必须更新 OpenAPI 规范

### 8.3 CHANGELOG

- 仓库根目录的 `CHANGELOG.md` 记录版本策略和人工维护的正式发布摘要。
- Player beta 的 GitHub Release notes 由 CI 从 tag/commit 自动生成，不需要人工把每个 beta 的 commit 清单复制进 `CHANGELOG.md`。
- 正式版发布时，再把相关 beta 的用户可见变化汇总到 `CHANGELOG.md`。
- 不要在 changelog、release notes、commit subject 或手动补充说明中写入 API key、cookie、token、passkey、下载器密码、AI key 或带签名参数的播放 URL。
