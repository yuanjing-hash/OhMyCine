# 实施计划

## 有序步骤

1. 冻结当前 Discovery/SiteService/TMDB/catalog API 和测试基线，确认 OpenAPI 文件是否存在。
2. 扩展 TMDB 安全元数据读取，提供受限别名/翻译名称生成器及单元测试。
3. 在 service 层实现媒体关键词搜索和稳定身份校验，复用现有 Discovery 图片网关/详情 DTO。
4. 扩展 SiteService 内部媒体身份搜索：名称预算、站点并发、部分失败、识别、跨名称去重、稳定排序和 opaque claim。
5. 添加身份资源 JSON/SSE handler/route，并补认证、权限、取消、错误和不泄密测试；保留原始关键词 API。
6. 实现 actor-scoped `MediaCoverageService`，覆盖电影、电视剧、Season 0、future/unknown、跨库去重和 freshness。
7. 添加 coverage API 和 router/handler/service 集成测试。
8. 更新 Web UI typed client、路由和探索默认媒体海报搜索；保留高级原始资源搜索。
9. 扩展统一详情页的电影存在/电视剧季集覆盖 UI，补键盘、响应式、loading/unknown/error 测试。
10. 运行跨层安全复核，更新 Server 架构/roadmap 和存在时的 OpenAPI。

## 重点文件/模块

- `server/pkg/metadata/tmdb/`
- `server/internal/services/discovery.go`
- `server/internal/services/site.go`
- `server/internal/services/media_catalog.go` 或独立覆盖率 service
- `server/internal/handlers/discovery.go`
- `server/internal/httpserver/router.go`
- `server/webui/src/discovery.ts`
- `server/webui/src/views/ExploreView.vue`
- `server/webui/src/views/DiscoveryDetailView.vue`

避免在大 view 中堆积名称/覆盖率算法；提取 typed helper/composable/component。不得改写不相关的下载、Transfer 或 Player 代码。

## 验证

```powershell
cd server
go test ./pkg/metadata/tmdb ./internal/services ./internal/handlers ./internal/httpserver
go test ./...
go vet ./...
cd webui
npm test
npm run typecheck
npm run lint
npm run build
```

实施过程中至少单独运行覆盖以下用例的定向测试：

- 中文名/繁体别名/原名/英文名顺序、去重和硬上限。
- 一个别名或站点失败、context 取消、重复资源和异步稳定排序。
- 电影 present/missing/unknown。
- 跨库同集去重、未播、无 air date、partial scan、Season 0、无权限库。
- 推荐卡与海报搜索卡同路由、高级原始搜索不扩展别名。
- DTO/日志不出现路径、Cookie、passkey、torrent/magnet URL。

## 完成门

- 子任务 2 可仅通过内部 `MediaIdentitySearch` 与 `MediaCoverage` 契约获取搜索和缺集事实，无需复制实现。
- 所有 acceptance criteria 有服务或 UI 回归测试映射。
- 全量 Server/Web UI 质量门通过后才能归档并进入子任务 2。

