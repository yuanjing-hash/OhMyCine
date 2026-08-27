# 技术设计

## 1. 依赖与模块

本任务在子任务 1 完成后实施，直接依赖其：

- `MediaIdentity` 验证和 SearchNames 生成；
- actor-scoped `MediaCoverage`/逐集状态；
- 内部身份资源搜索和稳定候选结构；
- 统一详情的季卡组件/数据结构。

新增模块建议：

- `models`: follow subscription/run/episode claim。
- `FollowService`: CRUD、revision、owner/RBAC、默认预填、调度扫描、进度投影。
- `FollowSearchWorker`: 缺集对账、搜索、过滤、选择、DownloadService 交接。
- handlers/routes: `/api/v1/follows` 资源和 action API。
- Web UI: follow typed client/store/helper、详情季选择/编辑对话框、订阅管理列表/详情。

handler 保持薄；站点、下载器、媒体库验证、外部调用和自动选择都在 service/worker。

## 2. 持久化模型与迁移

使用迁移 head 之后的新 additive 版本（当前规划观察为 v51，实施时重新确认）：

### `follow_subscriptions`

```text
id UUID PK
owner_id FK users RESTRICT
media_type CHECK tv
tmdb_id
title / year / poster_ref              安全显示快照
status active|paused|completed|blocked
revision >= 1
execution_snapshot_json                版本化、无敏感值
progress_*                             已播目标/已入库/缺失安全计数
last_run_id / last_run_at / next_run_at
last_error_code / last_error_message   安全文案
created_at / updated_at
```

### `follow_subscription_seasons`

```text
subscription_id FK CASCADE
owner_id, tmdb_id, season_number
special
PRIMARY KEY(subscription_id, season_number)
UNIQUE(owner_id, tmdb_id, season_number)
```

唯一约束使 paused/completed 订阅仍持有季；删除订阅后 cascade 释放。多季创建、snapshot 和 season rows 在同一短事务提交。

### `follow_runs`

```text
id UUID PK
subscription_id / owner_id
subscription_revision
execution_snapshot_json                该 run 的不可变安全策略
job_id UNIQUE
trigger scheduled|manual
status
missing_snapshot_json                  仅 season/episode 坐标
searched_names_count / candidates / selected
safe_filter_summary_json
error_code / error_message
started_at / finished_at / created_at
```

### `follow_episode_claims`

```text
subscription_id, season_number, episode_number PK
state missing|queued|downloading|imported|failed
run_id / download_task_id
resource_fingerprint                   服务端不可逆稳定摘要
updated_at
```

该表是自动下载幂等读模型，不保存 torrent URL/token。失败后下一次运行在重新核对 catalog 和活动 download 后更新同一行，不插入平行 claim。

迁移需覆盖 fresh、v51 upgrade、重复执行、FK/index/check/unique 约束；不改写现有任务或媒体数据。

## 3. 执行策略快照

版本化 JSON 示例：

```json
{
  "version": 1,
  "seasons": [1, 2],
  "site_ids": ["site-a", "site-b"],
  "downloader_id": "...",
  "media_library_id": 1,
  "schedule": {"kind": "interval", "minutes": 360},
  "filters": {
    "resolutions": ["2160p", "1080p"],
    "video_codecs": ["hevc", "av1"],
    "qualities": [],
    "include_keywords": [],
    "exclude_keywords": [],
    "release_groups": [],
    "exclude_release_groups": [],
    "min_seeders": 1,
    "max_age_hours": null,
    "min_size_bytes": null,
    "max_size_bytes": null
  },
  "max_resources_per_run": 3,
  "download_priority": 0
}
```

所有字符串/数组/数值有长度、数量和范围上限。站点顺序即优先级。快照引用 ID，不复制凭据或路径。API 返回可编辑的同一安全结构；默认值 endpoint/表单只根据当前可用配置构建预填，不是运行时依赖。

编辑采用 expected revision/CAS。入队事务先创建包含当前 revision 与完整安全策略快照的 `follow_run`，Job payload 保存 `{run_id, subscription_id, subscription_revision, trigger}`。worker 始终执行 run 的不可变快照；普通编辑只影响下一次运行。worker 在开始和每次提交下载前验证订阅仍存在且未暂停/删除，状态失效时安全结束。

## 4. API 与权限

计划 API：

