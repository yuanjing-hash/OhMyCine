# Server 115 网盘连接与云存储驱动设计

## 管理端入口约束

用户侧只提供一个“数据源”页面和一份混合列表，不把后端 Connection / Storage 分层直接做成两个分页。“添加数据源”必须先选择本地目录或网盘类型，再展示对应配置；Connection 负责凭据、Storage 负责根目录的内部边界仍保持不变。

## 1. Scope

本任务实现 115 的首个只读纵向切片：

```text
115 Connection → Cookie probe → 云目录浏览 → 115 Storage → MediaLibrary 扫描
              → 生活事件增量 + 周期文件树对账 → DirectURL capability
```

本任务不开放通用上传、移动、复制、重命名和删除。115 原生离线下载作为只写入明确云端 Storage 根的受限能力接入现有 Downloader 状态机；后续整理仍需独立的云端 mutation、确认和恢复设计。

## 2. Ownership boundaries

### Connection

`Connection` 是 provider 账号和凭据的唯一 owner：

- provider 固定为 `pan115`；用户界面显示 `115 网盘`。
- 保存规范化 Cookie 的 AES-GCM ciphertext。
- 保存脱敏账号摘要、容量、健康状态、稳定错误码和最后检测时间。
- 删除前拒绝仍被 Storage 引用的 Connection。

Cookie 更新语义：

- 创建必须提供 Cookie。
- 更新 Cookie 留空表示保留旧值。
- `clear_cookie=true` 只允许在 Connection 不再被启用 Storage 使用时执行；首期 UI 不提供清除按钮。
- Cookie 测试失败时不覆盖现有可用凭据。

### Storage

`Storage` 表示一个 provider root：

- `type=pan115`。
- `connection_id` 必填。
- `root_path` 保存 provider directory ID，不保存凭据或临时 URL。
- 新增 `root_display_path` 保存仅用于管理端展示的 provider-relative 路径。
- `root_path_normalized` 使用 `pan115:<connection_id>:<directory_id>`，避免不同账号同 ID 冲突。
- capabilities 由 Driver probe 生成，客户端不能自报。

### MediaLibrary

MediaLibrary 保持 `storage_id + relative_root` 事实模型。云媒体库的 `relative_root` 仍以 `/` 开头，但解析由 provider item identity 完成，不转换成 Server 绝对路径。

扫描器统一输出：

```go
type Entry struct {
    ProviderID  string
    RelativePath string
    Size        int64
    ModifiedAt  time.Time
    Name        string
}
```

分类和数据库 reconciliation 继续复用现有逻辑。local watcher 与 pan115 watcher 只在 supervisor adapter 层分流。

## 3. Provider-neutral contracts

`pkg/cloud` 只定义稳定领域接口，不包含 GORM、Gin、SDK 或 UI 类型：

```go
type Driver interface {
    Provider() string
    Probe(ctx context.Context) (Account, Capabilities, error)
    List(ctx context.Context, parentID string, page PageRequest) (Page, error)
    Stat(ctx context.Context, itemID string) (Item, error)
    DirectURL(ctx context.Context, file DirectURLRequest) (TemporaryURL, error)
}
```

`pkg/cloud/pan115` 把 `github.com/SheltonZhu/115driver` v1.3.5 隔离在 adapter 内。业务服务通过 factory 创建连接级 Driver。第三方 SDK 没有原生 `context.Context` 参数，因此 adapter 必须：

- 给底层 `http.Client` 设置有限 timeout；
- 在 provider semaphore 内等待 SDK 调用真正退出后才释放 slot；
- 对调用方响应 context cancellation；
- 永不启用 SDK debug/trace；
- 将所有 SDK/upstream error 映射为有限的 provider error kind。

正式依赖记录 MIT license 和版本。SDK 的特殊政策声明不改变 MIT 授权，但第三方声明随依赖归档保留。

## 4. Cookie normalization and encryption

`pan115.ParseCookie` 自行执行 allowlist 解析，不把原始浏览器 Cookie 直接交给 SDK：

- 字段名大小写不敏感，输出固定 `UID; CID; SEID; KID` 顺序。
- `UID/CID/SEID` 必填，`KID` 可选。
- 拒绝重复核心字段、控制字符、空值、单字段过长和总长度超限。
- 忽略非 allowlist 字段，不写入 ciphertext，也不发送给 115。

credential purpose 为 `connection:<id>:pan115:cookie`。创建先生成 Connection ID，再加密并持久化；网络 probe 不放在数据库事务中。

## 5. Connection data flow

```text
Vue form
  → strict JSON handler
  → ConnectionService validate + normalize
  → credential.Store.Encrypt
  → SQLite intent/config commit
  → pan115 Driver probe (outside transaction)
  → health/account summary update
  → safe DTO + audit
```

创建可保存一个暂时离线的 Connection，但结构无效的 Cookie 必须拒绝。UI 在保存前提供“测试”，保存后也可重新测试。测试错误只返回稳定代码，例如：

- `pan115_cookie_invalid`
- `pan115_auth_expired`
- `pan115_rate_limited`
- `pan115_unavailable`
- `pan115_response_invalid`

日志只记录 `connection_id`、provider、error code 和 duration。

## 6. Cloud directory picker

API：

```text
GET /api/v1/connections/{id}/directories
GET /api/v1/connections/{id}/directories?token=<opaque>
```

要求 `connections.read` 和 `storages.browse`。响应使用与本地 picker 兼容的 listing shape，但 platform 为 `pan115`。

导航 token 使用现有 credential AES-GCM，claims 绑定：

- token version
- actor ID
- connection ID
- provider item ID
- provider-relative display path
- purpose (`browse|select`)
- expiry

