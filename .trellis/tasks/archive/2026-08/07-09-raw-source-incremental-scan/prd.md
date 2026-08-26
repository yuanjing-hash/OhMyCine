# 原始媒体源全量与增量扫描调度

## Goal

把当前 raw file source 的单一 6 小时自动索引升级为“双扫描机制”：全量扫描用于兜底校准，增量扫描/监听用于尽快发现新增、删除、修改的媒体文件。功能优先覆盖本地文件夹、OpenList/Alist，预留 CloudDrive2 和未来 115/123/Quark 等原始文件源；Emby/Jellyfin 这类自带媒体库和元数据的服务端源不进入 Player 本地扫描调度。

## What I already know

* 当前 `rawSourceIndexScheduler` 只有一个默认自动索引间隔：`6 * 60 * 60 * 1000`。
* 当前 App 启动会触发一次自动索引，并按默认间隔定时触发。
* 当前 SourceLibraryView 首次无 scan cache 时会前台触发当前源/root 的扫描并显示状态。
* 用户明确希望保留 6 小时全量扫描，但新增更短间隔的增量扫描。
* 用户希望每个媒体源都有全量/增量相关时间设置。
* 用户明确指出 Emby 这类源不需要 Player 扫描，因为进入媒体库时会从服务器拉最新数据。
* 本任务必须继续遵守：除非用户明确说推送 GitHub，否则只做本地 git，不 `git push`。
* 用户要求每次代码改动后都刷新 Windows GNU exe。

## Requirements

* 引入扫描模式概念：
  * `full`：全量扫描，默认 6 小时一次，负责完整递归扫描并重建/校准 scan cache。
  * `incremental`：增量扫描，默认短间隔，负责发现源/root 下的新增、删除、大小/修改时间变化，并尽量只重算受影响候选。
* raw file source 调度范围：
  * 当前实现覆盖 `local` 和 `alist`。
  * API/配置命名预留 `clouddrive2` 和未来 raw file source。
  * `emby` / `jellyfin` 不进入 raw scan 调度，设置页不展示扫描间隔控制或展示为“不适用”。
* 每源配置：
  * 支持启用/停用自动全量扫描。
  * 支持配置全量扫描间隔，默认 6 小时。
  * 支持启用/停用自动增量扫描。
  * 支持配置增量扫描间隔，默认短间隔。
  * 支持手动触发全量扫描和增量扫描。
  * 配置只能存非敏感字段，放在 `DataSourceConfig.extra` 的安全键里。
* 本地文件夹监听：
  * 本地文件源优先实现 Tauri/Rust 文件系统 watcher。
  * watcher 事件只用于标记该源/root 需要增量扫描；实际 cache 写入仍走 Player 扫描管线。
  * watcher 不应把本地绝对路径写入 raw scan cache、Home、AI、导出或日志展示。
* 远端源增量：
  * OpenList/Alist 暂无可靠实时事件时，用短间隔 polling/diff 实现近实时增量扫描。
  * 远端不可用或增量失败只标记当前源状态，不影响其他源和文件夹浏览。
* UI：
  * SourceLibraryView 扫描管理中区分全量/增量状态、最近执行时间、错误。
  * 设置页数据源管理中为 raw file source 展示全量/增量调度设置。
  * Home 聚合仍保持源级故障隔离。

## Acceptance Criteria

* [x] App 启动后 raw file source 会按配置启动全量和增量两个调度通道。
* [x] 默认全量扫描间隔为 6 小时，默认增量扫描间隔明显更短且可配置。
* [x] 本地文件夹新增/删除/修改媒体文件后，watcher 或短间隔增量扫描会刷新本地 scan cache 和媒体库显示。
* [x] OpenList/Alist 可通过增量 polling/diff 发现新增/删除/修改，并更新媒体库。
* [x] Emby/Jellyfin 不进入 raw scan scheduler，设置页不会误导用户配置本地刮削扫描。
* [x] 每个 raw source 的全量/增量启用状态和间隔能持久化，且不写入敏感信息。
* [x] 手动全量扫描、手动增量扫描都可用，失败只影响当前源页面/状态。
* [x] 既有首次无缓存自动索引、手动“立即索引/重扫”、Home 故障隔离不回退。
* [x] typecheck/lint/build、相关 verify、Windows GNU build 通过并刷新 exe。
* [x] 本地 commit 完成，不 push GitHub。