```text
GET    /api/v1/follows
POST   /api/v1/follows
GET    /api/v1/follows/{id}
PUT    /api/v1/follows/{id}
DELETE /api/v1/follows/{id}
POST   /api/v1/follows/{id}/pause
POST   /api/v1/follows/{id}/resume
POST   /api/v1/follows/{id}/search
GET    /api/v1/follows/{id}/runs
GET    /api/v1/follows/defaults?media_type=tv&tmdb_id=<id>
```

列表分页。own/all 权限在 service 层重复约束；create 不能替代 downloads.create 或目标库权限。计划补充：

```text
follows.update_own / follows.update_all
follows.delete_own / follows.delete_all
follows.execute_own / follows.execute_all
```

创建订阅需要 `follows.create`，实际自动提交还要以 owner 重新验证下载权限、站点可用性和目标媒体库授权。角色种子和生成前端权限同步更新。

## 5. Scheduler 和 Worker

### 5.1 入队

Scheduler 按短固定 tick 查询 `status IN (active, completed) AND next_run_at<=now`，调用现有 QueueService。`completed` 运行先做低成本 TMDB/coverage 复核，只有出现新的已播缺集才重新进入搜索：

```text
job_type      = follow-search
owner_id      = subscription.owner_id
resource_key  = follow:<subscription_id>
coalescing_key= search
priority      = snapshot/download scheduling priority 的受限映射
payload       = run_id + subscription_id + revision + trigger
```

同一订阅已有 active Job 时立即执行返回该 Job 或幂等冲突，不创建并发运行。

### 5.2 Worker 阶段

```text
load + authorize configuration
→ GetByID + validate revision/status
→ shared MediaCoverage reconciliation
→ build definitely-aired missing set
→ shared identity-aware SiteService search
→ season/episode recognition
→ snapshot filters
→ deterministic set-cover selection
→ transactional episode claims
→ resolve selected private site results
→ existing DownloadService submit
→ persist run + emit safe events
```

外部网络调用不持有 SQLite transaction。提交每个下载前重新检查：订阅仍存在且未暂停/删除、目标集仍 missing、没有 active download/claim、资源引用未失效。revision 变化不改写当前 run 的不可变快照；若 private search reference 过期，只在本次 worker 内重新搜索，不持久化真实 URL。

结果选择使用确定性集合覆盖：优先覆盖最多尚未 claim 的缺集，再应用站点/质量/健康度/发布时间和 stable fingerprint tie-break，直到覆盖目标或达到 `max_resources_per_run`。候选必须有明确作品和季集集合；模糊命中不进入自动选择。

### 5.3 进度与恢复

- follow-search Job 遵循现有 lease/heartbeat/retry；checkpoint 只保存阶段、缺集坐标和安全 candidate fingerprint。
- 下载提交成功后 episode claims 记录 task ID。失败在安全状态中可重试，且提交前查询现有任务避免 crash-after-submit 重复。
- 下载/Transfer/MediaLibrary 扫描完成可触发轻量 reconciliation；即使事件丢失，下次定时运行也能从 catalog 收敛。
- `completed` 表示当前目标季已播集补齐，不是永久停止。Scheduler 仍调度轻量 coverage 复核，发现新已播集后回到 active。

## 6. Web UI

- 详情季卡增加“订阅”入口；选择多季后打开执行策略 drawer/dialog。
- 表单显式展示所有快照项、当前 defaults 来源和“保存后不随默认变化”的说明；无可用下载器/目标库/站点时禁用提交并导航配置页。
- 订阅管理页替换导航占位，提供过滤、进度卡、状态、下次运行、最近结果和 actions。
- 编辑表单使用 revision CAS，冲突时重新加载而不是覆盖别人/另一个标签页的修改。
- 运行详情只显示安全查询名称、过滤原因计数、选中资源标题摘要和关联 download task 链接，不显示真实来源 URL。
- mobile/desktop、键盘焦点、confirm delete、loading/empty/error/blocked states 均验证。

## 7. 安全、运行与回滚

- follow 表和 Job payload 是非敏感区；CI/测试搜索禁止字段模式和样例 URL/路径。
- 事件按 owner/RBAC 过滤，高频进度节流；审计详情不记录过滤关键词原文之外的上游数据，敏感匹配内容做数量摘要。
- 删除订阅只删除/归档 follow 配置和季 claim，不取消下载、不删除文件；明确审计。
- 可通过禁用 follow scheduler/worker registration 和隐藏创建入口停止自动化；additive 表保留，手工搜索/下载与现有流水线继续运行。
