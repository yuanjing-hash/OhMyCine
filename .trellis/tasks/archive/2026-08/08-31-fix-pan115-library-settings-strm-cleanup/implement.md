# 执行计划

1. 启动 Trellis 任务并加载 115、媒体库、整理、STRM、安全与 Server WebUI 规范。
2. 为 Pan115 增加受限路径解析和带用途的公平读取调度，保留共享风险控制与 endpoint mutation lanes；补齐调用数、饥饿、取消和 405/429 测试。
3. 把下载路线预览改成纯持久快照计算；提交/worker 保留一次权威校验，并补充多个 115 媒体库时零 provider 调用测试。
4. 重构同源 115 目标目录准备：一次根证明、叶路径解析、缺失层创建、每父目录一次 listing，与现有 batch intent/move/copy/reconcile 打通。
5. 修复 media library supervisor 原子替换和 wake channel 生命周期；为生活事件增加范围过滤、单飞合并和 closed-channel 回归。
6. 为 reconciliation 增加 no-op generation gate，避免空扫描推进 artifact generation 与重复 STRM 任务，并验证 pending change/partial 安全边界。
7. 优化 provider 目录服务为常数次 115 浏览；为选择器增加会话缓存、强制刷新和 stale response 防护。
8. 重构媒体库表单 fingerprint/baseline dirty 状态；成功后用权威详情重建并清除 picker token，增加就地保存反馈。
9. 在统一 STRM cleanup primitive 中实现受 root/reparse 保护的自底向上空目录收敛，覆盖云端删除和投影根切换。
10. 运行定向 benchmark/测试、`go test ./...`、`go vet ./...`、可用的 golangci-lint，以及 WebUI test、typecheck、lint、build。
11. 使用 Trellis 检查跨层契约，更新必要 specs，提交并归档任务；未经当前任务明确授权不推送或发布。
