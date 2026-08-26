# 接入 CloudDrive2 WebDAV 数据源

> 已废弃：该 MVP 将通用 WebDAV 错误地绑定到了 `clouddrive2` 类型。后续任务 `07-27-clouddrive2-api-webdav-split` 已将 CloudDrive2 改为官方 gRPC API Token，并把 WebDAV 拆为独立 `webdav` DataSource。本文件仅保留历史决策记录。

## Goal

在 Player 独立版中接入 CloudDrive2 数据源，让用户可以通过 CloudDrive2 暴露的 WebDAV 地址添加媒体库，完成根目录选择、目录浏览、视频播放，并复用现有原始文件源扫描、分类、海报墙、全量/增量扫描调度和首页聚合能力。

## What I Already Know

* 路线图 Phase 1 Sprint 1.3 明确要求 CloudDrive2 能连接、浏览和播放。
* `DataSourceType` 已预留 `clouddrive2`，但 `DataSourceManager.createDataSource()` 尚未实例化 CloudDrive2。
* 现有 `AlistDataSource` 和 `LocalFileDataSource` 已提供 raw source 模式：`list()`、`listLibraries()`、`getDetail()`、`getStreamURL()`、`getHomeSections()` 都能复用本地扫描缓存。
* 扫描类型 `RawFileSourceType` 已包含 `clouddrive2`，但 `isAutoIndexableRawSourceType()`、`SourceLibraryView` 和 `SettingsView` 仍只启用 `alist` / `local`。
* CloudDrive2 凭据属于敏感信息。普通 DataSource 配置只能保存 URL、显示名、根目录和 `credentialRef`，账号密码必须写入现有 Tauri SQLite 凭证边界。
* 本任务只做 Player 独立数据源，不引入 Server、STRM、302 代理或文件写操作。

## Requirements

* 新增 `CloudDrive2DataSource`，通过 WebDAV `PROPFIND` 浏览目录，映射为通用 `MediaItem` / `MediaLibrary` / `MediaDetail`。
* 添加设置页 CloudDrive2 类型卡片，使用服务器 URL、账号、密码登录/验证，并支持从 `/` 浏览目录选择根目录。
* CloudDrive2 新增和编辑流程必须复用现有 credentialRef 持久化边界，不把账号、密码、Authorization header 或可播放直链写入普通配置、localStorage、错误文本或扫描缓存。
* CloudDrive2 根目录作为非敏感 `extra.rootPath` 保存；不选择时默认为 `/`。
* CloudDrive2 文件夹源接入 raw source 扫描：首次进入可触发索引进度，全量/增量调度按每个源配置生效，扫描结果进入媒体库分类、海报墙和 Home 聚合。
* `getStreamURL(id)` 只允许具体文件路径，拒绝 synthetic raw series/season id、目录、根目录外路径和非 HTTP(S) WebDAV URL。
* UI 与 OpenList/Alist、本地文件源保持同一交互模型：默认媒体库视图、文件夹兜底入口、扫描管理面板、错误/空状态清晰。
* 同步更新 `docs/architecture/06-roadmap.md` 的 CloudDrive2 状态。
* 本地验证必须通过 `npm run typecheck`、`npm run lint`、`npm run build`，并按项目要求刷新 Windows GNU exe。

## Acceptance Criteria

* [x] 设置页可以选择 CloudDrive2，填写 WebDAV URL、账号、密码后登录测试。
* [x] CloudDrive2 添加/编辑时可以浏览目录并选择根目录，最终配置只持久化非敏感字段和 `credentialRef`。
* [x] 左侧侧边栏显示 CloudDrive2 数据源，进入后能浏览目录和文件。
* [x] CloudDrive2 视频文件能通过现有 Player 播放流程获得可播放 URL。
* [x] CloudDrive2 作为 raw file source 进入全量/增量扫描调度，扫描设置页可配置全量/增量启用状态与间隔。
* [x] CloudDrive2 扫描缓存中的 matched 条目能进入媒体库海报墙和 Home 聚合；失败不影响其他数据源。
* [x] 凭据、Authorization header、tokenized URL 不进入普通配置、错误展示、日志或扫描缓存。
* [x] `npm run typecheck`、`npm run lint`、`npm run build` 通过。
* [x] Windows GNU release exe 刷新。
* [x] 本地 git commit 完成，不 push GitHub，除非用户明确要求。

