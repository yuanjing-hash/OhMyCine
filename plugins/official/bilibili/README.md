# OhMyCine Bilibili 插件

这是 Bilibili 在线媒体源的官方插件包，必须只依赖 `@ohmycine/plugin-sdk` 和受控 Host API。Server 与 Player 核心不得添加 Bilibili API、Cookie、清晰度或下载特判。

当前实现提供真实扫码登录、推荐/热门/排行/番剧/影视/纪录片、搜索、详情、分 P/UGC 合集/追更分集、收藏、稍后再看、历史、关注与追更读取、幂等远端动作、播放进度回传、progressive 与 DASH 双轨多清晰度播放、WebVTT 字幕、标准弹幕轨道和真实 DownloadPlan。Server 与 Player 核心没有 Bilibili API 特判。

扫码确认使用 Host 的两阶段凭据捕获：WASM 只能获得一次性引用，确认 Bilibili 业务状态成功后才要求 Host 将 Cookie 加密写入当前连接；Cookie、CSRF 和上游临时 URL不会进入普通 DTO、日志或 Player 持久状态。DASH 下载只描述视频/音频与固定合流拓扑，不允许插件指定命令或本地路径。

Windows 构建：

```powershell
.\build.ps1
```

脚本编译 `wasm32-unknown-unknown` 并调用公开 SDK 打包器生成带真实 SHA-256 的 Manifest 与 `.omcp`；`target/` 和 `dist/` 都是 gitignored 本地产物。
