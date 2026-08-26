# Design: MediaLibrary Routing and Transfer/Import

## Ownership

MediaLibrary 是用户可选择的最终目标，同时拥有排序、Profile 引用和入库策略。Downloader 只拥有连接能力，全局 DownloadSettings 只拥有本地暂存目录；不新增 DownloadRule。

## Data Flow

```text
submit(media_library_id=0|id)
  -> resolve ordered enabled library
  -> snapshot library/storage/profile/import policy
  -> qBit metadata probe + Profile preclassification/category
  -> global staging download
  -> completed manifest verification
  -> enqueue TransferTask + transfer Job
  -> plan safe target paths
  -> resolve conflict / wait without lease
  -> move|copy|symlink
  -> mark library dirty + reconciliation
  -> copy|symlink: create SeedingTask from snapshotted policy
  -> scheduler samples provider telemetry without occupying worker lease
  -> threshold reached: copy delete task+data; symlink delete task only
```

DownloadTask 只链接其 download Job；TransferTask 一对一引用 DownloadTask 并链接独立 transfer Job。唯一约束和幂等检查保证 download worker 重启不会重复创建 transfer Job。

## Snapshots

DownloadTask 私有字段保存 concrete target library/storage/root、Profile revision/rules、transfer mode、conflict policy 和模板。公开摘要只返回库 ID/名称及非敏感策略。绝对 staging/storage 根只存在于私有数据库字段。

## Conflict State

Transfer worker 先生成确定性的计划。目标存在且策略为 `ask` 时返回 `WaitForAction`，checkpoint 只保存目标相对摘要和选项；用户响应后任务重新入队并重新验证源、目标与边界。其它策略立即执行。

## Rollout

显式 v14 migration 增量添加字段并创建 transfer_tasks；MediaLibrary 排序回填为原 ID 顺序。历史 DownloadTask 的 target 字段为空，不生成 transfer Job。首版只执行 local Storage；未来 cloud driver 通过相同 TransferTask/strategy contract 扩展。

v15 migration 创建单例 SeedingSettings、SeedingTask 及 DownloadTask 做种策略快照。SeedingTask 持久化 provider task ID、transfer mode、清理语义、阈值快照、最后采样和终态，Job payload 只保存 seeding_task_id，不保存路径。做种设置默认关闭；时长与分享率默认按 `all` 组合，阈值 0 表示该条件未启用。

Transfer source resolution 先查找 `staging/category/relative`，再查找 `staging/relative`。每个候选都必须在规范化后逐级检查 reparse point；不安全的分类候选不能直接导致选用未检查的根目录候选。
