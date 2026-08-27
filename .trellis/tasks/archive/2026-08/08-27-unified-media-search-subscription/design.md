# 技术设计

## 1. 任务边界

父任务只管理跨子任务契约和最终集成，不直接承载产品代码。实现拆成两个按顺序交付的子任务：

1. `08-27-media-identity-search-coverage`：建立稳定媒体身份、TMDB 海报搜索、统一详情、多名称资源聚合和跨媒体库覆盖率。
2. `08-27-automatic-tv-subscriptions`：在第一阶段契约上实现执行策略快照、订阅 CRUD、调度、缺集对账、自动择优下载和进度管理。

第二个子任务明确依赖第一个子任务提供的媒体身份、多名称搜索和覆盖率服务。父任务不启动；先启动并完成子任务 1，再规划状态不变地启动子任务 2，最后回到父任务做跨层集成复核。

## 2. 目标架构

```text
Web UI 关键词
  → TMDB media search
  → media_type + tmdb_id
  → unified detail
      ├─ library coverage
      ├─ identity-aware resource search
      ├─ one-click download
      └─ TV subscription editor
             → versioned execution snapshot
             → persistent follow-search Job
             → aired missing-episode reconciliation
             → shared identity-aware resource search
             → deterministic selection
             → existing DownloadService
             → recognition → Transfer → import → refresh/notify
```

核心原则：作品身份、媒体库事实和执行策略分别建模。页面标题不是下载授权；自动任务只使用重新从 TMDB 验证的 `media_type + tmdb_id`、确定的已播缺集集合和当前订阅修订。

## 3. 跨子任务契约

### 3.1 MediaIdentity

```text
provider = tmdb
media_type = movie | tv
tmdb_id > 0
title / original_title / release_year 仅为安全显示快照
```

任何详情、覆盖率、资源搜索和订阅 API 都以 `media_type + tmdb_id` 为权威键。后端必须 `GetByID` 复验，不能信任浏览器回传标题、别名或季集总数。

### 3.2 SearchNames

子任务 1 提供共享的受限查询词生成与身份资源搜索服务。查询词来自 TMDB 的本地化标题、替代标题、原名和英文翻译，规范化、去重并受数量上限约束。自由文本资源搜索继续使用独立入口，不自动扩展名称。

子任务 2 只能复用该内部服务，不能另写一套订阅专用站点搜索或标题算法。

### 3.3 MediaCoverage

覆盖率是按当前 actor 可读取且启用的媒体库实时/短缓存投影：

- 电影：`present | missing | unknown` 和命中的安全媒体库摘要。
- 电视剧：TMDB 季集集合、逐集 `present | missing | future | unknown`、每季汇总、全剧汇总、数据新鲜度。
- 未扫描、身份不明、TMDB 季集不可用或日期不确定时为 `unknown`，不得转换为自动下载目标。
- Season 0 可显示但默认不进入缺集目标；只有用户显式订阅特别篇时才参与自动化。

子任务 2 使用同一服务计算缺集，不维护会漂移的第二份缺集算法。

### 3.4 SubscriptionExecutionSnapshot

订阅保存版本化执行策略：目标季、按优先级排序的站点 ID、下载器 ID、目标媒体库 ID、运行周期、质量/包含/排除规则、单次资源上限和下载优先级。只保存稳定引用和非敏感规则；凭据、torrent/magnet URL、绝对路径和临时 token 永不进入快照。

全局默认仅用于创建表单预填。保存后行为由快照决定；编辑增加修订号，已经入队的 Job 使用其 payload 中的启动修订，下一次 Job 才读取新修订。

## 4. 数据和运行一致性

- 所有新增 SQLite 迁移均为 additive、显式版本、可重复检查，并补 fresh/upgrade/idempotency 测试。
- 外部 TMDB/PT/下载器调用不持有数据库事务；事务只用于短时间保存意图、配置修订、幂等 claim 和结果。
- 同一用户、同一电视剧、同一季只有一个未删除订阅 claim；并发创建由数据库唯一约束裁决。
- follow-search Job 使用现有持久队列的 `resource_key/coalescing_key` 阻止同一订阅并发运行，并继承 lease、恢复、重试和安全任务 DTO。
- 每集保留与下载任务的幂等关联；入库事实或活动下载存在时不重复提交。
- 下载提交复用现有 `SiteService` 的受控资源解析和 `DownloadService` 的内部可信身份覆盖，不把真实下载地址带回浏览器或持久订阅配置。

## 5. 权限与安全

- 详情和覆盖率按当前用户的媒体库读取权限过滤。
- 订阅对象按 owner 隔离；`follows.read_all` 只放宽管理员列表读取，不隐式授权更新、删除或执行。
- 补充 `follows.update_own/update_all/delete_own/delete_all/execute_own/execute_all` 等操作权限，并同步权限目录、种子角色、生成的前端权限和测试。
- 自动任务以订阅 owner 执行下载权限和目标资源授权复验；配置被停用、删除或失去权限时进入可见 blocked 状态，不沿用旧授权。
- API、Job metadata、审计、事件和日志只使用安全 DTO/error code，不包含 Cookie、passkey、下载器密钥、真实 torrent URL、绝对路径或上游响应体。

## 6. 兼容、上线与回滚

- 保留当前 PT/BT 原始关键词 JSON/SSE 搜索和下载确认流程；新增媒体身份搜索是并行契约，Web UI 默认入口改为先搜海报。
- 推荐卡片、TMDB 搜索卡片、相似/相关卡片继续使用同一详情路由；旧详情深链保持可用并规范化到 TMDB 身份。
- 没有订阅数据的升级数据库行为不变。若订阅 Worker 故障，可停用新 Job policy/入口而不影响手工搜索、手工下载和既有 Download/Transfer worker。
- 数据回滚不自动删除订阅或下载记录；代码回滚前停止新增 follow-search Job，保留 additive 表供之后恢复。
- 仓库当前没有 `server/api/openapi.yaml`；实施前再次确认，若文件出现则两个子任务都必须同步更新。

## 7. 集成质量门

父任务在两个子任务完成后检查：

- 同一作品从推荐和 TMDB 搜索进入完全相同的详情和覆盖率。
- 手工身份搜索与订阅搜索使用同一查询词生成、资源识别、去重和排序基础。
- 订阅选中的资源确实经过现有下载、识别、Transfer、入库和刷新链路。
- Web UI、API、Job、数据库和事件中的身份/修订/owner 字段端到端一致。
- 未知覆盖、未来集、重复运行、并发立即执行、配置失效和部分站点失败均有回归测试。
