# OhMyCine Player 架构

## 定位

Player 是远程媒体库客户端，也是独立的本地视频播放器。可配置媒体库只包括 OhMyCine Server、Emby、Jellyfin。已完成下载由内部 `offline` 虚拟源解析，不作为新连接出现。无需 Server 时仍可连接 Emby/Jellyfin、观看已完成的离线影片，或直接打开本地视频。

本地视频入口接受单文件、多文件和文件夹。文件夹仅取直接子级视频，按文件名自然顺序组成当前会话的队列；支持上一项、下一项和自动连播。进度、字幕、音轨和现有播放控制继续使用原生播放器能力。队列不持久化，也不生成媒体库、扫描索引或刮削元数据。

## 技术与代码

| 层 | 主要职责 |
| --- | --- |
| Vue 3 / TypeScript / Pinia | UI、路由、媒体库聚合、播放与下载状态 |
| `src/services/datasource/` | Server、Emby/Jellyfin 的类型化读取和内部离线解析 |
| `src/services/localPlaylist.ts` | 本地临时队列及自然排序 |
| `src/services/serverArtwork.ts`、`imageCache.ts` | Server 图片引用校验、按需读取和缓存 |
| `src-tauri/src/commands/` | 本地文件选择/读取、受认证图片请求、下载与安全存储 |
| Rust libmpv | 桌面及 Android 播放和原生媒体控制 |

`SourceLibraryView` 只浏览远程媒体库。服务端在线插件的分类节点、播放版本和操作描述均作为通用 Server DTO 处理，Player 不硬编码某个插件或网盘。Emby/Jellyfin 仍使用各自的媒体库、播放、图片、收藏及其原生维护接口。

## Server 图片契约

Server `/api/v1/player` 的作品、分类、播放版本、人物和历史响应提供可显示的图片地址，包含 `poster_url`、`backdrop_url`、`still_urls`、`profile_url`、版本图片 URL 与 `episode_still_url`。在线插件外部封面先由 Server 按插件获准的网络范围获取，再以受权限保护的 Server 引用交付。Player 不拼接 TMDB 图片域名，也不直连插件图片域名。

Server 图片只接受配置的 Server 同源、明确允许的受保护路径。原生请求带该 Server 设备认证，按需获取并写入有容量上限的本地缓存；凭据与上游地址不写入图片 URL 或持久展示缓存。Emby/Jellyfin 图片仍从各自服务器获取。离线下载同步保存海报、背景、剧照等图片附件，断网后优先使用本地副本。

Player 和 Server 通过版本化 HTTP/WebSocket 接口协作。本版 Player 面向同步升级后的 Server 图片契约，不承诺兼容旧版 Server；Server 的网盘驱动另行实现。

## 下载与升级

下载入口只为 Server、Emby、Jellyfin 规划播放文件及离线附件。已完成离线包保留原来源身份、视频、字幕、图片和播放记录；断线时先解析受管理的本地副本。

启动时，原生队列先删除旧网盘/本地来源的未完成任务及受管理的 partial 临时文件，再恢复仍受支持的任务。Android SAF 清理在队列恢复前尝试，失败记录留待下次启动重试。前端随后过滤旧来源配置并清理旧凭据、扫描缓存、导航和其他失效来源关联记录；迁移标记保证中断后可重复执行。已完成离线包和本地文件播放记录不因旧配置删除而丢失。

## 安全与验收

- Server Bearer 只发给配置的 Server 源；Emby/Jellyfin 凭据只发给各自服务。拒绝跨源图片 URL、路径穿越与不受保护的插件图片直链。
- 不把旧存储源恢复为可配置 DataSource，不在 Player 中实现网盘挂载、WebDAV、CloudDrive2、123、夸克、TMDB 刮削或本地扫描。
- 分别运行 Server 的 Go 测试，以及 Player 类型检查、Lint、构建、Rust 检查和相关验证脚本。桌面与 Android 的单文件、多文件、文件夹连播和真实远程播放需要各自运行环境验收。
