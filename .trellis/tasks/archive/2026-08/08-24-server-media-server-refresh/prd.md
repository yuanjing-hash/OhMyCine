# Server 媒体变更与 Emby/Jellyfin 刷新

## Goal

为 Server 建立统一、持久、可恢复的媒体库变更版本，并让绑定的 Emby/Jellyfin 在媒体真正可用后自动刷新，补齐媒体流水线的媒体服务器通知消费者。

## Requirements

### Authoritative change contract

- 真实 catalog 新增、更新、删除、人工识别修正及所需 artifact 完成必须收敛到统一媒体变更入口。
- 变更版本与 catalog 提交保持原子；依赖 STRM/sidecar 的变更必须经过 readiness barrier 后才可消费。
- partial、failed、superseded、stale generation 和等待冲突状态不发布 ready 变更。
- 变更历史有界保留，可支持 Player 子任务断线补偿；历史过旧时提供安全全量失效信号。

### Media-server targets

- MVP 同时支持 Emby 与 Jellyfin 的连接探测、媒体库枚举和指定媒体库刷新。
- OhMyCine 媒体库可绑定多个明确的上游媒体库，保存稳定 ID 和安全标签，不按名称或路径猜测。
- API Key 沿用 Connection AES-GCM envelope，永不回填、记录或进入 Job payload。
- 目标的创建、编辑、测试、禁用、删除、自动刷新、手动刷新和重试均有权限、审计与安全错误。

### Persistent refresh execution

- 每个目标使用持久系统 Job，并以目标为资源/合并边界读取最新 desired revision。
- 批量导入和 watcher 抖动不会对同一目标发起逐文件刷新。
- 一个目标失败不阻止其它目标，也不阻止 Player 子任务消费 ready change。
- Server 重启后 queued/running/failed 状态按现有队列契约恢复；成功 revision 不重复执行。

### Administration

- Player 管理工作区展示真实连接、绑定、desired/successful revision、运行/失败状态和安全操作。
- 未配置目标时显示明确空状态，不伪造刷新成功。
- `media_servers.refresh` 控制手动执行；配置操作复用稳定 Connection/媒体库权限并在服务层复验。

## Acceptance Criteria

- [ ] 本地媒体入库提交后只产生一个 ready content revision，并自动刷新所有启用目标。
- [ ] 115+STRM 媒体在 artifact 完成前不刷新，完成后才推进 ready revision；失败/partial run 不误通知。
- [ ] Emby 与 Jellyfin 均能枚举受控媒体库并按稳定 ID刷新，连接路径前缀和旧版本兼容有测试。
- [ ] 同一目标短时间收到多次变化时只执行受控次数，最终 successful revision 等于最新 desired revision。
- [ ] 单目标失败、限流、认证失败和网络失败有安全错误与重试，不影响其它消费者。
- [ ] 手动刷新、自动刷新和失败重试复用同一持久 Job/运行记录。
- [ ] 无权限用户不能读写目标或执行刷新；API、Job、日志和审计不泄露 API Key、路径、provider ID 或上游响应。
- [ ] 管理端在白色/深色主题下可配置、测试、查看和重试，并正确表示未知/失败状态。
- [ ] additive migration、fresh DB、upgrade、重复迁移、队列恢复和目标引用删除均有回归。

## Out of Scope

- Player 客户端事件消费与刷新 UX，由兄弟任务 `08-24-player-server-library-refresh` 负责。
- Emby/Jellyfin 用户播放进度、配置同步、Webhook、通知插件或系统级推送。
- OpenList/Alist、CloudDrive2、PT、追更和 Transmission 功能本身。
- 自动创建或修改 Emby/Jellyfin 媒体库目录。

## Key Decisions

- 媒体服务器与 Player 并行消费同一 ready revision，互不等待。
- 目标绑定稳定上游 ID，不以标题或文件系统路径推断。
- 队列 payload 只保存安全 record ID；worker 查询数据库中的最新 desired revision。
