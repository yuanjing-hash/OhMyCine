# OhMyCine Bilibili 插件

这是 Bilibili 在线媒体源的官方插件包，必须只依赖 `@ohmycine/plugin-sdk` 和受控 Host API。Server 与 Player 核心不得添加 Bilibili API、Cookie、清晰度或下载特判。

当前实现提供真实扫码登录、插件驱动的多层栏目导航、推荐/热门/排行/番剧/影视/纪录片、搜索、详情、分 P/UGC 合集/追更分集、收藏、稍后再看、历史、关注与追更读取、幂等远端动作、播放进度回传、progressive 与 DASH 双轨多清晰度播放、WebVTT 字幕、标准弹幕轨道、真实 DownloadPlan 和插件专属 ProviderMetadata。Server 与 Player 核心没有 Bilibili API 特判。

扫码确认使用 Host 的两阶段凭据捕获：WASM 只能获得一次性引用，确认 Bilibili 业务状态成功后才要求 Host 将 Cookie 加密写入当前连接；Cookie、CSRF 和上游临时 URL 不会进入普通 DTO、日志或 Player 持久状态。DASH 播放分别注册视频/音频资产，优先 AVC/AAC，并从 Bilibili `baseUrl/backupUrl` 选择 Host 可安全访问的候选；下载只描述视频/音频与固定合流拓扑，不允许插件指定命令或本地路径。

连接配置由声明式设置页提供，包含账号状态、首页推荐、默认清晰度、字幕、弹幕和元数据说明。默认清晰度仅在用户没有显式选择时生效。插件通过带 `route:` / `branch:` 作用域的 `library.artwork_candidates` 为推荐、热门、番剧、电影、个人收藏等栏目分别注册真实海报短时 Host asset，Server 负责筛选、解码限制、风格 3 合成和签名分发；插件包内的原创横向封面仅作最外层入口及上游不可用时的兜底。Player 不直接读取插件文件、Host asset UUID 或 Bilibili 远端图片。

Bilibili 插件只返回标题、简介、UP 主、发布日期、时长、BVID/CID 与受控封面资产引用。媒体下载、DASH 合流、字幕/弹幕旁挂、入库、115 上传、冲突处理以及 NFO/JPG 生成都由 Server 内置流水线完成。因此 Bilibili 普通视频无需 TMDB 也能生成包含 Provider 身份的 NFO 和海报，且该元数据不会被其他扫库/下载流程调用。

Windows 构建：

```powershell
.\build.ps1
```

脚本编译 `wasm32-unknown-unknown` 并调用公开 SDK 打包器生成带真实 SHA-256 的 Manifest 与 `.omcp`；`target/` 和 `dist/` 都是 gitignored 本地产物。