Provider item ID 不视为凭据，但仍不允许浏览器任意构造。Storage create/update 接受 `provider_picker_token`，服务端重新 Stat 目录并验证 token 绑定的 Connection。

## 7. Rate limiting and resilience

每个 Connection 的 runtime handle 共享 limiter：

- list/stat/probe 默认 1 request / 2 seconds，burst 1；
- full reconciliation 使用独立 bulk-tree lane：递归文件分页最多 1,150、后代目录分页最多 5,000、请求启动间隔 500ms、全 Connection 最多 2 个并发，并与其它 lane 共享退避和断路状态；不得用它替代交互目录浏览；
- direct URL 默认 1 request / second，burst 1；
- connection test 不绕过 limiter；
- 429/405/疑似风控按稳定 error kind 进入指数退避；
- 连续失败形成内存断路状态，成功请求关闭断路；重启后从健康状态重新探测。

MediaLibrary 的 provider rate 只能取 `min(library setting, provider maximum)`。

## 8. Scanning and life events

### Full/reconciliation

从 Storage root directory ID 开始分页 BFS/DFS。只保留 provider-relative path，视频扩展名过滤和分类继续复用现有 parser/matcher。每次分页遵守 Connection limiter；中途取消或上游分页异常时返回 `partial=true`，不得删除未见旧条目。

### Life events

新增 connection-scoped event inbox 和 cursor：

```text
provider_events(connection_id, provider_event_id, event_time, kind,
                item_id, parent_id, payload_json, processed_at)
provider_cursors(connection_id, stream, cursor_json, updated_at)
```

保存的 payload 是 allowlist 后的有限 JSON，不含 Cookie、pickcode、临时 URL、文件绝对路径或上游响应正文。游标只在事件写入事务成功后推进。事件按 file ID 幂等映射，再同时唤醒受影响的 MediaLibrary supervisor 和同 Connection 的 115 离线下载 worker；事件拉取失败不影响其它库或下载任务，媒体库周期 reconciliation 与离线任务低频状态查询分别负责补漏。

生活事件协议可能在 115 侧变化，具体 client 封装在 `pkg/cloud/pan115/events.go`，不把 MoviePilot 的业务处理器移植进来。

## 9. Direct URL

`DirectURL` 只接受稳定 file ID + server-side pickcode lookup + User-Agent scope。返回值只在 proxy 层内存使用：

- cache key：connection ID + file ID + UA hash；
- same-key singleflight；
- TTL 不超过 upstream expiry，并提前安全余量失效；
- 获取 URL 的 UA 与最终 302 客户端 UA 一致；显式空 UA 不允许 SDK 自动填默认值；
- 上游 URL、Cookie 和返回 Header 永不持久化或写日志。

本任务只交付 Driver contract 和 adapter，signed proxy/STRM route 由独立任务消费。

## 10. Web UI

`/system/connections` 对用户统一呈现为“数据源”页面，不暴露 Connection / Storage 两个技术分页：

- 同一列表展示 local 与 115 provider root；未完成目录选择的 115 Connection 显示为可继续或删除的待完成项。
- “添加数据源”先选本地或 115。115 可新增 Cookie 账号或复用已有账号，再通过云目录 picker 选择根目录。
- 同一 115 Connection 可对应多个数据源根；更新账号 Cookie 时明确提示会影响该账号下的其它数据源。

Toast 复用现有全局悬浮提示。Cookie 输入框始终为空，placeholder 表示留空保留；浏览器 DOM 不回填已保存 Cookie。

## 11. Compatibility and rollback

- 新迁移只新增表/列/索引，不删除或重命名现有 local 字段。
- local Storage、目录 picker、媒体库 watcher 和 transfer 行为必须保持不变。
- 关闭/删除 115 Connection 不删除云端文件。
- 若 pan115 adapter 不健康，系统保留 Connection/Storage/MediaLibrary 配置并进入安全错误状态；移除 runtime adapter 即可回滚，不需要破坏数据。
- 尚未实现的 capability 固定为 false，UI 不展示写操作、离线下载或 STRM 开关。

## 12. Validation strategy

- Cookie parser、error redaction、Driver mapping 使用单元测试。
- 连接/Storage/媒体库 service 使用 fake cloud Driver，不依赖真实账号。
- handler/router 覆盖认证、双 permission、strict JSON、no-store 和 DTO 脱敏。
- 真实 115 使用 opt-in PowerShell smoke；Cookie 只从环境变量读取，脚本和输出均不回显。
- Windows `server/test.ps1`、Go test/vet/build、Web UI test/typecheck/lint/build 作为最终质量门。

## 13. Native offline downloader

- `pan115_offline` Downloader 只引用 `storage_id`，运行时通过 Storage → Connection 取得共享的连接级 Driver。
- capability 固定为 `native_offline=true`、`output_constraint=provider_storage`，不声明 115 不支持的暂停、恢复、做种或上传速度能力。
- 原生离线任务使用现有 DownloadTask/Job 页面和控制接口；取消始终把 `delete_data` 传给 115。
- 同 Connection 的生活事件通过进程内广播唤醒所有等待中的离线任务立即复核；生活事件不直接宣告任务完成，115 离线任务 API 仍是状态与输出根的权威来源，并以 20 秒低频查询补偿事件延迟或遗漏。等待期间每 10 秒刷新 Job lease，不占用额外持久队列任务。
- 离线任务完成后以 `delete_file_id`（缺失时回退 `file_id`）为输出根，受限分页建立 manifest，用于完成后分类与未来云端整理。
- 离线任务列表页查询按任务缓存页码；重启后的未知任务最多探测有限页数，避免每次进度轮询遍历整个账号离线历史。
