# Implementation Plan

1. 新增 schema migration、Connection 模型字段、默认值与迁移测试。
2. 增加 5 段 Cron 校验/next 计算，扩展 Connection create/update/summary/handler/API 类型，并覆盖密码与启用联动测试。
3. 在 cloud contract 中新增全量回收站 capability；为 Pan115 实现无 item ID 的清理及 limiter/error tests，确保精确 purger 不回归。
4. 新增 Connection 级 recycle cleanup service、due supervisor、persistent Job worker、队列 policy、主程序注册与恢复测试。
5. 更新 StorageView 的创建/编辑表单、危险确认、状态展示和前端契约测试。
6. 更新 OpenAPI/架构文档中 115 Connection 的不可逆清理语义和安全边界。
7. 运行定向 Go 测试、`go test ./...`、`go vet ./...`、WebUI lint/typecheck/tests、`git diff --check`。

## Risky Points

- 绝不能把空 item ID 误传给精确 purger；全量接口必须是独立方法。
- Scheduler 重复扫描与多 worker 必须依靠 active-job coalescing 和执行时 revision 校验防重入。
- 保存策略与密码变更要在一个 Connection revision 事务内完成。
- 任何日志、错误包装或审计 metadata 都不得包含操作密码与 115 响应正文。

## Rollback Points

- Driver capability 可独立回滚，不影响已有精确清理。
- Supervisor/worker 可关闭注册；默认关闭的配置不会自行执行。
- UI 可隐藏新设置而保留数据库列和安全默认值。

