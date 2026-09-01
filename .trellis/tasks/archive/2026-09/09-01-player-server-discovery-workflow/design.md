# Design

## 1. Explicit Server discovery entry

`GlobalSearchWorkspace` 的普通搜索只调用 DataSource 搜索。底部 CTA 根据已启用 Server source 和 Bootstrap capabilities 计算状态；点击后显式调用 Server media discovery 并进入海报结果/详情。删除原先 `runSearch()` 中并发调用 `searchServerDiscovery` 的逻辑。

搜索输入组件统一为一个 clear control，弹窗 dismiss control 统一为“取消”。共享可访问性标签和焦点恢复。

## 2. Shared detail shell

从 `MediaDetailView` 提取 `MediaDetailShell` 及动作 slot。播放器本地详情提供播放动作，Server discovery 详情提供 acquisition 动作。海报、背景、标题、元数据、剧情、季/集布局保持一致；Server-specific 状态和向导作为 slot/子组件，不污染 DataSource 播放逻辑。

## 3. Acquisition wizard

向导状态显式分为 method/sites/results/target/confirm。每一步只在进入时请求所需数据，保留已完成选择。Server capability 决定搜索、下载、订阅控件，API 403 会刷新 capability 并回到可理解状态。

Server acquisition aggregate 转换为只读 UI 投影，详情 load 时与 coverage 并行但互相隔离。提交成功后使用服务端返回 aggregate revision 更新，而不是假定已入库。

## 4. Native SSE bridge

新增受严格路径 allowlist 的 Tauri command。它使用现有 Server URL/token 校验，`Accept: text/event-stream`，独立 connect timeout、长请求 deadline 和 idle timeout，限制单事件及累计安全数据量。Rust SSE parser 只接受已知 event 类型，把结构化事件通过 Tauri Channel 传回 TypeScript；redirect、非 SSE、超大/畸形事件均安全失败。

TypeScript `ServerDataSource.searchDiscoveryResourcesStream` 返回 cancel handle，并把事件关联到 search generation。Vue composable/reducer 维护 progress、站点 map 和稳定 order。新搜索先取消旧 handle；unmount 必须取消。

## 5. Progressive result reducer

`progress` 全量快照覆盖计数；`site` 以 site ID upsert group；`done` 固化最终状态。显示顺序使用 Server priority/order 字段而非事件到达顺序。重试只传失败 site IDs，并合并替换对应 group。错误事件只有搜索整体无法开始时进入页面级错误，站点错误留在站点行。

## 6. Load and timeout isolation

`resolveSource` 只解析配置和凭据，不调用 `source.test()`。详情请求决定基本连接状态；coverage 失败只显示未知；sites/options 延迟到向导。普通 JSON 使用短交互超时，SSE 使用 connect + idle timeout，海报继续使用独立二进制限制。
