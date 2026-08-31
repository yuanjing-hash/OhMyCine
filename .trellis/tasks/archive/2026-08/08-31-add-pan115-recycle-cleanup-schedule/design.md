# Technical Design

## Boundaries

- Connection 继续拥有 115 Cookie、操作密码和账号级回收站策略。Storage 不复制策略。
- `cloud.ExactRecyclePurger` 保持单 owned item 语义；新增 `cloud.RecycleBinCleaner` 表达全账号不可逆清空。
- Connection service 负责配置校验、密文生命周期、revision 和安全摘要；新的 recycle cleanup service 负责到期扫描、入队和运行状态。

## Persistence and API

- 在 `connections` 上增加策略及状态列：enabled、cron、next_run_at、last_run_at、last_status、last_error_code。它们与 Connection revision 一起更新，避免策略另有并发版本。
- 迁移为现有 115 Connection 写入默认关闭、Cron `0 */7 * * *`、状态 `idle`；非 115 Connection 保持关闭。
- Create/Patch Connection 接受可选的 recycle cleanup 字段。Summary 返回非敏感状态字段；密码仍只暴露 `recycle_password_configured`。
- Cron 使用标准 5 段格式，按 Server 本地时区求下一次，再以 UTC 持久化；WebUI 用浏览器本地时间展示。

## Scheduling Flow

1. 轻量 supervisor 每 30 秒查询已启用且 `next_run_at <= now` 的 115 Connection。
2. 对每个 Connection 查找相同 resource/coalescing key 的 active Job；存在则复用，不重复入队。
3. 新 Job payload 只含 Connection ID 和 revision，队列资源键为稳定的 Connection ID。
4. Worker 重读 Connection并重新验证 provider、enabled、cleanup enabled、revision、Cron 和密码 configured。
5. Worker 从 ConnectionService 获取已解密 driver，通过 `RecycleBinCleaner.ClearRecycleBin` 执行全量清空。
6. Worker 无论成功或失败都持久化 last 状态，并从当前 Cron 计算 future next_run_at；安全失效的旧 revision 只结束 Job，不调用 provider。

## Safety and Observability

- WebUI 从关闭切换到启用时弹出不可恢复确认；保存前校验密码与 Cron。
- 自动清理不提供立即执行端点，降低误触范围。
- 同一 Job resource 单飞；115 调用继续走 destructive limiter 和共享调用槽。
- 审计只含 Connection ID、revision、结果、稳定错误码和触发类型；日志不记录秘密或回收站内容。
- 关闭策略不会撤销已入队记录，但 Worker 的执行时重校验会让旧 Job 安全 no-op。

## Compatibility and Rollback

- 旧数据迁移后功能关闭，行为不变。
- 删除/回滚 UI 和 supervisor 不会触发清空；数据库新增列可保留而不影响旧代码读取。
- 精确临时副本永久清理接口和调用路径不修改。