## Definition of Done

* 任务上下文和 PRD 完成并进入 `in_progress`。
* 代码保持 DataSource 边界，扫描只能通过 DataSource/Tauri 安全边界读取。
* 本地绝对路径不进入 raw scan cache、AI、导出、Home 或普通 UI 日志。
* 相关验证脚本覆盖调度间隔、增量触发、raw source 过滤、故障隔离和配置持久化。
* `player/src-tauri/target/x86_64-pc-windows-gnu/release/ohmycine-player.exe` 刷新。
* 文档/roadmap 同步。
* 本地 git commit 完成。

## Technical Approach

* 扩展 raw source scheduler：
  * 保留 sourceType + sourceId + rootPath key。
  * 增加 scan kind：`full` / `incremental`。
  * 全量扫描复用现有 `runRawSourceLocalScan()`。
  * 增量扫描先实现保守 MVP：读取当前源/root 文件快照，与上次 cache records 比较 providerPath/size/modified，若有变动则刷新受影响集合；必要时降级成 full scan。
* 扩展 scan cache / schedule record：
  * 保存最近 full/incremental attempt/success/failure。
  * 记录用于 diff 的安全 provider metadata，不保存本地绝对路径。
* 本地 watcher：
  * 在 Tauri 侧添加 root-scoped 文件监听命令/事件，事件 payload 使用 provider path 或 source id + relative path，不暴露 absolute root。
  * 前端接收事件后让 scheduler 标记对应 local source/root 需要 incremental scan。
* 设置页：
  * 在数据源管理的 raw source 卡片/编辑页加入“扫描调度”区。
  * Emby/Jellyfin 隐藏该区或显示“不适用，由媒体服务器管理”。
* 验证：
  * 扩展 `verify:raw-source-index-scheduler`。
  * 新增或扩展 local file datasource verify 覆盖 watcher/incremental 触发。
  * 继续跑 scraper、home fault isolation、raw scan cache、typecheck/lint/build/Windows build。

## Decision (ADR-lite)

**Context**: 单一 6 小时自动扫描无法满足新增文件后快速出现在媒体库的体验；直接把全量扫描改短会导致远端源和大库负载过高。

**Decision**: 使用双通道调度：全量慢扫负责一致性，增量快扫/监听负责新鲜度。本地文件源用 watcher 触发增量，远端 raw source 用短间隔 polling/diff，Emby/Jellyfin 排除。

**Consequences**: 媒体库更新更及时，同时保留全量校准。增量 diff 的准确性依赖 provider metadata，遇到 provider 不给 modified/size 或复杂移动/重命名时可降级全量扫描。

## Out of Scope

* 不在本任务实现真正实时远端事件订阅，OpenList/Alist 先用 polling/diff。
* 不实现 SQLite 二进制图片缓存。
* 不做 Server 侧扫描调度。
* 不把扫描结果写回 OpenList/Alist、本地文件夹或任何 provider。
* 不执行 `git push`。

## Technical Notes

* Branch: `develop-new/raw-source-incremental-scan`
* Base branch: `develop-new/local-library-autoscan-match-fix`
* Existing scheduler: `player/src/services/scraper/rawSourceIndexScheduler.ts`
* Existing full scan runner: `player/src/services/scraper/localScanCache.ts`
* Existing source UI: `player/src/views/SourceLibraryView.vue`
* Existing settings UI: `player/src/views/SettingsView.vue`
* Existing DataSource config boundary: `player/src/services/datasource/types.ts`