## Definition of Done

* Trellis task 进入 `in_progress` 并记录实现/检查上下文。
* CloudDrive2 WebDAV DataSource、设置页、SourceLibrary、扫描调度、路线图全部完成。
* 核心 WebDAV 路径、凭据边界、root containment 和 XML 解析有 focused verification 脚本或等价测试覆盖。
* Player 前端检查与构建通过。
* Windows GNU 主程序重新编译产出。
* 本地提交完成。

## Technical Approach

* 新增 `player/src/services/datasource/clouddrive2.ts`，优先复用 Alist/Local 的 raw source mapping、path normalization、home sections 和 credential backup/restore 模式。
* 在 `credentialStore.ts` 新增 CloudDrive2 provider envelope，保存 `{ provider: 'clouddrive2', username, password }`。
* 使用现有 `ofetch` 发送 WebDAV `PROPFIND`，自带 Basic Auth header；XML 解析使用浏览器/Node 均可用的轻量 DOMParser 回退逻辑或小型受控解析函数，不引入额外依赖，除非验证发现现有运行环境无法支持。
* `listLibraries()` 暴露选中的 `extra.rootPath`；`list()` 将 root path 作为逻辑库根；`search()` 先做有限目录递归搜索。
* `getStreamURL()` 返回 WebDAV 文件 URL，并只作为播放层敏感 URL 使用；不在 UI 上显示，不持久化。
* 设置页把 CloudDrive2 纳入账号密码型数据源，并抽象现有 Alist 目录浏览状态为 raw remote directory browser，以复用根目录选择 UI。
* 将 `isAutoIndexableRawSourceType()`、`SourceLibraryView` raw source 判定、扫描计划设置判定扩展到 `clouddrive2`。

## Decision (ADR-lite)

**Context**: CloudDrive2 暴露 WebDAV，项目路线图要求 Player 独立连接 CloudDrive2。现有 Player 已经有 OpenList/Alist 和本地文件 raw source 扫描/海报墙路径。

**Decision**: 本 MVP 采用通用 WebDAV + Basic Auth 接入 CloudDrive2，直接复用 raw file source 本地扫描体系。暂不接入 CloudDrive2 私有 API，也不做远端写操作。

**Consequences**: 实现更小、更符合 Player 独立优先，并为后续通用 WebDAV/CloudDrive2 扩展保留空间。代价是某些 CloudDrive2 私有能力不会在本任务中出现。

## Out of Scope

* 不做上传、删除、移动、重命名、创建目录等 WebDAV 写操作。
* 不做 Server 端 CloudDrive2 driver、302 代理、STRM、配置同步。
* 不做 115 / 123 / 夸克真实驱动。
* 不实现 OAuth、Cookie、二维码或 CloudDrive2 私有登录模式。
* 不新增全局 toast 或重做设置页布局。

## Technical Notes

* Relevant specs: `.trellis/spec/frontend/index.md`, `directory-structure.md`, `component-guidelines.md`, `state-management.md`, `type-safety.md`, `quality-guidelines.md`, shared thinking guides.
* Security reference: `docs/architecture/07-security-design.md`.
* Main code paths: `player/src/services/datasource/alist.ts`, `local.ts`, `credentialStore.ts`, `manager.ts`, `player/src/views/SettingsView.vue`, `SourceLibraryView.vue`, `player/src/services/scraper/rawSourceScanSchedule.ts`, `rawSourceIndexScheduler.ts`.
