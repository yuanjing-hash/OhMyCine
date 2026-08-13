# Design: Transfer and Import Pipeline

DownloadRoute 描述 downloader output、target library storage、staging 和 hops；ImportPlan 描述媒体身份、目标相对路径、命名、冲突和传输策略。二者分离，因为下载目的地由 provider 限制，而最终媒体位置由 MediaLibrary/入库策略决定。

状态机：queued -> downloading -> identifying -> awaiting_confirmation/planned -> transferring -> reconciling -> enriching -> projecting -> notifying -> completed，任何阶段可 failed/cancelled。每次外部副作用前持久化 intent + idempotency key。

冲突执行器读取不可变 rule snapshot。`ask` 生成 ActionRequest；`overwrite` 不生成 ActionRequest，并在同一受控相对目标内执行 replace protocol：重新读取目标 identity → 本地 permanent remove，cloud 则 trash-if-supported / permanent remove → 写入/移动新对象 → checkpoint 新 identity → 审计。本地不创建隔离回收区；cloud 回收站能力只改变旧目标去向，不能阻止覆盖。普通、递归或永久删除 API 仍与此协议分离并反复确认。

`reconciling/projecting` 是 pipeline 的阶段视图，不代表全局 Queue Job；transfer 完成只推进目标 LibrarySupervisor 的 dirty generation，并观察其对应 generation 收敛。supervisor 可为该 generation 派生独立 metadata scrape/refresh Queue Job，但监听、文件树 diff 和 STRM/伴随文件投影本身不入队。这样所有媒体库持续并行监听，pipeline 只等待自己的目标库确认，不占用其它库的监听能力。
