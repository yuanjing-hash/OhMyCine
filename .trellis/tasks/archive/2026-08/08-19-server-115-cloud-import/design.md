# 115 云端自动整理入库设计

## Boundaries

```text
DownloadWorker
  -> private provider manifest (stable item IDs)
  -> package media selection + trustworthy TMDB gate
  -> existing TransferTask + transfer Job
  -> TransferWorker
       -> local executor, or
       -> cloud mutation executor
            -> ConnectionService builds credentialed driver
            -> same-Connection boundary validation
            -> mkdir / move|copy / rename / recycle
  -> dirty_generation
  -> MediaLibrary supervisor reconciliation
```

MediaLibrary 和 DownloadTask 继续拥有路由及策略事实；Downloader 只负责下载和完成清单；TransferService 是唯一写入口。Cloud driver 只执行 provider 原子操作，不决定命名、分类、冲突或任务状态。

所有 provider 在完成后先进入公共 package takeover：以最大可信视频作为电影识别锚点，剧集保留体积合理且可解析季集号的多文件集合，广告/样片被排除，伴随文件只按同目录和 stem 关联。只有任务级 TMDB/Profile 快照达到可信阈值后才生成私有 Transfer manifest。Transfer planner 不再逐文件回退识别；这保证 local、qBittorrent、Transmission、115 与未来网盘离线下载具有相同安全边界。

## Contracts

### Cloud mutation interface

在 `pkg/cloud` 添加可选 `MutationDriver`，使用 provider item ID：

```go
type MutationDriver interface {
    Driver
    CreateDirectory(ctx context.Context, parentID, name string) (Item, error)
    Move(ctx context.Context, itemID, targetParentID string) error
    Copy(ctx context.Context, itemID, targetParentID string) error
    Rename(ctx context.Context, itemID, name string) error
    Recycle(ctx context.Context, itemID string) error
}
```

`Capabilities` 分别声明 create/move/copy/rename/recycle，避免用一个总开关掩盖部分实现。115 adapter 将 SDK 错误映射为稳定 provider code，并让所有 mutation 经过独立限速器及共享风险状态。

### Private manifest and snapshot

`downloader.File` 增加不带公开 JSON 的 provider item/parent/checksum 字段；115 manifest 枚举时填充，qBittorrent 保持为空。DownloadTask 加法迁移保存 target storage type、target provider root ID 和 target connection ID。执行时还会加载 source Storage 并比较 Connection，数据库中的 Connection 凭据不复制进任务。

### Cloud plan

共享模板渲染输出 provider-relative POSIX segments，不先转成 Windows 绝对路径。计划项包含 source item ID、原 parent/name、目标目录 segments、目标 filename、size/checksum 和媒体组。公开 plan summary 只保留清洗后的相对目标、kind、size 和 result。

## Execution and Idempotency

1. 加载 DownloadTask、TransferTask、source/target Storage 和 Connection，验证两端均为 pan115、同 Connection、仍启用，target root 与下载快照一致。
2. 重新 Stat source item 并沿父链验证仍在 source Storage 根内；目标 root 同样验证仍在 target Storage 根内。
3. 逐级 List/ensure 目标目录。Mkdir 超时后先重新 List；只接受唯一同名目录。
4. 计划冲突并按现有 ActionRequest 语义等待或选择策略。
5. 对每个计划项执行 provider 操作，并在每个已确认阶段持久化私有 item checkpoint：`planned -> placed -> renamed -> completed|skipped`。
6. move 重试先 Stat source item：已在目标 parent 时跳过 Move，名称正确时跳过 Rename。
7. copy 在调用前确认目标不存在；调用后按 target parent + original name + size/checksum 唯一识别副本并记录新 ID。超时也执行同样识别；零个结果可重试，多个结果进入非自动重试的歧义失败。
8. overwrite 先验证冲突 item 在 target MediaLibrary root 内并 Recycle；回收成功才放置源。rename 的组后缀选择写入 checkpoint，重启不重新选号。
9. 全部项目完成后在短事务中完成 TransferTask、审计并递增 dirty_generation。

在步骤 1 之前，DownloadWorker 必须完成 package selection 和可信识别。失败时保留 provider 源文件并停止在 Transfer enqueue 之前；因此 ensure directory、冲突回收和 move/copy 都不可达。

外部网络调用不放在数据库事务中。Job lease heartbeat 与每项持久化保持现有队列契约；provider 限流错误返回 `RetryAt` 并释放 slot。

## Compatibility and UI

- v23 仅增加 DownloadTask/TransferTask 私有字段，不改变现有 local 记录；旧任务保持 local executor 行为。
- 下载目标选项开放可写的 115 MediaLibrary，云端库隐藏 symlink。媒体库卡片和媒体整理页面复用现有 mode、conflict、进度、重试、删除历史功能。
- 现有 API summary 不增加 provider item ID 或路径。必要的新字段仅为 storage type/capability 等非敏感展示信息。
- 115 copy 不进入 SeedingTask；现有 qBittorrent copy/symlink 做种行为不变。
- MediaClassificationProfile 增加独立的识别规则 JSON 与四个命名模板字段；分类 schema 仍保持 v1，避免把文件操作配置混进纯分类 matcher。
- 规则管理页增加“分类、识别预处理、命名格式”三个视图。识别规则采用结构化 `enabled/media_type/pattern/replacement`，由 Go RE2 编译并按顺序应用，避免执行任意表达式。
- DownloadTask 增加私有识别规则快照；现有四个命名模板快照改由 Profile 提供。旧 MediaLibrary 模板字段继续保留用于数据库/API 兼容，但新 UI 在 Profile 中编辑命名。
- 旧 Profile/媒体库升级不删除配置；v24 按 `旧 profile_id + 四模板组合` 创建必要的 custom Profile 并重绑媒体库，相同组合复用同一个迁移 Profile，不修改受保护的默认 Profile。新 Profile 与复制 Profile 立即使用 Profile 命名配置，在途 DownloadTask 永远使用已有快照。
- 公共 recognizer 按主文件名、有意义的父目录、provider 包名生成候选；对 movie/tv 各自先执行适用 Profile 规则，再进入内置解析、年份提取与 TMDB 查询。Transfer planner 重新运行同一个 package selector 并要求文件集合一致。

## Safety and Failure Policy

- mutation 权限沿用下载创建、任务控制和管理员媒体库配置边界；provider 写操作只能由已授权 Job 执行。
- source/target 身份、Connection 或根边界无法证明时 fail closed。
- 不自动清理 copy 歧义或半完成结果，避免用删除掩盖不确定性。
- 日志和审计只记录内部任务 ID、数据库资源 ID、动作、计数、耗时和 error code。
- 真实 115 smoke 使用专门创建的隔离源/目标目录；脚本不得接受账号根作为写入或清理目标。

## Rollout and Rollback

- 先完成 fake provider 与 adapter contract，再开放下载目标选择。
- capability 未实现或 connection 不匹配时仍显示明确不可用，不回退到本地上传或路径猜测。
- 回滚时关闭 cloud executor 注册即可停止新写入；已持久化任务和 checkpoint 保留供升级后恢复，不删除用户云端文件。
