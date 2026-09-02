# Design

## Boundaries

- `ServerDiscoveryDetailView` 仅协调详情、覆盖率、搜索会话和入库状态。
- 站点选择、流式结果工作区、入库确认与任务中心拆为独立 Vue 组件。
- `serverDiscovery.ts` 负责运行时 DTO 校验、分页派生和状态标签；所有请求继续经 `ServerDataSource` 的 Tauri Bearer 边界。
- Server 是任务列表、任务状态、默认顺序和身份约束的权威来源；Player 不持久化 token、种子 URL 或站点私有信息。

## Flow

1. 点击搜索动作后加载站点并打开站点选择器。
2. 确认后关闭选择器、打开结果工作区并启动 SSE。
3. `site` 事件按 Server 站点顺序归并；首个非空结果自动成为活动页签。
4. 用户选择资源后打开入库对话框，先选下载器，再选媒体库，最后确认。
5. 创建任务后立即刷新当前作品 acquisition，并可从右侧悬浮工具条打开“Player 入库任务”面板；面板按当前账号聚合全部已连接 Server。
6. 目标媒体库将非终态 acquisition 投影为“正在入库”虚拟分类和占位海报，详情只展示阶段与进度，不构造虚假播放源。
7. 活跃 acquisition 使用有上限的定时刷新；离开页面或进入终态时停止。

## Navigation

进入详情前关闭搜索工作区但保留其 Pinia 状态。详情页注册 layout-back handler：从搜索进入时重新打开搜索工作区，从任务面板进入时重新打开任务面板；深链进入则回首页。避免组件内第二个返回按钮。

## Compatibility

旧 Server 缺少 acquisition 列表时，详情页单作品状态仍可工作；全局任务中心显示不支持提示。搜索结果 `page/has_next` 字段均为可选解析，客户端分页对旧响应兼容。
