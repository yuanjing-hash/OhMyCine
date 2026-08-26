# 本地媒体库首次扫描反馈与无年份电影匹配

## Goal

修复本地文件夹数据源首次进入时媒体库页空白、用户必须手动点“扫描”才有内容的问题；同时修复 `复仇者联盟.mp4` 这类无年份中文电影名被 TMDB 自动匹配到续集（例如第三部）的偏差，让无年份但精确标题的电影更接近 Emby 的直觉匹配结果。

## What I already know

* 用户反馈：新添加的媒体库文件夹第一次点进去，页面空空的，手动点扫描后才正常。
* 用户反馈：`复仇者联盟.mp4` 被识别成 `复仇者联盟3`，而 Emby 能识别成第一部。
* 当前 App 启动会调用 `rawSourceIndexScheduler.startAutoIndexing()`，但 `SourceLibraryView` 首次进入只读取 scan cache；如果 cache 未写入、自动任务仍在跑、或者状态还没同步，媒体库区域会显示空态。
* 当前 `scanStatusLabel` 有“等待自动索引/扫描中”，但默认扫描管理面板是收起的，用户看不到状态。
* 当前 `TmdbScraper.selectBestSearchResult()` 对无年份候选在 acceptable title match 中按 `popularity` 选最高；`isAcceptableTitleMatch()` 允许 `key.includes(queryKey)`，导致续集标题包含基础片名时也被视为可接受。
* 本任务必须继续保持 Player independent-first，不引入 Server 依赖。
* 本任务必须继续遵守：除非用户明确说推送 GitHub，否则只做本地 git，不 `git push`；代码改动完成后重新生成 Windows GNU exe。

## Requirements

* 本地/OpenList 这类 raw file source 首次进入媒体库页时，如果没有 scan cache，应主动触发当前源/root 的索引或至少绑定现有 scheduler in-flight 状态。
* 媒体库页默认区域要显示明确的索引状态：
  * 正在索引时显示进度/忙碌状态、根目录和最近扫描日志。
  * 尚无缓存且自动索引排队/等待时，不显示“空媒体库”的误导文案。
  * 索引失败时仍保留文件夹浏览入口，并给出可手动重扫的提示。
* 自动索引不能阻塞文件夹浏览和播放；切到文件夹视图仍应可用。
* TMDB 自动匹配要区分“精确标题”和“包含基础标题的续集标题”：
  * 无年份且查询标题与某个结果标题/原名完全一致时，优先选择完全一致项。
  * 包含匹配（例如 `复仇者联盟3` 包含 `复仇者联盟`）不能单纯靠 popularity 压过精确匹配。
  * manual identification 的候选排序也应把精确标题放在更前面。
* 增加 focused verify 覆盖：
  * raw source 页面/调度器首次无 cache 时能进入 running/completed 状态并加载 cache。
  * `复仇者联盟` 无年份搜索结果在第一部和续集同时出现时优先选精确标题。

## Acceptance Criteria

* [x] 新添加本地媒体库后第一次进入，不再只看到空媒体库；页面能显示正在自动索引/可见状态。
* [x] 自动索引完成后，媒体库分类卡片自动出现，无需用户手动点扫描。
* [x] 自动索引失败时，错误只显示在当前源页，文件夹浏览和播放仍可用。
* [x] 手动“立即索引/重扫”仍可用，并继续显示日志。
* [x] `复仇者联盟.mp4` 这类无年份精确标题优先匹配第一部，而不是靠 popularity 选到标题包含基础片名的续集。
* [x] 既有 `阿凡达.mp4`、`动漫/剧名/Season 01/S01E01.mkv` 等解析/分类回归不破。
* [x] typecheck/lint/build、相关 verify、Windows GNU build 通过并刷新 exe。
* [x] 本地 commit 完成，不 push GitHub。

## Definition of Done

* PRD、implement/check 上下文完成并进入 `in_progress`。
* 代码保持 DataSource 边界，视图不绕过 DataSource 直接读本地文件。
* 无本地绝对路径写入 raw scan cache / AI / export 默认路径。
* 相关验证脚本和回归脚本通过。
* `player/src-tauri/target/x86_64-pc-windows-gnu/release/ohmycine-player.exe` 刷新。
* 路线图或设计文档在行为状态变化时同步更新。
* 本地 git commit 完成。

## Verification

* `npm run verify:scraper` 通过，新增 `复仇者联盟` vs `复仇者联盟3：无限战争` 夹具，确认无年份精确标题优先。
* `npm run verify:raw-source-index-scheduler` 通过，覆盖首次前台扫描 running/completed 状态与 in-flight 绑定。
* `npm run verify:local-file-datasource`、`npm run verify:home-fault-isolation`、`npm run verify:raw-scan-cache`、`npm run verify:tmdb-auth` 通过。
* `npm run typecheck`、`npm run lint`、`npm run build` 通过。
* Windows GNU build 通过，刷新 `player/src-tauri/target/x86_64-pc-windows-gnu/release/ohmycine-player.exe`：`2026-07-09 11:17:57 +0800`，`29869539` bytes。

## Technical Approach

* `SourceLibraryView` 增加 raw-source 当前 target status 读取/刷新：
  * 首次进入 raw source 时，在 `ensureSource()` 成功后读取 scheduler status。
  * 如果没有 scan cache 且不是正在运行/冷却失败的状态，主动对当前 source/root 触发一次 `forceScan` 或等价的 foreground auto scan。
  * 页面默认展示一个轻量索引状态面板/进度条，而不是空 `MediaGrid`。
* `rawSourceIndexScheduler` 如需扩展，保持 sourceType + sourceId + rootPath key，不破坏 app startup 后台索引。
* `TmdbScraper` 调整搜索结果评分：
  * 增加 title match quality：exact normalized title/originalTitle > compact exact > token overlap > contains。
  * `selectBestSearchResult()` 在无年份时优先 exact title，再用 score/popularity 作为 tie-breaker。
  * `rankSearchResults()` 使用同一评分，保证手动识别候选顺序一致。
* 在 `verify-scraper-title-classification.ts` 或新增 focused verify 中加入 `复仇者联盟` 匹配夹具。

## Decision (ADR-lite)

**Context**: raw source 自动索引已有后台 scheduler，但数据源页没有把 scheduler 状态变成用户可见反馈；TMDB 无年份匹配目前 popularity 权重过强。

**Decision**: 在数据源页做 source/root-scoped 的首次索引状态闭环，并把 TMDB title match quality 纳入自动匹配与候选排序。

**Consequences**: 首次进入本地库会更像真实媒体库初始化过程；无年份电影名会更保守、更符合用户直觉。少数确实需要匹配续集但文件名未带数字/年份的情况，仍可通过右键手动识别修正。

## Out of Scope

* 不实现完整百分比进度（当前扫描器没有总文件数预扫描，MVP 使用 indeterminate progress + 文件/日志计数）。
* 不实现文件系统 watcher 或增量扫描。
* 不改变 TMDB API provider 或引入外部 metadata 服务。
* 不实现全局扫描队列 UI。
* 不执行 `git push`。

## Technical Notes

* Branch: `develop-new/local-library-autoscan-match-fix`
* Base branch: `develop-new/local-file-datasource`
* Likely files:
  * `player/src/views/SourceLibraryView.vue`
  * `player/src/services/scraper/rawSourceIndexScheduler.ts`
  * `player/src/services/scraper/tmdb.ts`
  * `player/scripts/verify-scraper-title-classification.ts`
  * Potential new focused verify script for source page scheduler behavior.
