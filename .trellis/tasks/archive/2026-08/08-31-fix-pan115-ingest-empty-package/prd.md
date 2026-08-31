# 修复115自动监听入库后的空包目录

## Goal

让用户手动转存到 115 自动监听目录的作品，在完成自动发现、识别、移动整理和入库后，自动回收该任务认领且已经为空的顶层作品包目录，同时严格保留监听根、非空包目录和任何不属于该任务的内容。

## Background

- 实际链路为：用户手动转存作品包到 115 监听目录 → 生活事件收养为内部下载任务 → 自动识别 → 同源 115 move 入正式媒体库 → 来源包变空。
- `server/internal/services/transfer_cleanup.go` 的 `cleanupTransferStaging` 在 `extras` 为空时直接标记成功；`cleanupCloudStaging` 只回收未选中的 manifest 文件，不检查 `DownloadTask.provider_output_id` 包根。
- 该目录不是 STRM 投影目录，本缺陷不属于本地 STRM 空目录收敛。

## Requirements

- 仅对 `pan115_offline` 来源、已完成成功入库的 Transfer 执行包根收敛。
- 以冻结的 `DownloadTask.provider_output_id` 作为唯一包根身份，并重新证明其位于冻结的 Staging Storage 根内；不得按目录名猜测。
- 包根不存在视为幂等成功；存在且权威 listing 为空时使用 115 回收站语义删除，并在 mutation 后重新读取确认其消失。
- 包根含任意文件或子目录时保留；不递归删除，不扩大到任务未认领内容。
- 禁止删除 Staging Storage 根、下载器监听根或自动监听目录本身。
- 即使 `extras` 为空也执行包根检查；若先清理 extras，只在其完成对账后检查包根。
- provider 风控、边界变化、listing/recycle/复查失败时保留可重试 cleanup 状态，不回滚已完成入库。
- 日志只记录任务 ID、稳定错误码和聚合数量。

## Acceptance Criteria

- [ ] 手动转存到监听目录的单作品包在媒体全部 move 入库后，空的顶层作品目录被回收，上层监听目录保留。
- [ ] `extras` 为空的健康路径仍执行边界证明、空 listing、recycle 和消失复查。
- [ ] 包根非空时不回收且 cleanup 可完成；包根已不存在时幂等成功。
- [ ] 包根等于 Storage 根、监听根或无法证明位于 Storage 根内时零删除并返回稳定安全错误。
- [ ] Recycle 或复查失败时可重试；下一次包根已不存在可正常收敛且计数不重复。
- [ ] 原有未选中文件安全清理、受保护视频/字幕、同源/跨源和做种清理测试不回归。

## Out of Scope

- 递归删除非空来源目录或自动扫描监听目录下所有历史空目录。
- 永久清空 115 回收站。
- 修改 STRM 投影、下载任务删除 UI 或媒体库内容删除。

