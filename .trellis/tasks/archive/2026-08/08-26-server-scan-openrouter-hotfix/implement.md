# Implementation Plan

## 1. Reproduce media-library persistence failure

- [ ] 建立 Transfer 完成后 10 集 TV 包进入本地媒体库的 SQLite 集成 fixture。
- [ ] 分别覆盖 event scan 与 manual scan，并先确认现有代码可重现事务失败或确定触发失败的最小 schema/state 条件。
- [ ] 断言失败前识别确实成功，避免把识别失败误归类为持久化失败。

## 2. Add safe transaction diagnostics

- [ ] 为 reconcile commit 阶段引入内部 stage error wrapper，保留 cause 链。
- [ ] 实现 SQLite/GORM 安全错误分类；日志只输出允许字段。
- [ ] 增加失败注入测试，覆盖关键阶段、原子回滚和日志脱敏。

## 3. Fix the persistence root cause

- [ ] 根据回归失败 stage 检查 recognition/entry 外键和唯一键、generation revalidation、scan run 更新与 change outbox revision。
- [ ] 以最小兼容变更修复实际根因，不吞错、不禁用约束、不产生部分提交。
- [ ] 覆盖 fresh DB、现行 DB 和相关旧 migration 升级数据库。
- [ ] 验证成功提交、重复扫描幂等和失败回滚。

## 4. Fix OpenRouter URL handling

- [ ] 重构 OpenAI-compatible Base URL path 规范化，接受安全 `/api/v1` 前缀。
- [ ] 修正 endpoint 拼接，避免重复 `/v1`。
- [ ] 增加 OpenRouter models/probe/structured generation URL fixture。
- [ ] 保留并扩展 SSRF、歧义 path、query/fragment、port、private IP 拒绝测试。

## 5. Quality gates

- [ ] 运行受影响 Go package 测试。
- [ ] 运行 `go test ./...`、`go vet ./...`、格式与 diff 检查。
- [ ] 运行 Server Web UI test/typecheck/lint/build。
- [ ] 运行 Windows `server/test.ps1` 全量门禁并确认测试进程退出。
- [ ] 使用 Trellis check 独立复核 spec、跨层数据流、日志脱敏和测试覆盖。

## 6. Commit and Server Beta

- [ ] 按 Conventional Commit 中文描述提交任务改动。
- [ ] 完成必要 spec 更新、归档任务并记录 journal。
- [ ] 推送最新 `develop`，确认本地 HEAD 与 `origin/develop` 一致。
- [ ] 从最新 `origin/develop` 触发下一个 Server Beta（预计 `v1.1.28`）。
- [ ] 确认 Windows/Linux Server 包和 SHA256 资产上传成功。

## Validation commands

```powershell
cd server
go test ./pkg/aiprovider ./internal/services ./internal/database
go test ./...
go vet ./...
cd webui
npm test
npm run typecheck
npm run lint
npm run build
cd ..
.\test.ps1
```

实际 package scripts 以仓库 `package.json` 和 `test.ps1` 为准；若名称不同，使用现有等价门禁并记录结果。

## Risk and rollback points

- reconcile transaction 是媒体目录权威 read model 的原子提交边界；任何变更必须先通过回滚测试。
- URL path 放宽可能扩大 SSRF 面；必须与既有安全 client 一起验证，不能以 provider 兼容为由放开 HTTP/私网/端口/重定向。
- 若需要 migration，先停在实现阶段复核 additive/idempotent/旧库升级契约，再继续发布。
