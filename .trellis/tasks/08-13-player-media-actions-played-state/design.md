# 技术设计：Player 媒体操作菜单与已播放状态

## 1. 总体边界

本任务建立一个统一的媒体操作领域层，页面只负责提供目标对象和承载响应式 UI，不直接判断 Emby、网盘或本地文件 API。

```text
桌面右键 / 手机长按 / 播放页工具菜单
                ↓
        MediaActionController
                ↓
  target + capability + confirmation
                ↓
┌──────────────────────────────────────┐
│ Provider adapter                     │
│ Emby/Jellyfin native operations      │
├──────────────────────────────────────┤
│ Player local domain                  │
│ history / collections / metadata     │
├──────────────────────────────────────┤
│ Native privileged operations         │
│ download / source-file delete / SAF  │
└──────────────────────────────────────┘
                ↓
     invalidate caches + refresh UI
```

Player 仍可脱离 Server 独立工作。Server 下载管线是未来可选 provider adapter，不成为本地菜单、已播放状态或直接 DataSource 下载的前置条件。

## 2. 共享目标与动作契约

### 2.1 稳定目标身份

共享菜单消费 `MediaActionTarget`，不持久化播放直链：

```ts
interface MediaActionTarget {
  sourceId: string
  sourceType: DataSourceType
  kind: 'media' | 'library'
  itemId: string
  libraryId?: string
  mediaIdentity?: string
  mediaType?: MediaItem['type']
  displayName: string
  aggregate?: 'single' | 'movieVersions' | 'season' | 'series'
  childTargets?: MediaActionTarget[]
}
```

远端临时 URL、Cookie、Header、签名参数和本地不受控绝对路径不进入该对象。原始文件源路径仅在 DataSource/原生命令边界内由稳定 item identity 重新解析。

### 2.2 动作描述

```ts
type MediaActionId =
  | 'play' | 'resume'
  | 'markPlayed' | 'markUnplayed' | 'removeFromContinueWatching'
  | 'favorite' | 'unfavorite' | 'addToPlaylist' | 'addToCollection'
  | 'download' | 'downloadTo'
  | 'editMetadata' | 'editArtwork' | 'editSubtitles'
  | 'identify' | 'refreshMetadata' | 'rescanLibrary'
  | 'delete'

interface MediaActionCapability {
  id: MediaActionId
  availability: 'available' | 'disabled' | 'hidden'
  reason?: string
  danger: 'none' | 'confirm' | 'destructive'
}
```

`MediaActionController` 是唯一执行入口，负责 generation/idempotency、加载态、确认请求、错误脱敏和执行后的缓存失效。共享菜单组件只渲染 capability，不自行调用 provider。

### 2.3 Provider adapter

- Emby/Jellyfin adapter：提供方原生已播放、收藏、播放列表、合集、下载、删除、元数据/图像/字幕编辑及刷新；功能受服务端版本、用户权限和对象类型约束。
- Raw/local adapter：Player 本地状态、集合和刮削资产；下载与源删除委托原生 DataSource operation adapter。
- Library adapter：进入媒体库、重新扫描当前库；Emby/Jellyfin 调提供方刷新，原始来源调 Player 扫描调度器。
- 未实现或无法安全实现的 capability 为 `hidden`；权限缺失或配置暂不可用为 `disabled + reason`，不出现假按钮。

## 3. 输入与菜单架构

### 3.1 播放画面

- Android/触摸长按继续由播放手势状态机独占：右侧倍速、左侧连续后退。
- Player 根 `contextmenu` 只接受真实鼠标右键。原生 Android 直接阻止播放根 contextmenu；桌面触摸设备根据当前/最近触摸 pointer session 抑制合成 contextmenu。
- 播放页移动操作进入 `MobilePlayerControls` 右上角工具面板；桌面仍可使用响应式播放上下文菜单。

### 3.2 媒体对象

- `MediaCard` 扩展统一 Pointer Events 长按识别：移动阈值、滚动取消、释放取消、合成 click/contextmenu 抑制。
- 鼠标右键与触摸长按最终都调用同一个 `open(target, anchor)`。
- 桌面使用定位 popover；移动使用底部/横屏侧边 sheet。
- App 根统一 `preventDefault` 原生 contextmenu，但仅媒体对象、媒体库对象和桌面播放画面打开自定义菜单。

## 4. 已播放与继续观看

### 4.1 本地存储

扩展 `playback_history.sqlite` 命令：

- 单条标记已播放/未播放；
- 单条移出继续观看（重置或删除进度，不影响媒体）；
- 按多个稳定 identity 批量查询完成态；
- 保留自动完成阈值和历史完成记录。

`MediaItem` 增加来源中立的 `played?: boolean` / `playCount?` 或等价状态快照，`progressSource` 扩展为 provider/local 语义，不能靠 `progress === 1` 临时推断所有页面状态。

### 4.2 Provider 状态

