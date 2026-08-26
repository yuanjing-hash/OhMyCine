# 本地文件数据源媒体库

## Goal

在现有 OpenList/Alist 原始文件源刮削链路之上，实现 Player 独立可用的 `LocalFileDataSource`。用户选择一个本地影视文件夹后，Player 能像 OpenList/Alist 一样浏览目录、播放视频、递归只读扫描、生成分类卡片和海报墙，并把扫描结果保存到现有 Tauri app data SQLite raw scan cache。

## What I already know

* 用户希望本地文件源“直接跟现在的 OpenList 差不多”，复用刚完成的扫描、分类、缓存、海报墙体验。
* 当前分支 `develop-new/local-file-datasource` 已从本地 `develop` 创建；本地 `develop` 已 fast-forward 到 OpenList 刮削缓存和单文件电影识别完成状态。
* Player 路线图 Sprint 1.3 中 `LocalFileDataSource` 仍未完成：本地文件系统浏览、文件类型过滤、文件关联和完整本地媒体库能力待实现。
* 现有 `DataSourceType` 已包含 `local`，`RawFileSourceType` 也包含 `local`，Tauri raw scan cache 命令已允许 `local`。
* 现有 OpenList/Alist 已有可复用模式：`list` / `search` / `getDetail` / `getStreamURL` / `getHomeSections`，SourceLibraryView 中也已有 raw media-library、扫描管理、手工识别、图片覆盖和文件夹兜底视图。
* 现有浮动按钮和播放页已经支持选择单个本地视频并播放，但这不是 DataSource 级本地文件夹媒体库。
* 本任务仍遵守用户规则：除非明确要求推送 GitHub，否则只做本地 git，不执行 `git push`；每次代码改动完成后需要重新生成 Windows GNU release exe。

## Assumptions

* MVP 优先支持用户手动添加一个或多个本地文件夹作为数据源。
* `DataSourceConfig.url` 可保存为非敏感本地源标识，例如 `local://filesystem`；真实根目录放在 `extra.rootPath`。
* 本地文件源扫描与展示使用 provider-style rooted path，但不得把本地绝对路径暴露给 AI/export/logs；前端展示尽量使用文件名、相对路径和 root label。
* 本地文件源是只读媒体库：MVP 不提供删除、移动、重命名、上传、下载或整理文件。
* 文件关联（双击系统文件打开 Player）不放入本任务 MVP，先完成文件夹数据源媒体库。

## Requirements

* 新增 `LocalFileDataSource`，实现通用 DataSource 接口。
* 设置页支持添加/编辑/删除“本地文件夹”数据源：
  * 使用 Tauri dialog 选择目录。
  * 保存显示名称、启用状态、排序和非敏感 `extra.rootPath`。
  * 不需要账号、密码、token 或 credentialRef。
* Tauri 后端提供安全的本地目录读取命令：
  * 只能在用户选择的 root 内列目录/文件。
  * 拒绝路径穿越、root 外路径、URL-like 路径和明显不安全路径。
  * 返回 `name/path/isDir/size/modified` 等前端需要的字段。
  * 遇到无权限目录或单个读取失败时返回用户可理解错误，扫描时可按分支跳过。
* 本地文件源支持：
  * `list(path?)` 目录浏览。
  * `listLibraries()` 返回选择的根目录入口。
  * `search(keyword)` 在 root 内做有限目录回退搜索。
  * `getDetail(id)` 返回文件/目录详情和可播放媒体源。
  * `getStreamURL(id)` 返回 mpv 可加载的本地文件路径，目录不能播放。
  * `getHomeSections()` 读取现有 raw scan cache 并生成 Hero / 最新 / 分类入口。
* SourceLibraryView 支持 `local` 原始文件源：
  * 默认进入媒体库视图，和 OpenList/Alist 一样显示大图、分类卡片、海报墙和扫描管理。
  * 保留文件夹浏览兜底入口。
  * 手动扫描调用 `rawSourceIndexScheduler.forceScan`，`sourceType` 使用 `local`，`rootPath` 使用本地源 root。
  * 加载缓存时使用 `loadRawSourceScanCache(sourceId, 'local', rootPath)`。
* 本地源扫描复用现有 parser / TMDB enrichment / category rules / manual identification / artwork override / raw scan SQLite cache。
* 播放路径必须能进入现有 PlayerView/mpv 加载流程。
* 更新 `docs/architecture/06-roadmap.md` 对 LocalFileDataSource 状态。

## Acceptance Criteria

* [x] 设置页可以添加本地文件夹数据源，并在侧栏出现。
* [x] 进入本地文件源后可以浏览 root 内目录和视频文件。
* [x] 点击本地视频文件能进入播放页并由 mpv 播放。
* [x] 手动扫描本地文件源后，可以生成和 OpenList/Alist 一样的媒体库分类卡片和海报墙。
* [x] `阿凡达.mp4`、`电影/阿凡达.mp4`、`动漫/剧名/Season 01/S01E01.mkv` 等本地结构复用现有 raw parser 行为。
* [x] 扫描缓存按 `sourceId + local + rootPath` 隔离保存；本地文件源前端和扫描缓存使用 `/` 开头的逻辑 provider path，不写入本地绝对路径。
* [x] 本地源缓存可进入 Home 聚合，但未匹配/失败/未配置条目不污染正式 Home。
* [x] root 外路径、`..`、URL-like 路径、符号链接逃逸和目录播放被拒绝。
* [x] 文件夹读取或扫描失败不影响已可浏览/可播放的其它条目。
* [x] 相关 verify/typecheck/lint/build/cargo check 通过。
* [x] Windows GNU Tauri build 通过，并刷新 `player/src-tauri/target/x86_64-pc-windows-gnu/release/ohmycine-player.exe`。
* [x] 本地 git commit 完成，不 push GitHub。

