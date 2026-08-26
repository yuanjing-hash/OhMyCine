# Design: Downloader Management

## Boundaries

- `Downloader` 是管理员维护的外部下载能力连接，独立于 Storage、MediaLibrary 和后续 DownloadRule。
- Server 持有一份独立于 Storage 的统一本地下载暂存绝对目录；本地 downloader 不保存 staging Storage，也不保存最终 MediaLibrary。
- `DownloadTask` 保存用户归属、通用 Job ID、downloader ID、provider task ID、阶段和 telemetry；通用 `Job` 仍负责 lane、lease、重试、顺序和等待操作。
- `pkg/downloader` 定义 provider-neutral client；`pkg/downloader/qbittorrent` 隔离 qBittorrent Web API 兼容细节；fake adapter 仅用于测试与开发验收。

## Secret Boundary

- Server 启动时从 `OMC_CREDENTIAL_MASTER_KEY`（Base64 32 bytes）或 `OMC_CREDENTIAL_KEY_FILE` 加载 key；未配置时在数据库同目录生成 owner-only key file。
- Downloader username/password 与下载源分别使用 AES-256-GCM envelope，并绑定用途/记录 ID 的 AAD。
- API DTO 只返回 `username_configured` / `password_configured`；更新时空密码表示保留，显式 `clear_password` 才清除。
- magnet/URL/种子字节不进入 Job payload、checkpoint、日志、审计或 WebSocket。Job payload 只含 `download_task_id`。`.torrent` 通过同源 JSON 的受限 Base64 字段进入内存，解码后最大 4 MiB，不写公开临时文件。

## Data Flow

```text
Admin downloader config -> validate URL/provider -> encrypt credentials -> SQLite downloader
Admin download settings -> global directory picker token -> canonicalize and reject symlink/Reparse Point -> SQLite singleton setting
User submit -> resolve unified staging setting -> validate source -> encrypt source + create DownloadTask + enqueue download Job
Scheduler -> DownloadWorker -> decrypt source/config -> adapter submit/reconcile
adapter telemetry -> DownloadTask + Job heartbeat -> REST fact + queue WebSocket delta -> Web UI
```

```text
manual source + Profile snapshot
  -> qBit metadata probe (`stopCondition=MetadataReceived` when supported)
  -> provider name/file manifest
  -> filename/episode parse
  -> bounded TMDB search/detail
  -> existing classification.Classify(Profile snapshot)
  -> qBit category ensure/set
  -> resume content download
  -> completed manifest recheck + persisted scrape summary
  -> future ImportPlan/Transfer handoff
```

## Adapter Contract

统一 adapter 暴露 `Test`、`Submit`、`Get`、`Pause`、`Resume`、`Cancel(deleteData)` 和 capability；Submit source 是 URL/magnet 或内存中的 torrent bytes。未知 telemetry 使用 nil。HTTP client 固定超时、禁用自动重定向、限制响应体并只允许 HTTP(S) origin。

qBittorrent 使用短生命周期登录 Cookie，不持久化 SID。提交前先按稳定 `omc-<task id>` tag 查找既有任务；提交响应兼容旧 `200 + Ok.` 与新 `200/202 + JSON`，新版优先使用 `added_torrent_ids`，随后仍可按 tag 对账。裸 magnet 在支持时设置 `stopCondition=MetadataReceived`；adapter 暴露 bounded torrent metadata/file manifest、category list/create/set。取消调用 delete API 且 `deleteFiles=true`。pause/resume 对 qBittorrent v4/v5 endpoint 做有限兼容回退。

TMDB 是受控 metadata provider：有限超时/响应大小、禁用重定向，只发送解析后的标题、年份、语言和地区，不发送 magnet、绝对路径、凭据或完整 provider 文件路径。credential envelope 显式保存 `read_access_token|api_key`；前者使用 Bearer，后者使用 `api_key` query，禁止格式猜测。凭据优先级为“Server AES-GCM 用户凭据 → 部署环境凭据 → 构建期应用凭据”；设置 DTO 只返回 `credential_source=custom|deployment|builtin|none`、`credential_kind` 等安全状态。运行环境使用互斥的 `OMC_TMDB_READ_ACCESS_TOKEN` / `OMC_TMDB_API_KEY`；构建期通过互斥的 `OHMYCINE_TMDB_READ_ACCESS_TOKEN` / `OHMYCINE_TMDB_API_KEY` 和独立 linker 变量注入。正式 CI 缺少两者或同时配置两者时失败；启动器在 npm/Vite 之前移出 build-only 环境变量，只保留非导出的 linker 参数值。该应用凭据按最终二进制可提取、可撤销、只读和独立限流管理。v11 为旧记录增加默认 `read_access_token` kind，不解密或重写既有密文。

