# 技术设计

## 115 provider 访问模型

在 Pan115 client 外部保持现有 `cloud.Driver` 最低接口，在可选能力中增加受限路径解析和带用途的读取调度。路径解析接收由 Server 内部构造的完整 provider 路径，只返回受控 item 事实；调用方必须再核对解析 ID 与已签发 token/持久根 ID，不把路径或 ID暴露给 DTO、日志或审计。

每个 Connection 共享一个风险控制器和有界 call slots，但读取等待按用途分为：前台交互、活动 pipeline、后台 reconciliation。调度器提供公平性而不是无限优先级：后台可推进，但不能连续占满所有槽；405/429、熔断、context 取消仍对所有用途生效。mutation、offline、upload 等 endpoint limiter 保持独立。

持久化且已在创建/更新时验证的 Storage、Downloader 与 MediaLibrary 根形成稳定配置快照。纯 UI 路线预览只读取该快照和 capability，不访问 provider；提交与 worker 仍在冻结 snapshot 上做一次权威根证明。短期 attempt cache 只复用同一业务尝试内的 Stat/List/路径解析结果，mutation 后按已知变更更新或失效。

## 同源 115 整理

worker 启动后一次证明 Storage 根、来源 package root 和目标媒体库根。根据 naming plan 建立去重目标目录 DAG，并按目标叶目录分组：

1. 使用受限路径解析一次确认已存在叶目录；
2. 若不存在，从最近已证明父层开始只创建缺失层；
3. 每个目标父目录只获取一次 conflict listing，并复用于 batch intent 与结果对账；
4. 按目标父目录/chunk 持久化 intent，执行 `MoveMany/CopyMany`，再以稳定 ID、parent、size、可用 SHA1 收敛；
5. mutation 后更新 attempt cache，不重新逐祖先 Stat。

现有 copy 临时目录、崩溃恢复、partial/ambiguous fail-safe、protected leftovers 与批量 cleanup 边界不变。

## 生活事件与媒体库 supervisor

supervisor registry 的 stop、replace、publish 在同一锁内完成，并为每个实例分配不可复用 generation/token。listener 直接持有创建时的 wake channel，不再按 ID二次查找；关闭 channel 是终止信号，必须使用双值接收，绝不当成持续 wake。

Provider event batch 先按 Connection 合并。利用上一次完整扫描形成的 provider item/parent 边界索引判断受影响媒体库；无法安全判定时才保守唤醒该 Connection 的库，并受单飞 debounce/stability window 约束。扫描事务先计算 catalog/metadata/policy diff：完全 no-op 时更新 scan status，但不推进 artifact generation，不入队 artifact job。

## 目录选择器与媒体库表单

目录服务对支持受限路径解析的 115 driver 使用常数次边界核对；其它 driver 继续使用 ancestry fallback。`DirectoryPickerDialog` 按 browse token 建立弹窗会话缓存，普通导航读缓存，显式刷新绕过；请求序号与 AbortController 防止 stale response。

媒体库表单使用覆盖全部持久编辑字段的稳定 fingerprint 与权威 baseline 比较 dirty。picker token 不属于持久配置。保存成功后用 API 返回的 `MediaLibraryDetail` 更新列表并经 `draftFromLibrary` 重建草稿，清除 token；失败保留输入。按钮附近使用 `aria-live` 展示保存状态。

## STRM 空目录收敛

无论 inactive artifact 来自云端文件删除、来源根切换还是旧投影根人工清理，都走同一 cleanup primitive：

1. 继续使用 `safeCleanupTarget` 验证 owner root identity、Constrain、Lstat 和 reparse 边界；
2. 删除文件，NotFound 视为已收敛；
3. 从父目录自底向上使用非递归 remove 删除空目录，在 owner root 前停止；
4. 非空正常停止；不存在继续向上；symlink/junction/reparse、越界、不可读或真实删除错误返回稳定错误；
5. 文件和目录均收敛后才删除 manifest。失败恢复可重试状态，重试允许文件已不存在。

## 安全、兼容与观测

- 不复制 MoviePilot-Plugins GPLv3 代码，只复用批量分组、缓存一致性和 tree diff 的设计证据。
- 不新增公开 provider identity，不记录路径、文件名、Cookie、pickcode 或响应正文。
- 不使用 `RemoveAll`，不删除投影根、用户内容或云端媒体文件。
- 增加聚合 counters/timers：read class wait/call、path resolve、target list、batch mutation、event coalesce、no-op artifact skip、removed files/directories。
- 不支持新可选能力的 provider 保持现有安全 fallback。

## 测试策略

- Provider/route：深度无关调用数、路线预览零 provider 调用、公平调度、取消、风险退避与 fallback。
- Transfer：单文件已存在目标叶目录、28 文件/4 父目录、缺失层创建、batch crash replay、partial/ambiguous 和 mutation cache 失效。
- Supervisor/event：并发 start/stop/update、closed wake、事件风暴合并、范围过滤、no-op scan 不推进 artifact。
- WebUI：目录缓存/刷新/stale response、全字段 dirty、成功清 token/重建 baseline、失败保留草稿、就地反馈。
- STRM：云端删除、根切换、自动/人工/旧根、幂等、多层空目录、非空、unmanaged、reparse、失败重试。
