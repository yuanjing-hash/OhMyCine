# 115 媒体库目录、分页与作品聚合设计

## 1. Architecture

```text
Connection(pan115)
  -> Storage(provider root ID + display path)
    -> MediaLibrary(provider root ID + storage-relative display path)
      -> MediaLibraryEntry(file facts + work key)
        -> catalog read model
          -> Movie
          -> Series -> Season -> Episode
```

Storage 仍定义安全边界；MediaLibrary 只允许选择该边界内的目录。文件索引是扫描与 reconciliation 的事实来源，作品目录是通过持久化 work key 和聚合 SQL 形成的读模型，不复制一套可漂移的作品表。

## 2. Provider Directory Contract

扩展 Storage-scoped directory browsing，而不是让媒体库直接使用 Connection 根选择器：

```text
GET /api/v1/storages/{id}/directory
GET /api/v1/storages/{id}/directory?token=...&page_token=...
```

- local Storage 继续走 `DirectoryBrowserService`。
- pan115 Storage 走 `ProviderDirectoryService.BrowseStorage`。
- provider token claims 新增 `storage_id` 和 `storage_root_id`，每次浏览、翻页、选择和解析都重新验证 actor、Connection、Storage 与 root。
- root listing 的 current selection token 表示 Storage 根；root 不返回 parent token。
- `ResolveStorageSelection` 返回服务端内部 `{ProviderID, RelativeRoot, DisplayPath}`。`RelativeRoot` 以 `/` 开头并相对 Storage 根；DTO 只持久化/返回 RelativeRoot。

`DirectoryPickerDialog` 继续使用统一的 `storageId + restrictToStorage` 输入。其后续 browse 请求不能硬编码 `/filesystem/directories`，而应沿用 initial storage endpoint；这样 local 和 provider 都保持在同一 Storage scope 内。

## 3. Persistence And Migration

新增迁移 v21：

- `media_libraries.provider_root_id TEXT NOT NULL DEFAULT ''`，API DTO 使用 `json:"-"`。
- `media_library_entries.work_key TEXT NOT NULL DEFAULT ''`。
- `media_library_entries.series_title TEXT NOT NULL DEFAULT ''`。
- 复合索引 `(library_id, work_key)`，以及分页/过滤需要的 `(library_id, media_type, title)` 索引。
- 对既有 pan115 library，把 `provider_root_id` 回填为对应 Storage 的 `root_path`。
- 对既有 tv Entry 使用当前 title 生成兼容 series key；其它 Entry 使用稳定的 entry key。后续扫描用新 parser 覆盖。

不新增作品表：作品统计均可由 indexed Entry 聚合，避免扫描事务同时维护两套事实。若未来 TMDB 元数据需要作品级覆盖，再单独引入 work entity。

## 4. Scan And Parsing

`MediaLibraryService.reconcile` 对 115 选择：

```go
rootID := library.ProviderRootID
if rootID == "" { rootID = storage.RootPath } // upgrade compatibility
ScanProvider(ctx, driver, rootID, ...)
```

`internal/medialibrary` 增加 path-aware parser，输出：

```go
type ParsedMedia struct {
    MediaType   string
    Title       string
    SeriesTitle string
    Season      *int
    Episode     *int
    WorkKey     string
}
```

识别次序与 Player 对齐：文件分集标记 -> 季度目录 -> 剧名父目录 -> 清理技术/发布组/字幕噪声 -> 规范化 key。Parser 是 local 与 provider 扫描的单一来源，不能在 UI 再做一份聚合规则。

## 5. API Contracts

### Raw entries

```text
GET /api/v1/media-libraries/{id}/entries?page=1&page_size=50&query=&media_type=
```

响应保持标准 `{list,total}`，list 为文件 Entry；服务端执行 COUNT + LIMIT/OFFSET。暂时兼容旧 `limit` 查询但新 UI 不再使用。

### Catalog works

```text
GET /api/v1/media-libraries/{id}/catalog?page=1&page_size=50&query=&media_type=
GET /api/v1/media-libraries/{id}/catalog/{work_key_token}
```

列表响应每个作品包含 opaque work token、标题、`movie|series`、文件/季/集计数、聚合大小、最近修改时间和当前分类/匹配摘要。work token 由服务端编码/签名或使用 URL-safe opaque ID，浏览器不拼接原始 work key。

详情响应只包含该作品的 season groups 和文件级 episode DTO。所有查询先验证 library read 权限和 work 对当前 library 的归属。

## 6. Web UI

- create/edit 对 local 和 pan115 都显示“选择目录/重新选择”。
- 媒体清单使用 catalog endpoint；Series 行提供展开按钮，按需加载详情并缓存到当前 library 生命周期。
- 顶部提供搜索、类型筛选和 page-size menu；底部显示范围、真实 total、页码及前后页。
- 使用 request generation 或 AbortController 防止轮询、搜索和媒体库切换发生旧响应覆盖。
- 表格保持 Server 管理台现有密度，不改成 Player 海报墙；Player 设计在本任务中用于领域聚合行为，不用于照搬视觉布局。

## 7. Compatibility And Rollback

- 迁移仅加列/索引并回填，不删除任何 Entry。
- 旧客户端仍可调用 `/entries?limit=N`；返回真实 total 是兼容性修复。
- provider root ID 为空时回退 Storage root，保证滚动升级期间可扫描。
- 若新 catalog UI 出错，raw entries endpoint 仍可诊断，回滚前端不会破坏扫描事实。
- 不修改现有 115 bulk enumeration 协议；只改变传入的安全 root ID。

## 8. Risks

- 作品 key 依赖文件/目录命名，无法替代 TMDB；未识别内容必须保持可见而不能错误丢弃。
- SQLite `COUNT(DISTINCT work_key)` 在大库上依赖复合索引；需要用 1 万级 fixture 或 explain/query test 防止全量 Go 聚合。
- provider token 的 scope 扩展属于安全敏感修改，必须覆盖跨 actor、跨 Storage、跨 Connection、过期和根逃逸测试。
