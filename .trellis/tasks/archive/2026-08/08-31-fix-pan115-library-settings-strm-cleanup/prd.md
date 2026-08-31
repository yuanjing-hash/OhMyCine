# 修复 115 全链路性能、媒体库保存与 STRM 清理

## Goal

消除 Server 中 115 前台浏览、下载路线预览、同源媒体整理和后台媒体库监听之间的串行请求放大与相互饿死，让已规划的同源 115 整理真正以“规划一次、检查一次、批量移动、结果对账”的方式执行；同时修复媒体库设置二次保存反馈，并让任何权威云端删除或投影根切换产生的 STRM 清理完整收掉对应空目录树。

## Background

- 2026-08-28 的 MoviePilot-Plugins 对照已经实现了 115 批量 `MoveMany/CopyMany/RecycleMany`、私有 batch intent 和共享来源证明，但优化重点在 mutation 阶段；当前目录解析、保存校验、路线预览和目标目录准备仍反复走 `Stat/List`。
- `ProviderDirectoryService.BrowseStorage` 当前先从当前目录逐级 `Stat` 到 Storage 根，再重复 `Stat` 当前目录后执行 `List`。115 的 `Stat` 与 `List` 共用每两秒一个请求的 `listRate`，目录越深等待越长。
- 下载路线预览会为每个 115 媒体库调用 `providerItemWithinRoot`，按祖先深度远程验证已经持久化并验证过的根；前端重复预览会把一次本应纯本地的选择变成十几至六十秒请求。
- 同源整理虽然已在文件移动阶段使用批量 API，但进入“检查目标目录”后仍逐层 list、重复验证保存的目录状态；一个文件也会在真正 move 前支付多次两秒读限速，因此截图中的任务长期停在该阶段。
- 当前运行日志还显示 115 媒体库在一个事件批次内产生数百次 `event` reconciliation 和 artifact generation。媒体库 supervisor 的 wake 生命周期缺少唯一 owner 保护，缺失 handle 时会返回已关闭 channel；no-op scan 仍推进 generation 并调度产物，令后台循环进一步抢占 provider 调用槽。
- `DirectoryPickerDialog` 有请求取消但没有会话缓存，返回或重新进入同一目录仍访问 Server。
- 媒体库编辑仅在重新选择目录时设置 dirty。保存成功后短期 picker token 仍留在草稿，后续无关字段保存会重复执行 115 目录验证或在 token 过期后失败；反馈只显示在页面顶部。
- STRM cleanup 只删除 inactive managed 文件及 manifest，不向上删除已空的作品、季、类型和分类目录。该缺陷同时影响“更换 115 来源目录清旧投影”和“权威完整扫描确认云端视频已删除”两条链路。

## Requirements

### R1 — 115 前台读取与公平调度

- 目录选择、路线预览、同源整理目标准备、配置保存和后台扫描的 provider 调用数必须分别有明确上限，不得按“文件数 × 祖先深度”或“媒体库数 × 祖先深度”放大。
- 交互式目录浏览使用 115 路径解析能力，在常数次 provider 调用内验证完整路径仍解析为 token item ID；不支持该能力的 provider 保留安全 ancestry fallback。
- 下载路线预览只使用已持久化的数据源 identity、能力和已验证根快照，不访问 115；真正提交与 worker 执行仍在各自安全边界内做一次权威校验。
- 同源整理对 Storage/媒体库根只证明一次；目标目录 DAG 共享 listing/path cache。已存在目标叶目录应一次解析后直接进入冲突检查和 batch move；缺失目录只创建实际缺失层，不重复 list 已确认父层。
- 115 读操作按前台交互、活动下载/整理、后台扫描/生活事件分级并公平调度。后台任务不得饿死前台或活动 pipeline；所有级别继续共享 call slots、405/429 风险退避、熔断和 context 取消。
- 扫描、下载、移动、复制、重命名、回收和上传的 endpoint 风控边界保持独立；不得通过无限并发或删除真实风险退避换取速度。
- API 和日志不得公开 provider item ID、完整 provider 路径、Cookie、上游响应或用户本地绝对路径。

### R2 — 生活事件、supervisor 与 no-op generation

- 每个媒体库在任意时刻最多一个有效 supervisor；并发 start/stop/update 不能遗留 orphan listener，也不能把已关闭 channel 交给 listener 形成忙循环。
- 生活事件按 Connection 合并，并只唤醒能够由既有边界快照确定受影响、或确实需要保守对账的媒体库；同一事件风暴在稳定窗口内最多产生一次 reconciliation。
- 无新增、更新、删除和元数据变化的完整扫描不得推进 artifact generation、创建重复产物任务或改写 STRM；仍可记录轻量扫描成功状态。
- 后台扫描与产物调度必须有单飞/合并语义；更新 generation 接管旧任务后不得立即再创建同内容 generation 风暴。
- 增加安全聚合观测：各业务的 provider wait/call、path resolve、list、batch mutation、coalesced wake/no-op skip 数量与耗时，不记录敏感路径或 ID。

