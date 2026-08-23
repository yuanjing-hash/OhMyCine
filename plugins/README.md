# OhMyCine 官方插件库

此目录存放使用公开 `@ohmycine/plugin-sdk` 构建的官方非 PT 站点插件。插件运行在 Server 的 WASM 沙箱中，Player 只消费 Server 输出的标准在线媒体库协议。

- PT 站点适配属于 Server 内建能力，不放入此插件库。
- 插件不得导入 Server `internal/` 包，也不得向 Player 注入 JavaScript、Vue 组件或样式。
- 每个插件只能声明 SDK 已公开的 capability，并通过 Host API 使用受控网络、连接内凭据、私有 KV、日志、下载方案和插件专属元数据能力。
- 下载、合流、队列、命名、冲突处理、本地入库、115 上传、NFO/JPG 和媒体库对账全部属于 Server 宿主。插件不得读取 Storage 凭据、本地绝对路径，也不得直接上传、移动或删除 Storage 文件。
- 插件设置页是 Manifest 中的声明式 `settingsPage`，只能组合宿主白名单组件，不能包含任意前端代码。
- 插件可通过 Manifest 的 `libraryArtwork` 携带一张受管 PNG/JPEG/WebP 媒体库封面。资源必须位于插件包内、经过 SDK 与 Server 双重完整性校验，并由 Server 转换为同源内容摘要 URL；插件不能让 Player 直接加载任意远端图片、SVG/HTML 或本地路径。

首个正式插件位于 `official/bilibili/`。使用 `plugin-sdk` 的 `npm run pack` 生成独立 Manifest 与 `.omcp` Release 资产；源码阶段不提交伪造的 `plugin.wasm` 或摘要，本地 `dist/` 也不会进入 Git。

官方多插件 Registry 独立发布在 `https://github.com/yuanjing-hash/OhMyCine-Plugins`。Server 插件页应添加该仓库地址；主仓库的 `develop` 分支只保存源码与 Registry 校验副本，不作为用户安装入口。
