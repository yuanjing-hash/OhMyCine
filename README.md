<div align="center">

# OhMyCine Player

**THE NORTH STAR OF YOUR CINEMA**

独立可用的跨平台家庭影院播放器

[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)
[![Player CI](https://github.com/yuanjing-hash/OhMyCine/actions/workflows/player.yml/badge.svg?branch=develop)](https://github.com/yuanjing-hash/OhMyCine/actions/workflows/player.yml)

</div>

## 关于 Player

OhMyCine Player 是基于 Tauri v2、Vue 3、TypeScript、Rust 和 libmpv 的家庭影院播放器。它连接 OhMyCine Server、Emby 和 Jellyfin 媒体库，也可独立打开本地视频播放。无需部署 OhMyCine Server，仍可使用 Emby/Jellyfin、已完成的离线影片与本地播放。

[下载与版本说明](https://github.com/yuanjing-hash/OhMyCine/releases) · [开发指南](DEVELOPMENT.md) · [报告问题](https://github.com/yuanjing-hash/OhMyCine/issues)

## 下载与安装

请在 Releases 中选择对应平台的附件，具体可用包以该版本页面为准。Beta 为预发布版本。

| 安装包 | 用途与数据位置 |
|---|---|
| Windows x64 安装程序 | 常规安装；用户数据保存在系统应用数据目录 |
| Windows x64 `standard.zip` | 解压后运行；与安装版使用相同的标准数据目录 |
| Windows x64 `portable.zip` | 解压到可写目录运行；配置、缓存与日志保存在程序目录旁 |
| Android ARM64 APK | Android 预览版；需允许安装来自所用浏览器或文件管理器的应用 |

当前发布链路提供 Windows x64 和 Android ARM64 预览构建。macOS、Linux、iOS 的平台工作仍在推进，不把跨平台目标等同于已有可用发布包。

Windows ZIP 请完整解压，保留随包提供的 libmpv 和其他依赖。更新时使用相同安装形态；应用内支持的更新入口与渠道以当前平台为准。不要把 standard ZIP 当作 portable 包使用，两者的数据位置不同。

## 当前能力

- **媒体浏览**：聚合首页、跨已启用数据源搜索、分类海报墙、电影详情、剧集分季分集、多版本选择。
- **本地播放**：直接打开单个或多个视频，或选择文件夹按自然文件名顺序连播其直接子文件；不建库、不扫描、不刮削。
- **播放**：嵌入式 libmpv、硬件解码、进度与续播、字幕和音轨切换、快捷键、弹幕及字幕搜索。具体解码和显示效果取决于平台、设备和媒体格式。
- **个人媒体**：观看记录、收藏、下载任务与离线媒体；不同数据源按实际能力提供操作。
- **Server 联动**：读取 Server 媒体库和在线媒体、同步观看进度，并在 Server 能力和账号权限允许时发起资源搜索、入库与订阅，查看入库任务进度。

### 媒体库来源

| 来源 | 用途 |
| --- | --- |
| OhMyCine Server | 读取 Server 管理的实体媒体库及在线插件媒体；图片由 Server 受保护地交付 |
| Emby / Jellyfin | 直接读取各自的媒体库、图片和播放信息 |
| 内部离线源 | 解析已完成下载的视频、字幕与图片，不需要单独添加 |

本地文件不作为媒体库来源。网盘、WebDAV、CloudDrive2、123 云盘、夸克等存储连接由 Server 负责，Player 不直接挂载。

## 首次使用

1. 在设置中添加 OhMyCine Server、Emby 或 Jellyfin 连接，或直接使用“打开视频”“打开视频文件夹”。
2. 从首页或媒体库打开影片详情，选择版本或剧集播放；本地队列可以切换上一项、下一项并自动连播。
3. 对支持下载的远程媒体创建任务，在离线入口播放已完成的视频和图片附件。

### 可选连接 OhMyCine Server

先部署 [OhMyCine Server](https://github.com/yuanjing-hash/OhMyCine-Server)，再在 Player 中添加 Server 数据源，使用 Player 所在设备可访问的 Server 地址并完成授权。其他设备上的 Player 应填写服务器的局域网地址或域名，不能填写它自己的 `127.0.0.1`。

Server 媒体库通过 API 提供元数据、海报、剧照、人物图片和播放入口，**不需要先输出 STRM 或生成 NFO 才能在 Player 中浏览**。普通搜索只搜索已启用的数据源；需要扩展搜索时，主动使用“从 Server 搜索更多并入库”。资源搜索、入库和订阅是否可用由 Server 能力与账号权限决定。

## 用户数据与便携模式

Windows 标准模式的默认根目录为 `%LOCALAPPDATA%\com.ohmycine.player\`；portable 模式使用可执行文件所在目录。两种模式分别保存自己的状态。

```text
data/     配置、凭据存储、下载与观看记录等持久数据
cache/    图片等缓存
logs/     运行日志
```

portable 包通过程序旁的 `portable.flag` 启用便携模式，也支持 `--portable` 启动参数。迁移便携版时应在退出应用后保留整个目录，包括 `data` 和 `portable.flag`；不要只复制 EXE。凭据存储具有平台与模式边界，跨机器迁移标准版后可能需要重新授权。

Server 的元数据、图片来源及生成的 NFO/STRM 由 Server 管理。Player 仅保留图片缓存、已完成下载的离线副本和本地播放记录。

## 截图

| 聚合首页 | 媒体主页 |
|----------|----------|
| ![聚合首页](png/聚合首页.png) | ![媒体主页](png/媒体主页.png) |
| Emby 媒体库 | 播放页面 |
| ![Emby 媒体库](png/emby媒体库.png) | ![播放页面](png/播放页面.png) |

## 本地开发

Windows 原生环境是桌面开发与验证的权威环境。需要 Node.js 20+、npm、Rust stable、MSVC C++ 构建工具以及 WebView2。克隆代码后运行：

```powershell
git clone https://github.com/yuanjing-hash/OhMyCine.git
cd OhMyCine
npm ci
npm run setup:libmpv -- windows
npm run tauri:dev:windows
```

常规门禁：

```powershell
npm run typecheck
npm run lint
npm run build
cargo check --manifest-path src-tauri/Cargo.toml
```

`npm run dev` 只启动前端开发服务，完整原生播放需要通过 Tauri 启动。Windows 原生打包使用 `npm run tauri:build:windows:native`；Android 预览构建使用 `npm run tauri:build:android:preview`，需要另行准备 Android SDK/NDK 与对应 Rust target。

不要为了测试清空真实标准或 portable 配置。需要干净状态时使用隔离临时 profile。

## 目录

```text
src/                 Vue 3 前端
src-tauri/           Rust/Tauri 与 libmpv 集成
scripts/             构建和契约验证脚本
docs/architecture/   Player 架构与安全文档
.github/workflows/   Player CI 与发布
```

详细开发规则见 [DEVELOPMENT.md](DEVELOPMENT.md)，Player 架构见 [docs/architecture/03-player-design.md](docs/architecture/03-player-design.md)。

## 相关项目

- [OhMyCine](https://github.com/yuanjing-hash/OhMyCine)：独立 Player（当前仓库）。
- [OhMyCine-Server](https://github.com/yuanjing-hash/OhMyCine-Server)：Web 管理端、媒体库、下载整理、STRM 与播放网关；[Docker 部署指南](https://github.com/yuanjing-hash/OhMyCine-Server/blob/develop/README.docker.md)。
- [OhMyCine-Plugins](https://github.com/yuanjing-hash/OhMyCine-Plugins)：官方插件、Plugin SDK、Registry 与 Hub。

## 许可证

[GPL-3.0](LICENSE)
