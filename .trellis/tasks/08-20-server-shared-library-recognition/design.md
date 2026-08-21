# 统一媒体库扫描识别与目录对账设计

## 1. Boundaries

本任务把识别放在 provider 与业务消费者之间：

```text
local / 115 / future provider enumeration
                │
                ▼
        provider-neutral file facts
                │
                ▼
       recognition-unit grouping
                │
                ▼
Profile preprocess → parse candidates → TMDB → classify
                │
                ├── DownloadWorker → trusted transfer manifest
                └── MediaLibraryService → recognition + catalog projection
```

- `internal/medialibrary` 只负责根边界、枚举、文件事实、基础结构提示和 deterministic grouping，不访问 TMDB、不读取 Profile。
- 新的公共 recognizer 位于 Server domain/service 边界，通过接口依赖 TMDB provider 和纯 classification matcher。
- `DownloadWorker` 只把下载 manifest 与入队 Profile 快照适配成公共输入。
- `MediaLibraryService` 只把当前扫描事实与当前 Profile revision 适配成公共输入，并负责 generation 对账。
- provider adapter 不解析标题、不选分类、不创建 catalog work key。

## 2. Shared recognition contracts

公共输入包含：

```go
type RecognitionPackage struct {
    PackageName string
    Files       []RecognitionFile // provider-relative facts only
}

type RecognitionOptions struct {
    ProfileRevision  uint64
    Classification   classification.RulesV1
    RecognitionRules []RecognitionRule
    Language          string
    Region            string
}

type RecognitionResult struct {
    Status        string // matched|unrecognized
    ErrorCode     string
    Title         string
    MediaType     string
    Year          *int
    TMDBID        *int64
    Confidence    *float64
    CategoryName  string
    MatchedRuleID *string
}
```

候选生成、Profile 预处理、媒体类型/年份判断和 TMDB 置信度阈值只有一个实现。下载的“可信主媒体筛选”仍属于下载包安全接管，但它消费公共识别结果，不重新识别。

TMDB 通过窄接口注入，生产实现由 `MetadataSettingsService` 构造，测试使用 fake。公共识别服务不读取密文，不知道凭据值。

## 3. Recognition units

扫描枚举得到文件事实后进行 deterministic grouping：

- 剧集：从季目录、可信季集标记和作品父目录形成作品单元；同作品多季可共享去重后的 TMDB lookup，条目仍保留季集号。
- 电影：有意义发行目录内的视频和关联结构属于一个单元；媒体库根目录下每个视频默认独立，避免把多部影片合包。
- 光盘：`BDMV/STREAM`、`VIDEO_TS` 等保留外层发行目录作为 package name。
- 无法形成可信结构时：每个视频形成独立未识别单元，不把整库目录当成标题。

`source_key` 是库内 opaque hash，不直接包含路径；`input_fingerprint` 由有序文件事实摘要、候选相关父目录摘要、Profile revision、语言和地区构成。Provider ID 可参与私有源稳定性判断，但不进入公开 DTO、日志或工作键。

## 4. Persistence and migration

v25 采用加法迁移：

```text
media_library_recognitions
  id, library_id, source_key, input_fingerprint
  profile_id, profile_revision
  status, error_code
  media_type, title, release_year, tmdb_id, confidence
  category_name, matched_rule_id
  manual_override, last_generation
  created_at, updated_at

media_library_entries
  + recognition_id (nullable FK/set-null during migration transition)
  + tmdb_id, release_year, match_confidence, recognition_error_code

media_recognition_cache
  lookup_key, status, safe result fields/json, expires_at, updated_at
```

- `(library_id, source_key)` 唯一；library 删除和 source replacement 删除 scoped recognitions。
- 已匹配 `work_key` 使用 `tmdb:movie:<id>` 或 `tmdb:tv:<id>` 的摘要形式；未识别使用基于 recognition ID/source key 的库内 opaque key。
- 条目保留查询所需的安全投影，catalog 继续在 SQLite 先 group 再 page。
- v24 升级不伪造 matched 记录；旧 entries 保留为待重算事实，下一次 reconciliation 关联 v25 recognition。
- 缓存不保存凭据、URL、绝对路径、provider ID 或原始响应。正向结果使用较长 TTL，no-match/暂时错误使用短 TTL；认证失败不做长期负缓存。

## 5. Scan transaction flow

```text
enumerate facts (no DB transaction, no TMDB)
→ load existing facts/recognitions
→ build changed recognition units
→ recognize outside DB transaction with limiter/cache
→ short transaction:
   verify library source identity + generation still current
   upsert recognitions and entries
   delete proven missing rows only when !partial
   update scan counters/generation/status
```

