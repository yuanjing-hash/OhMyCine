# OhMyCine Bilibili 插件

这是 Bilibili 在线媒体源的官方插件包，必须只依赖 `@ohmycine/plugin-sdk` 和受控 Host API。Server 与 Player 核心不得添加 Bilibili API、Cookie、清晰度或下载特判。

当前后端实现提供推荐、热门、排行、搜索、详情、分 P、历史分页、播放进度回传、标准弹幕轨道、账号权限内的 progressive 清晰度播放和真实下载计划。所有站点请求、Cookie/CSRF 注入、短时播放资产注册和日志都经过 Host API；Cookie 与 CSRF 明文不进入 WASM、DTO 或日志。仅有 DASH 双轨而没有 progressive 的档位会明确返回不可用，不会把无声视频伪装为完整播放方案。

扫码登录、收藏/稍后再看/关注等个人集合、站点字幕和 DASH 双轨网关仍需按相同通用能力继续实现。Server 与 Player 核心没有 Bilibili API 特判。

Windows 构建：

```powershell
.\build.ps1
```

脚本编译 `wasm32-unknown-unknown` 并调用公开 SDK 打包器生成带真实 SHA-256 的 Manifest 与 `.omcp`；`target/` 和 `dist/` 都是 gitignored 本地产物。
