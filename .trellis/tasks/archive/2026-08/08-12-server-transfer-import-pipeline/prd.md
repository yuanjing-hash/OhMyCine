# Server 媒体库路由、传输与自动入库

## Goal

下载任务直接选择目标 MediaLibrary；未显式选择时按媒体库排序解析第一个可用目标。下载完成后使用该库关联的分类 Profile 和入库策略完成重命名、目录重建、冲突处理、文件转移与媒体库对账，不再引入重复的 DownloadRule 配置层。

## Requirements

1. MediaLibrary 支持稳定排序；管理端可拖动排序并提供键盘可用的上下移动操作。
2. MediaLibrary 保存入库方式、冲突策略和电影/剧集目录及文件名模板。本地首版支持 `move|copy|symlink`；云端以后按 capability 隐藏 `symlink`。
3. 下载提交直接选择目标 MediaLibrary；`0` 表示自动选择。自动选择在提交时按 `sort_order,id` 解析第一个启用、可用且与 downloader 输出兼容的库，随后快照为具体目标。
4. 分类 Profile 只能从目标 MediaLibrary 继承。DownloadTask 快照目标库、Storage、相对根、Profile ID/revision/rules 和完整入库策略；后续排序或配置变更不改变已入队任务。
5. 所有本地 downloader 仍只下载到 Server 全局暂存目录。qBittorrent 预分类使用目标库 Profile，并把分类结果映射为 provider category；下载完成后基于真实文件清单重新识别。
6. 下载完成后创建独立持久 `transfer` Job，不在 download worker 内长时间执行文件操作。TransferTask 只在私有状态保存绝对边界和 manifest，公开 DTO、日志、审计和 Job payload 不暴露绝对路径。
7. 本地入库保留视频扩展名，自动渲染受控目录/文件名模板；字幕和图片伴随文件与匹配视频使用相同目标 basename。所有源/目标路径必须重新规范化并限制在快照边界内。
8. `ask` 冲突创建 ActionRequest 并释放 worker slot，后续任务继续；`overwrite|skip|rename` 自动执行。显式覆盖只允许已重新验证的同一目标。
9. `move` 成功后源文件不再保留；`copy` 保留源；`symlink` 创建指向暂存源的链接并永久保留源。symlink 为管理员配置且不允许目标逃逸。
10. 入库完成后标记目标 MediaLibrary dirty generation并触发独立 reconciliation；媒体库监听本身不进入持久任务队列。
11. 历史无目标库 DownloadTask 继续完成下载/刮削但不自动入库；旧 API 的 `profile_id` 仅作为兼容输入，新 Web UI 不再展示独立 Profile 选择。
12. qBittorrent 等支持做种的下载器在 `copy|symlink` 入库成功后进入独立做种管理，不长期占用 transfer worker。用户可预设最低做种时长、最低分享率和条件组合方式，任务提交时快照策略。
13. 做种条件达成后，`copy` 删除 provider 任务及暂存源文件，`symlink` 只删除 provider 任务但必须保留源文件以保证链接有效。`move` 不进入做种管理。默认关闭自动做种清理，升级不得自动删除现有数据。
14. 暂存源文件解析同时兼容 qBittorrent 将文件保留在暂存根目录的情况；分类目录和根目录候选路径都必须逐级拒绝 symlink/Junction/Reparse Point 逃逸。
15. 下载页对失败的 download、transfer 和 seeding Job 都提供明确的分阶段重试入口；入库失败只重试 transfer Job，不重新提交或下载资源。

## Acceptance Criteria

- [x] 媒体库排序持久化，刷新后顺序不变；自动下载目标使用排序后的首个可用库。
- [x] 手动选择目标库后，任务快照使用该库 Profile 和入库策略，后续编辑不影响任务。
- [x] 下载页面只选择媒体库并展示 Profile、转移方式、冲突策略和目标摘要。
- [x] 下载完成创建独立 transfer Job；失败/等待冲突不阻塞后续 download/transfer Job。
- [x] fake 本地文件覆盖 move/copy/symlink、模板渲染、伴随文件、ask/overwrite/skip/rename 和重启幂等测试。
- [x] 任何任务/日志/API 响应均不泄露暂存或 Storage 绝对路径。
- [x] qBit 文件位于暂存根目录时仍可安全生成入库计划，分类目录优先且两条路径均通过 reparse-point 检查。
- [x] `copy|symlink` 入库成功后生成独立做种任务，展示分享率、做种时长和清理策略；达标后按模式使用正确的 `deleteData` 语义。
- [x] provider 任务已不存在时清理幂等完成；其它 provider 失败可重试且不删除做种记录。
- [x] 下载页的失败下载、失败入库和失败做种分别重试自己的 Job，不会误重试前置阶段。

## Out of Scope

- 115 等网盘原生离线下载和真实 cloud-to-cloud 字节传输。
- STRM/302 的具体投影实现。
- 独立 DownloadRule 管理页面。
