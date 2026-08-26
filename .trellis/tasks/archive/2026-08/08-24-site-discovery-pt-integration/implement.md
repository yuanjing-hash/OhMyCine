# 实施计划

1. 收敛父子 PRD、MoviePilot 清洁室参考和安全边界。
2. 添加 additive 数据迁移、站点/推荐缓存模型及迁移测试。
3. 实现 PT adapter 接口、PTTime 解析器、受控 HTTP 客户端和 fixture 测试。
4. 实现站点配置、候选测试/CAS、健康状态、限速和审计服务/API。
5. 扩展 TMDB 趋势/Discover；实现豆瓣公开 provider、缓存与来源级故障隔离。
6. 实现 PT 聚合搜索、短期不透明结果令牌、SSE/JSON 输出和单站重试。
7. 实现安全下载桥接，复用现有 `DownloadService.Submit`，补权限、日志和脱敏测试。
8. 实现推荐、探索和站点管理三页，移除 planned 状态，补 API client、类型和交互测试。
9. 更新 OpenAPI/架构/路线图与长期 Trellis spec。
10. 运行 `go test ./...`、Web UI test/typecheck/lint/build、`git diff --check`；最后做推荐 → PTTime → 下载创建的集成检查。

## 风险文件与合并策略

- `models.go`、`migrations.go`、`handlers/api.go`、`router.go`、`cmd/server/main.go`、Web UI router/navigation 当前可能有并行改动；使用小块 additive patch，修改前后检查 diff，不覆盖识别器或媒体刷新代码。
- 外部真实服务不可作为自动化测试前提；网络行为用固定脱敏 fixture 和本地测试服务器覆盖，真实 PTTime/TMDB/豆瓣仅做用户凭据下的运行验收。

## 回滚点

- 每层先通过单元测试再连接下一层；任何 provider 不稳定时保留接口与缓存，UI 单独显示来源故障，不关闭另一来源。
- 下载桥接失败时停止在 result token 解析，不创建半成品 `download_task`。
