# 完善 OpenList 刮削缓存与修正体验

## Goal

将 OpenList/Alist 本地刮削结果、扫描日志、手工识别和图片覆盖从浏览器 `localStorage` 迁移到 Tauri app data 下的 SQLite 持久化层，让 Player 的海报墙索引和用户修正更可靠，同时保持 OpenList/Alist 只读、不写回、不保存敏感播放 URL 的安全边界。

## What I already know

* 当前仓库已进入 Player 独立版 MVP 中后段，Server/CLI 仍是占位，当前开发应继续收尾 Player。
* OpenList/Alist DataSource 已支持登录、根目录选择、目录浏览、搜索和播放。
* OpenList/Alist 本地扫描、TMDB 匹配、海报墙、扫描日志、手工识别和图片覆盖入口已经存在。
* 当前扫描缓存由 `player/src/services/scraper/localScanCache.ts` 写入 `localStorage`，容易受浏览器存储限制、清理或 payload 过大影响。
* 手工识别和图片覆盖已经统一调用 `saveRawSourceScanCache`，迁移缓存 API 可覆盖扫描、修正、图片覆盖三条路径。
* Rust/Tauri 侧已有 SQLite 风格的本地持久化命令：播放历史、偏好和凭据边界，可复用 app data 目录、命令注册、输入校验和错误返回风格。
* 用户已要求专业 Git 工作流：长期开发从 `develop` 开始，新功能开功能分支；除非明确要求推送 GitHub，否则只做本地 Git 操作，不执行 `git push`。

## Requirements

* 新增 Player/Tauri 本地原始文件源扫描缓存持久化层，使用 Tauri app data 下的 SQLite。
* 缓存按 `sourceType + sourceId + rootPath` 隔离，支持读取、写入、删除当前源扫描缓存。
* 迁移现有 `loadRawSourceScanCache`、`saveRawSourceScanCache`、`clearRawSourceScanCache` 使用方，使扫描结果、手工识别、图片覆盖都落到 SQLite。
* 保持现有 `RawLocalScanCache` sanitize 逻辑，写入前继续清理敏感 URL、异常 provider path 和不在 root 内的条目。
* 保留浏览/播放兜底：SQLite 读写失败时，不影响 OpenList/Alist 文件夹浏览和视频播放。
* 保留只读边界：不对 OpenList/Alist 做上传、移动、重命名、删除或元数据写回。
* 不保存 OpenList/Alist 账号、密码、token、签名播放 URL 或完整敏感 provider URL。
* 为迁移后的缓存读写补充 focused verify 脚本或等价测试，覆盖保存、读取、覆盖更新、清理和 root/source 隔离。
* 更新路线图中 OpenList/Alist 刮削缓存状态，避免任务状态与计划表脱节。

## Acceptance Criteria

* [x] 用户手动扫描 OpenList/Alist 后，扫描缓存保存到 Tauri app data SQLite，而不是只依赖 `localStorage`。
* [x] 重新打开同一 OpenList/Alist 数据源时，能从 SQLite 恢复扫描结果、分类、日志、匹配元数据和海报墙展示。
* [x] 手工识别 TMDB 结果后，修正结果能持久保存并在刷新/重进页面后继续生效。
* [x] 手工图片覆盖后，poster/logo/backdrop 选择能持久保存并在刷新/重进页面后继续生效。
* [x] 不同 `sourceId`、`sourceType`、`rootPath` 的扫描缓存互不串用。
* [x] 缓存写入前继续经过 sanitize，敏感 query、账号密码、tokenized stream URL 不会进入 SQLite。
* [x] SQLite 读写失败时，页面展示可理解错误或降级提示，文件夹浏览和播放仍可继续。
* [x] 现有 raw source index scheduler 行为保持一致，自动索引冷却和状态显示不因缓存迁移回退。
* [x] 相关 verify/typecheck/lint 在当前环境可运行范围内通过；无法运行的命令需要记录原因。
* [x] `docs/architecture/06-roadmap.md` 与任务实际完成状态保持一致。

## Technical Approach

