# 增加115定时清空回收站

## Goal

在 115 数据源的账号连接配置中增加默认关闭的“定时自动清空回收站”，允许用户输入/更新 115 操作密码并使用 5 段 Cron 设置周期，按明确授权永久清空该账号的整个 115 回收站。

## Evidence

- MoviePilot `p115strmhelper` 使用 `clear_recyclebin_enabled`、`password` 和 `cron_clear`，默认 Cron 为 `0 */7 * * *`；执行时仅提交密码，不传条目 ID，语义是清空整个回收站。
- OhMyCine 已有 `RecycleCredentialCiphertext`、`recycle_password` API、`SecretInput` 和 `recycle_password_configured`，并通过 AES-GCM 保存；不得新建平行密码字段。
- 现有 `ExactRecyclePurger.PurgeRecycle(itemID)` 只永久清除 OhMyCine 记录的单个临时播放副本；全量清空必须使用名称和接口都明确不同的新 capability。

## Requirements

- 配置归属 115 Connection，而不是 Storage；同账号多个数据源只调度一次。
- UI 提供“定时自动清空回收站”开关、`115 操作密码（安全码）` SecretInput、5 段 Cron 输入和不可恢复的红色警告；默认关闭，默认 `0 */7 * * *`。
- 首次从关闭变为启用时必须二次确认；启用必须已有或本次提交有效操作密码和合法 Cron。
- 密码留空保留旧值，显式移除才清空；启用状态下不得单独移除密码。
- 连接摘要返回启用状态、Cron、上次运行时间、上次状态、稳定错误码和下次运行时间，不返回秘密。
- 使用持久 Job 队列执行；调度扫描可以重复运行，但同一 Connection 只能有一个 active job。
- Job payload 仅冻结 Connection ID 和 revision；Worker 执行前重读并核对 provider、连接启用、策略启用、revision 和密码 configured。
- 新增 `RecycleBinCleaner.ClearRecycleBin(ctx)` capability；115 实现调用 SDK `CleanRecycleBin(password)`（不传 item ID），不得改变 `ExactRecyclePurger` 的精确语义。
- 调用沿用 115 destructive lane、共享并发槽和 405/429 处理；失败不得记录密码或上游响应正文。
- 成功/失败后更新上次状态并从 Cron 计算下一次时间；Server 重启后从数据库恢复 due 调度。
- 配置修改和调度执行均写审计；仅连接管理权限可配置，调度任务以 system actor 执行。

## Acceptance Criteria

- [ ] 新建或编辑 115 数据源时可安全设置操作密码；普通 API 只返回 configured 布尔值。
- [ ] 默认关闭；未配置密码、Cron 无效或未完成二次确认时无法启用。
- [ ] `0 */7 * * *` 能产生正确的下次时间，页面展示上次/下次状态且刷新后保留。
- [ ] 多个 Storage 共用同一 Connection 时只出现一份设置和一个 active 清理 Job。
- [ ] Worker 调用全量清空接口时不传 item ID；精确临时副本清理测试保持不变。
- [ ] 禁用连接/策略、删除/替换密码或 revision 变化后，旧 Job 不执行破坏调用。
- [ ] 重启、重复轮询、并发调度不会对同一账号并发清空；失败在下一 Cron 周期可再次执行。
- [ ] 日志、审计、Job payload、事件和 DTO 中不存在操作密码、Cookie、provider ID 或真实路径。

## Out of Scope

- 手动“立即清空”按钮。
- 清空最近接收目录。
- 自动永久清除刚整理后的来源空包。
- 在非 115 Connection 上提供该设置。