如果配置在网络识别期间改变，generation/source identity 校验拒绝陈旧结果，由 supervisor 重新对账。单个 TMDB 失败产生 `unrecognized` 结果而非 scan failure；枚举、数据库、边界或上下文错误仍使 scan run 失败。

本地 watcher 将同一个 debounce 窗口内的路径归并为受影响单元；115 生活事件继续唤醒 reconciliation，并用 provider stable ID 对移动/改名收敛。完整/partial 删除语义保持现有契约。

## 6. Manual correction

新增受媒体库读取/扫描权限保护的 API：

```text
GET    /api/v1/media-libraries/:id/recognitions
POST   /api/v1/media-libraries/:id/recognitions/:token/retry
GET    /api/v1/media-libraries/:id/recognitions/:token/tmdb-candidates
PUT    /api/v1/media-libraries/:id/recognitions/:token/override
DELETE /api/v1/media-libraries/:id/recognitions/:token/override
```

公开 token 编码库内 recognition identity，不包含路径或 provider ID。候选查询接收受限 title/media_type/year，返回有限候选摘要。保存 override 只接受 TMDB ID 与媒体类型；服务端重新获取详情、校验身份并执行当前 Profile 分类。普通 reconciliation 保留人工 override，清除后立即重新自动识别。

## 7. UI and API compatibility

- `/catalog` 增加 `match_status=matched|unrecognized` 可选筛选，不改变原分页 envelope。
- Catalog item 增加可选 `tmdb_id`、`year`、`confidence` 和安全 `recognition_error_code`。
- 扫描 run 增加 matched、unrecognized、cache_hits、recognition_failed 计数。
- 媒体库“媒体清单”页使用现有管理 tabs 和表格风格，增加状态切换；未识别详情提供重试和匹配弹层，操作结果使用全局 Toast。
- 前端从 opaque endpoint helper 构造 URL，不自行拼 provider 路径。

## 8. Logging and security

新增中央 operation `media_recognition` / `媒体识别`。批次日志包含 `library_id`、`scan_run_id`、计数、duration、status/error code；单项人工操作记录 audit 的 resource IDs 和 outcome。绝不记录：

- 本地绝对路径、完整 provider path 或 provider item ID
- TMDB token/API key、Cookie、请求 query、原始响应
- 识别缓存 payload 或候选完整源文件名

扫描和人工匹配均只读来源文件。任何写入媒体文件的能力仍只属于 `TransferService`。

## 9. Compatibility and rollback

- API 字段和表均为加法；旧 Web UI 可继续读取原 catalog。
- recognizer 提取后，下载回归测试必须保持现有完成包行为和错误码。
- 若 v25 识别投影出现问题，可停用新识别调度并保留旧 entries；迁移不删除 v24 数据，不需要回滚列。
- 真实 115 测试保持只读且显式 opt-in，不在默认质量门访问用户账号。

## 10. Built-in recognition word packs

新增只读内置资源层，使用 `go:embed` 打包固定快照及归属文件：

```text
internal/mediarecognition/builtin/
  TV.txt
  anime.txt
  MoviePilot-Help-LICENSE.txt
  sources.json              # source URL, commit, synced_at, sha256
```

Profile 保存内置 pack code 的规范数组，默认是 `tv-v1,anime-v1`。这与用户 `recognition_rules_json` 分开存储，避免 322 条内置规则突破用户规则上限或复制到每一行数据库。protected 默认 Profile 只引用 pack code；自定义 Profile/复制操作保存自己的启用选择。

词包编译器实现 MoviePilot custom words 的四种格式：block、replace、episode offset、replace+offset。兼容引擎仅用于固定内置 pack，必须设置每次 match timeout、输入长度和替换次数上限；普通用户 Profile 规则继续使用 Go RE2。Python 风格 replacement backreference 在编译时转换为兼容引擎格式。

`{[tmdbid=...;type=...;s=...;e=...]}` 被解析为结构化 hint，不把标签继续当普通标题。Recognizer 使用 `GetByID(mediaType, tmdbID)` 验证远端身份；season/episode hint 只覆盖本次解析结果，最终分类仍来自可信 TMDB 元数据和当前 Profile。

启动/测试阶段对每个内置 pack 全量解析和预编译。固定快照中任一非注释规则不兼容时测试失败；运行时 timeout/error 返回稳定 code，批次继续处理其他单元。Web UI 只显示 pack 名称、版本、来源和启用开关，不允许编辑内置正文。