默认 API routes 为 `https://api.tmdb.org/3`、`https://api.themoviedb.org/3`，仅 timeout/DNS/connect 等纯网络错误尝试第二项；任何 HTTP 响应、尤其 401/403，不触发跨域重试。用户 API route 只使用通过测试并持久化的单一 HTTPS origin/path prefix。图片 route 独立配置，使用固定小尺寸 TMDB 图片做 bounded content-type/size 测试；API 测试使用固定 movie detail ID 并校验响应 ID。凭据/API route 在发出外部 probe 前先校验当前 revision，probe 成功后仍按 revision CAS 保存；过期管理请求不得把候选凭据或当前有效凭据发送到任何元数据地址。失败不改变另一项或旧值。预分类快照 Profile rules，分类证据与置信度使用 allowlisted DTO。

现有 Profile matcher 只处理已结构化 TMDB metadata。下载服务复用 `medialibrary.ParseFilename` 和 `classification.Classify`；缺少凭据、认证/网络失败、无 TMDB match、歧义、低置信或规则 fallback 时记录安全原因码，自动映射到 qBittorrent `未识别` category 并恢复下载，不创建 `download_classification` ActionRequest。高置信分类名称仍映射到 Profile category；缺失 category 只在统一暂存根内创建受管路径，同名 category 指向边界外时拒绝恢复下载。

## Queue And Recovery

- `download` worker 使用 downloader ID 作为 resource key，遵守现有 type/provider 并发限制。
- worker 每次 claim 都从 DB 读取 DownloadTask；已有 provider task ID 时直接 reconcile，不重复提交。
- transient upstream failure 进入 `retry_wait` 并释放 slot；provider 完成后 Job/DownloadTask 完成。
- running Job 在 Server 重启后由 lease recovery 重排，随后通过 provider task ID 恢复。
- pause/cancel 对 running Job 先记录 interrupt intent；interrupt-capable worker 完成 provider 操作后再确认中断。失败时清除 pending intent、保留 running 并记录安全错误。
- 非运行 provider download 的 pause/cancel 转成持久化 `queued + interrupt_status`，并关闭尚未响应的 ActionRequest。Scheduler claim 后先执行 provider Interrupt；成功按 lease 确认 paused/cancelled，失败清除 intent 后继续 worker。租约过期/Server 重启只重排 queued，不推断 provider 已成功。
- cancel interrupt 固定调用 `Cancel(..., true)`；provider 成功或明确 task-not-found 后先确认队列状态，再通过 acknowledgement hook 事务删除 DownloadTask、Job 及级联依赖。provider 失败则保留原状态和本地事实。

## API And RBAC

- 管理配置：`downloaders.read/create/update/delete/test`，仅管理员/运维角色默认拥有。
- 统一暂存目录：`GET/PATCH /api/v1/settings/downloads`，使用 `settings.read/update`；受保护且禁止缓存的 GET 返回当前绝对路径供管理员核对，PATCH 只接收短期目录选择 token 和 revision，不接受浏览器自由拼接路径。`GET /api/v1/settings/downloads/directory` 额外要求 `storages.browse`，并从 Server 已保存的当前路径打开选择器。
- 下载事实：沿用 `downloads.read_own/read_all/create/manage_all` 与 `jobs.*` 的 owner/all 规则。
- Routes: CRUD `/api/v1/downloaders`、`POST /downloaders/:id/test`、`POST /downloads`、`GET /downloads`。
- `DELETE /api/v1/downloads/:id` 仅允许 failed/cancelled；own scope 需要 owner + `jobs.control_own`，跨用户需要 `downloads.manage_all`。它先确认 provider 数据删除，再删除本地事实；provider 已不存在视为幂等成功。
- REST 是恢复事实；现有 jobs WebSocket 发送增量，不发送凭据、源 URI 或 provider 响应正文。

## Rollout And Compatibility

- 新增显式 SQLite migration，不改写旧表。
- 下载器删除只删配置；存在活跃 DownloadTask 时拒绝。
- 本轮不做最终媒体库选择或跨存储传输；取消/删除下载任务会在显式二次确认后删除 provider 下载数据并写审计。
- 旧 `downloaders.storage_id` 列保留兼容但不再读写；迁移将已有配置中首个有效 local Storage 作为统一暂存根的兼容默认，避免破坏现有任务。
- 全局通知由 App 根组件托管，下载页面只发布安全消息；下载器卡片从 downloader health 与当前 DownloadTask 聚合得到，不新增高频 provider 请求。
