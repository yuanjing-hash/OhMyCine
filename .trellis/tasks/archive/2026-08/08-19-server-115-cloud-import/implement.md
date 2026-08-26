# 115 云端自动整理入库实施计划

## 1. Schema and private contracts

- [x] 增加 v23 加法迁移和 model 字段，保存 target storage/provider/Connection 快照及 cloud item checkpoint。
- [x] 扩展 private downloader manifest，保留 115 item ID、parent ID、size/SHA1；验证 API/日志仍不可见。
- [x] 开放同 Connection、可写 115 MediaLibrary 的下载目标解析，并拒绝 symlink/跨 Connection。

## 2. Provider mutation adapter

- [x] 在 `pkg/cloud` 定义细粒度 mutation capabilities 与 optional interface。
- [x] 为 pan115 SDK adapter 接入 Mkdir/Move/Copy/Rename/Recycle。
- [x] 增加 mutation limiter，复用并发槽、风控 backoff/circuit 和稳定错误映射。
- [x] 用 fake SDK 覆盖参数、鉴权、限流、超时后复核和敏感错误脱敏。

## 3. Provider-neutral transfer planning

- [x] 提取共享模板计划，local 输出受控绝对路径，cloud 输出受控 provider-relative segments。
- [x] 实现 source/target Storage、Connection、root ancestry 和 capability 验证。
- [x] 实现唯一目录 ensure、同名歧义检测、目标冲突扫描及组级 rename 后缀。
- [x] 保持 plan summary 公开 allowlist，不暴露 provider ID、完整路径或 checkpoint。

## 4. Durable cloud executor

- [x] 实现 move 的 placed/renamed/completed 幂等状态机。
- [x] 实现 copy 的结果唯一识别、checksum/size 复核和歧义 fail-closed。
- [x] 接入 ask/overwrite-to-recycle/skip/rename，等待状态释放 worker slot。
- [x] 每项操作后 heartbeat + 持久化；限流/临时故障释放 slot 并按队列重试。
- [x] 完成后更新 TransferTask、审计、dirty_generation；115 跳过做种管理并按模式做安全 provider 收尾。

## 5. UI and observability

- [x] 下载创建页允许选择可写 115 MediaLibrary，显示“云端移动/云端复制”且不显示软链接。
- [x] 媒体库配置根据 Storage capability 限制 transfer mode。
- [x] 媒体整理详情复用现有冲突响应、重试、删除历史和计划摘要。
- [x] 增加“115云端整理”日志 operation、过滤项和脱敏审计事件。
- [x] 命名计划保存后立即进入“正在入库”，目录准备保持该阶段；下载管理与媒体整理明确提示 115 风控限速，多文件任务按完成项更新真实进度。

## 6. Verification and documentation

- [x] fake provider 覆盖 move/copy、四种冲突、sidecar、重启点、越界、歧义和部分失败。
- [x] 回归 local transfer、qBittorrent seeding、115 offline、生活事件和 MediaLibrary reconciliation。
- [x] 更新 backend specs、Server architecture/roadmap、Web UI contracts 和可选 live smoke 说明。
- [x] 运行 Go/Web UI 全量测试、vet、两种 Server build、module verify 和 `git diff --check`；测试后不遗留 Server 进程。

## 7. Package takeover regression

- [x] 将电影主片/剧集包/广告小视频/关联 sidecar 的选择放到 provider-neutral 完成清单入口。
- [x] 修正发行噪声标题候选，覆盖 `Seven Samurai CC MA 2 0 SONYHD -> Seven Samurai`。
- [x] 未识别或未筛选清单在 Transfer enqueue 和 planner 两层 fail closed，不创建“未分类”目录。
- [x] 旧失败 TransferTask 重试时重新识别并替换为过滤后的私有 manifest，不重提下载任务、不重复接管广告文件。
- [x] 为所有运行中 Worker 增加 scheduler-owned quiet lease keepalive，长 provider 调用不再被重复领取。
- [x] 运行聚焦、全量 Go 测试及最终质量门。

## 8. Shared recognition and naming profiles

- [x] 修复真实发行名尾部混合分隔符清洗，并增加真实文件夹/文件名与 TMDB 查询参数回归。
- [x] 重试重新识别前清空旧 plan/progress/cloud projection，失败后不显示陈旧广告计划。
- [x] 增加 Profile 识别规则与电影/剧集命名模板的加法迁移、严格校验、复制和 revision CAS。
- [x] 下载入队快照 Profile 识别/命名配置，所有 downloader/provider 共用同一 classify/transfer 入口。
- [x] 规则管理 UI 增加分类、识别预处理和命名格式分页；媒体库 UI 只保留目标/转移/冲突职责。
- [x] v24 按旧 Profile 与命名模板组合迁移并重绑媒体库，保留 v14-v23 自定义命名行为。
- [x] 公共识别入口覆盖主文件、父目录与包名，Profile 预处理先于媒体类型/年份解析；Transfer 二次执行同一安全清单筛选。
- [x] 完成 Go、Web UI、迁移、API、复制、快照和真实发行名全量验证。

Validation gate:

```powershell
cd server
go test ./pkg/cloud/... ./pkg/downloader/... ./internal/services ./internal/httpserver
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
go mod verify
cd webui
npm run permissions:check
npm test -- --run
npm run typecheck
npm run lint
npm run build
cd ../..
git diff --check
```

## Risky files and rollback points

- `server/internal/database/migrations.go`: v23 只能加列/表，先跑迁移和旧库升级测试。
- `server/pkg/cloud/client.go`: optional mutation interface 不能破坏只读 provider fake 和现有扫描。
- `server/internal/services/transfer.go`: local executor 必须保持原行为；cloud 分派前先完成 shared-plan 回归。
- `server/internal/services/download.go`: 目标选择开放 115 后必须在入队前验证 capability/Connection，不允许自动落到另一个库。
- 每阶段完成后跑 focused tests；出现云端结果歧义时停止自动重试，不通过删除远端数据回滚。