* 新增 Rust Tauri command 模块，例如 `commands/raw_scan_cache.rs`，使用 `rusqlite` 管理 `raw_scan_cache.sqlite`。
* SQLite 表建议使用单表 JSON payload 方案：`cache_key/source_id/source_type/root_path/payload/updated_at`。MVP 优先稳定保存完整 sanitized cache，不在本任务拆多张关系表。
* 前端新增异步缓存 API，封装 Tauri `invoke`，并保留现有 `RawLocalScanCache` 类型和 sanitize 入口。
* 将调用点改为 async：`SourceLibraryView`、`AlistDataSource.getHomeSections`、`rawSourceIndexScheduler.getStatus` 及相关扫描流程。
* 现有 `saveRawSourceScanCache` 的同步 API 可收敛为 async；verify 脚本同步更新。
* 若需要兼容测试环境，可允许依赖注入内存存储或提供纯函数级 serializer/sanitizer 测试，避免 Node 环境直接调用 Tauri。

## Decision (ADR-lite)

**Context**: 当前海报墙扫描结果和用户修正保存在 `localStorage`，这对大型媒体库、桌面应用数据可靠性和长期用户修正都不够稳。

**Decision**: 本任务先使用 Tauri app data SQLite 保存完整 sanitized scan cache JSON payload，不在 MVP 中拆分完整元数据库或下载图片二进制缓存。

**Consequences**: 实现范围小、迁移风险低，并能快速解决“扫描和修正结果可靠保存”。后续如果要做跨源搜索、增量扫描、图片离线缓存或 AI/RAG，可再把 JSON payload 演进为关系表和独立 artwork cache。

## Expansion Sweep

* Future evolution: 后续可扩展为 CloudDrive2/LocalFile/115 等原始文件源共用扫描库，并为 AI 推荐提供本地元数据索引。
* Related scenarios: 扫描缓存、手工修正、图片覆盖和首页聚合都应读同一个持久化结果，避免页面间状态不一致。
* Failure/edge cases: 需要处理缓存 payload 过大、SQLite 打不开、数据损坏、rootPath 改变、敏感 URL 混入、扫描中页面关闭等情况。

## Out of Scope

* 不实现 CloudDrive2、LocalFile、115/123/夸克 DataSource。
* 不实现 Server 侧刮削、STRM、302 代理或媒体流水线。
* 不下载 TMDB 海报/背景图片二进制到本地文件；本任务只持久化已 sanitize 的元数据 URL 和用户选择。
* 不做完整手工匹配工作台重设计；沿用当前已有识别/图片覆盖 UI。
* 不对 OpenList/Alist 写入、重命名、移动、删除或回写元数据。
* 不执行 `git push`；除非用户后续明确要求推送到 GitHub。

## Definition of Done

* PRD、implement/check 上下文完成并进入 in_progress。
* 代码改动保持 Player 独立优先，不引入 Server 依赖。
* 前端变更通过 `npm run typecheck`、`npm run lint`、相关 verify 脚本或记录当前环境限制。
* Rust/Tauri 变更通过相关 `cargo check` 或记录当前环境限制。
* 路线图状态同步更新。
* 本地 Git 提交可做；不推送 GitHub。

## Technical Notes

* Current branch: `develop-new/openlist-scrape-cache`, created from local `develop`.
* Likely impacted files:
  * `player/src/services/scraper/localScanCache.ts`
  * `player/src/services/scraper/rawSourceIndexScheduler.ts`
  * `player/src/views/SourceLibraryView.vue`
  * `player/src/services/datasource/alist.ts`
  * `player/src-tauri/src/commands/mod.rs`
  * `player/src-tauri/src/main.rs`
  * new Tauri command module for raw scan cache
  * focused verify script under `player/scripts/`
  * `docs/architecture/06-roadmap.md`
* Existing SQLite command references:
  * playback history uses `player/src-tauri/src/commands/history.rs`
  * playback preferences use `player/src-tauri/src/commands/preference.rs`
  * credential boundary uses `player/src-tauri/src/commands/credential.rs`

## Verification

* Passed `verify:raw-scan-cache` equivalent via Linux Node + `tsx`.
* Passed `verify:home-fault-isolation` equivalent via Linux Node + `tsx`.
* Passed `verify:raw-source-index-scheduler` equivalent via Linux Node + `tsx`.
* Passed `verify:scraper` equivalent via Linux Node + `tsx`.
* Passed `vue-tsc --noEmit` via Linux Node.
* Passed ESLint via Linux Node.
* Passed Vite production build via Linux Node.
* Passed `cargo fmt --check` and `cargo check` for `player/src-tauri`.
* Note: direct `npm run ...` still uses Windows npm in this WSL environment and fails before code execution on UNC path / `TMPDIR=...`; Linux Node direct execution was used for actual verification.