- Emby/Jellyfin 映射 `UserData.Played`、进度和收藏状态；手动操作调用提供方原生接口并清理 detail/home cache。
- 非 Emby 使用 Player 本地状态。
- 电影多版本代表同一作品，任一版本完整观看即可标记该电影作品完成；季仅在所有已知可播放集完成时完成；系列仅在所有已知可播放季/集完成时完成。空集合和仅一集完成不得误标整季/整剧。

### 4.3 展示

- 首页继续观看、所有海报墙、搜索结果与详情页复用同一完成态读取。
- 海报右下角显示可访问性清晰的勾选徽标；详情页在标题/主操作附近显示状态与切换动作。
- 完成项从继续观看移除，但不从最近添加、收藏、合集或媒体库消失。

## 5. 收藏、播放列表与合集

- Emby/Jellyfin 完全使用提供方原生实体，不双写 Player 数据库。
- 非 Emby 使用独立 Player SQLite：collection entity、ordered membership、type (`favorite|playlist|collection`)、稳定媒体身份和非敏感展示快照。
- 收藏可视作系统管理的单一集合；播放列表有顺序；合集是无播放顺序的媒体组织。
- 本地集合允许跨来源；来源被删除或目标失效时保留 `missing` 成员供用户诊断/清理。

## 6. 下载架构

### 6.1 目录

- 设置保存默认下载目录引用。桌面初始为系统 Downloads，可显式选择；Android 保存 SAF tree grant 和显示名。
- “下载”使用默认目录；“下载到”创建一次性目标授权，不修改默认设置。
- 本地文件动作显示“复制到/另存到”。

### 6.2 原生任务与 302

下载任务由 Tauri 原生层执行并持久化非敏感状态：目标身份、目标文件、已下载字节、ETag/Last-Modified（若安全）、状态和错误类别。禁止持久化最终 URL、Cookie、Authorization、Referer、签名参数或播放 Header。

```text
MediaActionTarget
  → DataSource native resolver
  → current original URL + headers
  → bounded redirect validation
  → Range-capable streaming writer
  → temporary partial file
  → fsync/atomic finalize
```

302/STRM 来源必须解析原始媒体响应，不能保存代理跳转或 `.strm` 文本。续传时重新按来源身份解析 URL；若上游不接受 Range 或实体校验变化，则明确重新开始，不能拼接损坏文件。

Android 使用 SAF 输出流和前台服务通知；桌面使用受控目录与磁盘空间检查。聚合下载展开成父任务 + 文件级子任务，支持取消、失败重试和部分成功。

## 7. 删除架构

统一确认输入：

```ts
interface DeleteConfirmation {
  removeFromLibrary: true
  deleteSourceFiles: boolean
  typedConfirmation?: string
}
```

- 默认只从媒体库移除/隐藏：Player 自有来源写本地 tombstone/override；提供方按其原生能力处理。
- 勾选源删除后，Emby/Jellyfin 遵循提供方原生接口与范围。
- Player 自有聚合项解析文件清单。电影多版本、整季、整剧展示数量和路径摘要，可展开；整季/整剧必须输入作品名。
- 本地删除只接受配置根内规范化文件，拒绝遍历和 symlink escape。网盘删除通过各 provider 原生命令执行，不向 Vue 暴露凭据。
- 视频、字幕和伴随文件必须由扫描记录的明确归属关系决定，禁止根据宽泛文件名 glob 猜测删除。
- 分批执行并返回逐项结果。成功项才从索引/历史/缓存移除，失败项保持可见；不提供“报告成功但只隐藏”的降级。

## 8. 元数据与维护操作

- 非 Emby 的编辑元数据、图片、字幕继续属于 Player 本地刮削数据库/缓存，不写回源目录。
- 识别调用现有 manual identification；刷新元数据重新刮削选中目标；媒体库重扫调用既有 index scheduler。
- Emby/Jellyfin 调用提供方原生元数据、图片、字幕编辑和 refresh API，按权限隐藏/禁用。
- 页面现有局部识别菜单迁移到共享 action controller，编辑对话框可继续复用已有组件与状态服务。

## 9. 一致性、迁移与回滚

- 新 SQLite 表/字段采用幂等迁移，不删除现有播放历史。
- Provider mutation 成功后统一失效 manager/home/detail cache；本地 mutation 发布单一领域事件或更新 Pinia snapshot，避免页面各自刷新。
- 每个子任务都可独立回滚：菜单根据 capability 自动隐藏未交付模块，不需要保留空入口。
- Windows、Linux 和 Android 共用 TypeScript 领域契约；文件选择、写入、删除和后台任务保持平台原生实现。

## 10. 安全与审计

- 删除、下载、provider mutation 的错误输出统一脱敏。
- 源删除和批量写操作记录不含路径明文/凭据的本地审计摘要；详细路径只在当前确认 UI 和本地受控执行上下文短期存在。
- 所有网络操作限制 HTTP(S)、重定向、超时；下载不设置通用响应体上限，但必须执行 Content-Length/空间/配额策略和流式写入，禁止整文件读入内存。
- 本任务不自动开放上传、重命名、覆盖源 NFO/图片/字幕等其他写权限。
