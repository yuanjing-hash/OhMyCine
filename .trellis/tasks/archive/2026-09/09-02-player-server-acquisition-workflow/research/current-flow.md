# Current Flow Findings

- 现有详情页已经通过 Tauri SSE 并行接收 `site` 事件，但把下载器、媒体库、Profile 和所有站点结果直接平铺在主页面。
- Server 站点结果已经返回 `page` 和 `has_next`，Player 解析层遗漏了这两个字段。
- AppLayout 已有统一 BackButton，详情页自身又渲染返回按钮，形成双返回。
- 全局搜索工作区在进入详情时 `hide()`，但没有由详情返回时重新打开工作区的上下文处理。
- 单作品 acquisition API 已有 transfer 进度字段，Player 类型层尚未解析。
- 全局账号任务列表需要 Server 增加 owner-scoped Player API，不能依赖 Player 内存记录。
