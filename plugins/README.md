# OhMyCine 官方插件库

此目录存放使用公开 `@ohmycine/plugin-sdk` 构建的官方非 PT 站点插件。插件运行在 Server 的 WASM 沙箱中，Player 只消费 Server 输出的标准在线媒体库协议。

- PT 站点适配属于 Server 内建能力，不放入此插件库。
- 插件不得导入 Server `internal/` 包，也不得向 Player 注入 JavaScript、Vue 组件或样式。
- 每个插件只能声明 SDK 已公开的 capability，并通过 Host API 使用网络、凭据、私有存储、日志和下载计划能力。

首个正式插件位于 `official/bilibili/`。使用 `plugin-sdk` 的 `npm run pack` 生成独立 Manifest 与 `.omcp` Release 资产；源码阶段不提交伪造的 `plugin.wasm` 或摘要，本地 `dist/` 也不会进入 Git。
