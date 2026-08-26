# Server 115 网盘连接与云存储驱动实施计划

## Phase 1 — Connection 与只读 115 adapter

- [x] 增加 `Connection` model、v17 additive migration、唯一约束与引用限制测试。
- [x] 增加 provider-neutral `pkg/cloud` contract、factory 与稳定 provider error。
- [x] 实现 `pkg/cloud/pan115` Cookie allowlist parser、SDK adapter、有限 HTTP timeout、probe/list/stat/direct URL。
- [x] 实现 ConnectionService CRUD/test、AES-GCM purpose、健康/容量摘要、审计与安全 DTO。
- [x] 增加 Connection handlers/routes、strict JSON、permission/no-store 和 router 测试。
- [x] 在 Web UI 实现 115 Connection 卡片、创建/编辑/测试/删除和全局 toast。

Validation gate:

```powershell
cd server
go test ./pkg/cloud/... ./internal/services ./internal/httpserver
cd webui
npm test -- --run
npm run typecheck
```

Rollback point：v17 只新增表；停用 route/bootstrap 即可回到 local-only 行为。

## Phase 2 — 云目录选择与 115 Storage

- [x] 实现 provider directory picker token、分页浏览和双 permission。
- [x] 扩展 StorageService 为 local/cloud driver resolver，支持 `type=pan115`、Connection 引用、provider root identity 和 capability snapshot。
- [x] 扩展 Storage API payload/DTO，不破坏 `root_path` legacy contract。
- [x] 扩展目录选择对话框支持 provider listing，不在客户端拼路径或 ID。
- [x] 统一数据源页面支持 local/115 类型、复用已有 Connection、浏览云目录和健康摘要。
- [x] 覆盖不同 Connection 相同 directory ID、过期/篡改 token、引用删除冲突和审计脱敏。

Validation gate:

```powershell
cd server
go test ./internal/services ./internal/httpserver
cd webui
npm test -- --run
npm run typecheck
npm run lint
```

Rollback point：已有 local Storage DTO 和目录选择器测试必须原样通过。

## Phase 3 — provider-neutral 媒体库扫描

- [x] 提取媒体库 scanner/supervisor adapter，local 继续使用现有 ScanLocal/fsnotify。
- [x] 实现 115 专用 bulk-tree scanner：递归文件流与后代目录映射并行分页、本地重建相对路径，避免逐目录 List/逐文件 Stat；交互浏览继续使用保守 lane，bulk lane 保持有限并发、风控退避、partial 语义和 file ID identity。
- [x] 允许 115 Storage 创建 MediaLibrary，复用首次 baseline、catch-up、周期增量/全量、分类和条目 API。
- [x] 云媒体库只提供 `move|copy` 后续能力占位；本阶段 transfer 到 115 明确拒绝，避免误报成功。
- [x] 覆盖大目录分页、取消、部分失败、移动/重命名后 file ID 稳定和多库并发。

Validation gate：focused service tests + `go test ./...`。

## Phase 4 — 生活事件

- [x] 增加 provider event inbox/cursor additive migration。
- [x] 实现 115 life event client、allowlist payload 和 cursor `(update_time,event_id)`。
- [x] 实现 connection event batch 向 MediaLibrary supervisor 与同 Connection 的 115 离线下载 worker 扇出唤醒，不占用额外持久 Job slot。
- [x] 实现 inbox/cursor 的重复事件幂等、崩溃恢复和乱序测试；真实 115 拉取 adapter 接通后由周期 reconciliation 继续补漏。

Validation gate：fake event stream 的重启/重复/乱序/遗漏测试，不使用真实 Cookie。

## Phase 5 — DirectURL capability 与真实 smoke

- [ ] 实现 file ID → pickcode 服务端查找、UA-scoped DirectURL、singleflight 和 expiry-safe cache contract。
- [ ] 添加给 STRM/signed 302 独立任务使用的 service boundary；本任务不开放匿名 proxy route。
- [ ] 增加 `server/scripts/test-pan115.ps1`，从 `OMC_TEST_115_COOKIE` 读取真实凭据，使用隔离数据库并禁止输出 Cookie。
- [ ] 更新 Server README、架构当前状态、路线图和第三方许可记录。

## Final quality gate

```powershell
cd server
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
go mod verify
cd webui
go test .
go mod verify
npm run permissions:check
npm test -- --run
npm run typecheck
npm run lint
npm run build
cd ../..
git diff --check
```

真实 115 smoke 是 opt-in，不纳入无凭据 CI。失败时保留隔离运行目录中的脱敏日志，绝不回显 Cookie。

## Phase 6 — 115 原生离线下载

- [x] 定义 provider-neutral NativeOfflineDriver，完成 115 提交、有限页状态查询、完成输出身份和取消删除映射。
- [x] 增加 `pan115_offline` Downloader adapter，复用 Connection credential，并通过 Storage-scoped 目录令牌选择根内任意下载子目录，不复制 Cookie 或向 API 暴露 provider ID。
- [x] 接入现有下载器卡片、新建下载页面、DownloadTask/Job 遥测和取消流程。
- [x] 在类型选择、配置区和卡片明确标注 115 不支持暂停、恢复和做种；非做种 provider 不读取或快照全局做种策略。
- [x] Storage 列表/详情从当前 Driver 自动校正并持久化旧 capability snapshot，使升级前创建的 115 数据源无需重建即可出现在离线下载目录选择器。
- [x] 完成后从 provider output root 分页构建 manifest，并复用完成后分类。
- [x] 生活事件广播立即唤醒同 Connection 的全部离线任务复核，保留 20 秒低频查询补偿与 10 秒 Job lease 心跳，避免把不完整的生活事件当成完成事实。
- [x] 修复 Cookie 离线提交的 115 Browser UA、按规范化 info hash 认领歧义/重复任务、provider 状态语义与永久错误分类，避免云端已创建但本地持续重试。
- [x] 修复失败任务删除与后台调度写入竞争产生的 SQLite `BUSY_SNAPSHOT`，统一使用 immediate transaction reservation 并补并发回归。
- [x] 实现 115 云端移动/复制/改名 mutation，把已分类 manifest 接入同 Connection 的目标媒体库整理，并复用冲突策略与 dirty-generation 对账。