### R3 — 115 目录选择器

- 同一次选择器会话返回上级或再次进入已读目录时使用内存缓存；显式刷新绕过缓存并重新读取 provider。
- 快速切换目录时旧请求不能覆盖新位置；关闭选择器必须取消请求并停止 UI 更新。
- token 继续绑定 actor、Connection、Storage、Storage 根、目录身份、用途与有效期，不能越出 Storage 范围。

### R4 — 媒体库设置保存

- 任何持久化可编辑字段变化都必须被识别为未保存修改，且后台列表/状态刷新不能覆盖 dirty 草稿。
- 更换来源或 STRM 输出目录成功保存后，picker token 必须被消费并清出草稿；后续普通字段保存只提交稳定持久值，不再次进行 115 目录验证。
- 保存成功后使用 Server 返回的权威 `MediaLibraryDetail` 重建草稿和 baseline；失败保留用户输入。
- 保存按钮附近通过 `aria-live` 显示“正在保存”“保存成功”或可读错误；进行中防重复提交，无修改或无效时禁用，有有效修改时可用。

### R5 — STRM 文件与空目录收敛

- 当完整、非 partial 的云端扫描确认视频已删除时，必须删除其 inactive managed STRM，并从该 STRM 的父目录开始向上清掉已空的作品、Season、类型和分类目录。
- 当用户更换 115 媒体库来源或 STRM 投影根并清理旧投影时，采用同一空目录收敛规则。
- 只处理本次 inactive managed artifact 的祖先；含用户文件、非托管文件或其它托管活跃内容的目录保留。
- 只使用非递归空目录删除：不删除投影根，不使用 `RemoveAll`，不跟随 symlink、junction 或 reparse point，不越出 artifact run policy 的 owner root。
- 文件已删除但目录收敛失败时保留可重试 manifest 状态；重试跳过不存在文件并继续清目录。目录不存在、非空或重复运行均安全收敛。
- 审计只记录删除文件数、删除目录数和稳定错误码，不记录绝对路径。

## Acceptance Criteria

- [ ] A1：未缓存 115 目录浏览调用数不随目录深度线性增长；返回缓存目录零网络请求，显式刷新有且只有一轮读取。
- [ ] A2：下载路线预览对任意数量 115 媒体库都不调用 provider，正常响应不再出现 12～60 秒 ancestry 等待。
- [ ] A3：一个文件、目标叶目录已存在的同源 115 move 只执行一次根证明、一次目标解析/冲突 listing、一次 batch move 和一次结果对账；健康路径不在“检查目标目录”产生逐层两秒等待。
- [ ] A4：28 文件/4 目标父目录 fixture 的 provider 调用与唯一父目录/chunk 数相关；405/429 仍进入共享退避，普通成功不会显示成风控等待。
- [ ] A5：前台目录导航和活动整理在后台扫描/生活事件运行时仍有有界响应时间，不发生调用槽饥饿。
- [ ] A6：并发 supervisor start/stop/update 后每库只有一个 listener；关闭/缺失 wake channel 不产生 busy loop。
- [ ] A7：一批生活事件最多触发一次受影响库 reconciliation；完全 no-op 的事件/周期扫描不推进 artifact generation、不入队产物任务、不改写 STRM。
- [ ] A8：更换 115 目录保存后，扫描间隔、限速、TMDB、整理策略、复选框和忽略规则中的任意字段都可立即二次保存，且不带旧 token、不访问 115。
- [ ] A9：保存按钮就地显示进行中、成功、失败；成功后无修改禁用，继续修改任意字段恢复可用，后台刷新不覆盖 dirty 草稿。
- [ ] A10：云端删除一个视频后只删除对应托管 STRM，并删除其已空作品/季/分类祖先；同库其它作品和投影根保留。
- [ ] A11：切换媒体库来源/投影根、自动清理、人工清理、旧根清理和重复清理均收掉空目录；非空、unmanaged、reparse 和越界目录不动。
- [ ] A12：目录删除失败后 manifest 可重试；重试能在文件已不存在时继续完成目录收敛。
- [ ] A13：Server Go 定向/全量测试及 WebUI test、typecheck、lint、build 全部通过，日志和工作树不包含真实凭据、provider ID 或用户数据。

## Out of Scope

- 取消 115 风控保护、无限并发或复制 MoviePilot-Plugins 的 GPLv3 源码。
- 跨账号 115 转移或改变已批准的跨数据源下载到 Server 临时目录再入库模型。
- 递归扫描并删除与本次托管 artifact 无关的历史空目录。
- 删除 STRM 投影根、115 云端媒体文件或用户/非托管本地文件。
- 修改媒体库持久化字段或执行破坏性数据库迁移。