## Verification

* `npm run verify:local-file-datasource`
* `npm run verify:raw-source-index-scheduler`
* `npm run verify:raw-scan-cache`
* `npm run verify:scraper`
* `npm run verify:home-fault-isolation`
* `npm run lint`
* `npm run build`
* `cargo test --manifest-path player/src-tauri/Cargo.toml local_file`
* `cargo check --manifest-path player/src-tauri/Cargo.toml`
* `node scripts/setup-libmpv.mjs windows`
* `tauri build --target x86_64-pc-windows-gnu --bundles nsis`
* Windows GNU exe: `player/src-tauri/target/x86_64-pc-windows-gnu/release/ohmycine-player.exe`, `2026-07-08 17:39:08 +0800`, `29865952` bytes.

## Definition of Done

* PRD、implement/check 上下文完成并进入 `in_progress`。
* 代码保持 Player independent-first，不引入 Server 依赖。
* 本地文件操作只读且 root-scoped。
* 复用 DataSource 抽象，不在视图里绕过数据源调用 Tauri 文件命令。
* 相关 frontend/Rust checks 和 Windows GNU package build 完成。
* 路线图和必要 spec 更新完成。
* 本地 commit 完成。

## Technical Approach

* 新增 Rust Tauri command 模块，例如 `commands/local_file.rs`：
  * `local_file_list(root_path, path?)`
  * 可选 `local_file_metadata(root_path, path)`，如果 `list` 返回信息足够，MVP 可不单独增加。
  * 命令内部 canonicalize root/path，并检查目标仍在 root 下。
* 新增前端 `player/src/services/datasource/local.ts`：
  * 读取 `DataSourceConfig.extra.rootPath`。
  * 把本地文件映射成 `MediaItem`，目录为 `folder`，视频为 `file`，非视频可在文件夹视图显示或在扫描时跳过。
  * `getStreamURL` 对文件返回可加载本地路径。
  * `getHomeSections` 复用 `createRawSourceHomeSections`。
* 扩展 `DataSourceManager.createDataSource('local')`。
* 扩展 `SettingsView.vue`：
  * 数据源类型卡片允许 `local`。
  * 本地源表单使用目录选择，不走登录字段。
* 扩展 `SourceLibraryView.vue`：
  * 把 `isAlistSource` 收敛为 raw source 判断，支持 `alist` 与 `local`。
  * root label、扫描、缓存加载和 view mode 按 source type 参数化。
* 新增 focused verify 脚本覆盖 LocalFileDataSource 的路径安全、映射、搜索和 raw scan cache key 使用；无法直接访问 Tauri 时使用 dependency injection 或纯函数测试。

## Decision (ADR-lite)

**Context**: OpenList/Alist 已打通原始文件源扫描、分类和 SQLite 缓存；本地文件夹是最适合验证这套链路的下一类数据源。

**Decision**: 先实现只读 `LocalFileDataSource`，复用 OpenList/Alist raw-source media-library UI 和缓存，不在本任务实现文件管理、文件关联或写操作。

**Consequences**: MVP 范围可控、验证快、用户能立即把本地影视库变成海报墙；后续 CloudDrive2/WebDAV 和更多原始文件源可以沿用同一 DataSource + raw scan cache 模式。

## Expansion Sweep

* Future evolution:
  * 本地文件源后续可成为 AI 推荐/RAG 的最稳定数据来源。
  * 后续可加入文件关联、自动监控文件夹变化、海报图片二进制本地缓存。
* Related scenarios:
  * CloudDrive2/WebDAV 应复用同一 raw-source scan cache 和 SourceLibraryView media-library 逻辑。
  * 本地文件源与 OpenList/Alist 在 Home 聚合、继续观看、详情页和播放历史中保持一致心智。
* Failure/edge cases:
  * 目录权限不足、符号链接逃逸、root 被移动、路径大小写差异、Windows/WSL 路径格式差异。
  * 大型本地库扫描需要继续使用现有 max depth/folder/entry 限制。

## Out of Scope

* 不实现文件移动、删除、重命名、上传、整理或写回元数据。
* 不实现系统文件关联。
* 不实现后台文件监听/自动增量扫描。
* 不实现 CloudDrive2/WebDAV、115、123、夸克。
* 不做 Server 侧本地文件连接、STRM 或 302。
* 不执行 `git push`。

## Technical Notes

* Current branch: `develop-new/local-file-datasource`
* Base branch: `develop`
* Existing reusable files:
  * `player/src/services/datasource/alist.ts`
  * `player/src/services/datasource/manager.ts`
  * `player/src/services/datasource/types.ts`
  * `player/src/services/scraper/localScanCache.ts`
  * `player/src/services/scraper/rawSourceIndexScheduler.ts`
  * `player/src/views/SettingsView.vue`
  * `player/src/views/SourceLibraryView.vue`
  * `player/src-tauri/src/commands/raw_scan_cache.rs`
* Existing route/playback context already supports local file playback from selected file paths.
