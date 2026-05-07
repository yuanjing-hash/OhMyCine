# Player Emby 媒体库源与 UI MVP

## Goal

优先适配 Emby 作为 OhMyCine Player 的第一个真实媒体库数据源，让用户可以在设置中添加现有 Emby 服务器，在侧边栏进入该媒体库，浏览 Emby 媒体库/影视条目，并从条目进入播放流程。

## Background

当前 Player 已经具备 DataSource 类型定义、数据源配置 Pinia store、动态数据源侧边栏、Home/SourceLibrary/Settings/Player 路由和 Cinema OS/liquid-glass 视觉基础，但实际数据仍主要是占位内容：没有 concrete `EmbyDataSource`、没有 DataSourceManager/registry、Settings 仍是静态设置页，SourceLibrary 仍是占位浏览 UI。

用户当前有现成的 Emby 服务器，优先级调整为先把 Emby 当作真实媒体库源接进 Player；内嵌视频渲染仍保留为后续任务，不阻塞本任务。由于当前播放器窗口内视频仍未完成，本任务只要求把 Emby 媒体条目接入到现有播放加载流程，并保持 UI 对视频渲染状态的诚实表达。

## Research References

- [`research/emby-datasource.md`](research/emby-datasource.md) — Emby 可通过 user-scoped libraries/items/detail/stream endpoints 映射到 OhMyCine DataSource；主要风险是 token/URL 敏感信息存储与泄露。

## What I already know

- `player/src/services/datasource/types.ts` 已定义 `DataSourceType`、`DataSourceConfig`、`DataSource`、`MediaItem`、`MediaLibrary`、`HomeSection`、`MediaDetail` 等核心类型。
- `player/src/stores/datasource.ts` 已有配置 CRUD 和 localStorage 持久化，但 `loadHomeSections()` 仍生成硬编码占位数据。
- `player/src/components/layout/DataSourceSidebar.vue` 会自动渲染已配置的数据源，并且已有 `emby` 图标映射。
- `player/src/views/SettingsView.vue` 目前只是静态占位页，需要新增数据源配置 UI。
- `player/src/views/SourceLibraryView.vue` 会根据 `/source/:sourceId` 找到配置，但媒体库浏览区域仍是占位。
- `player/src/components/media/HeroCarousel.vue` 可用，但 `MediaCard` / `MediaRow` / `MediaGrid` 等媒体库浏览组件尚缺。
- `ofetch` 已在 Player 依赖中，可用于 Emby HTTP API 客户端。
- 当前 libmpv 后端加载/控制可用，但窗口内视频渲染尚未完成；不可为了播放 Emby 条目重新打开外部 mpv 窗口。

## Requirements

- Implement Emby as the first concrete Player DataSource behind the existing DataSource abstraction.
- Add an Emby client/service that can test connection, list libraries, list library items, search items, get item detail, and generate a playback stream URL.
- Add a DataSourceManager/factory layer that instantiates sources from stored configs without coupling views directly to Emby.
- Expand Settings UI to support adding/editing/removing an Emby media source with Cinema OS/liquid-glass styling.
- Emby settings MVP must include at least display name, server URL, and access credential input.
- Source sidebar should show configured Emby sources through the existing data-source-driven navigation.
- Expand SourceLibraryView so an Emby source shows real library cards and media items instead of placeholders.
- Add reusable media browsing components as needed, such as poster/media cards and rows/grids, with missing-poster fallbacks.
- Wire Emby media item play action to the existing Player route/loading flow via `getStreamURL()`.
- Keep Player independent-first: no OhMyCine Server dependency for Emby browsing/playback.
- Do not store new Emby credentials in plaintext localStorage if avoidable; use a credential reference / secure-storage boundary when implementing sensitive fields, or clearly limit MVP behavior if secure storage is not yet available.
- Redact Emby tokens/API keys and tokenized stream/image URLs from logs/errors/UI.
- Update `docs/architecture/06-roadmap.md` if Emby/DataSource completion status changes.

## Acceptance Criteria

- [ ] User can add an Emby source from Settings with server URL and credential fields.
- [ ] User can test the Emby connection and see success/failure feedback.
- [ ] Added Emby source appears in the left data source sidebar.
- [ ] Opening `/source/:sourceId` for an Emby source displays real Emby libraries from the server.
- [ ] Selecting a library displays real media items with poster/title/year/type fallback handling.
- [ ] Basic Emby search or source-level item querying is implemented if needed for the UI flow.
- [ ] Clicking play on an Emby video item obtains a stream URL through the DataSource interface and routes/loads it through the existing Player flow.
- [ ] Emby unavailable/auth failure states are visible and do not break local playback or other sources.
- [ ] Sensitive tokens/API keys are not printed to logs or shown in user-facing errors.
- [ ] Existing local file picker/playback route behavior still works.
- [ ] Frontend typecheck, lint, and build pass.
- [ ] Tauri/Rust checks still pass if Tauri/secure-storage/plugin code is touched.
- [ ] Windows GNU Tauri package build succeeds from WSL; Windows runtime behavior remains user-verified.

## Definition of Done

- Emby can be configured as a real Player media source and browsed from the app UI.
- The implementation follows the DataSource abstraction rather than hard-coding Emby behavior in route views.
- The UI matches current Cinema OS/liquid-glass styling and has clear loading/empty/error states.
- Docs/roadmap reflect the exact state achieved.
- Validation commands pass, with Windows runtime verification explicitly left to the user.

## Technical Approach

1. Add an Emby DataSource implementation under `player/src/services/datasource/` that maps Emby libraries/items/details/stream URLs to current OhMyCine DataSource types.
2. Add a DataSourceManager/factory so the store/views can work with generic sources by id/type.
3. Refactor the datasource store to instantiate and call real sources while preserving existing config CRUD and placeholder-safe empty states.
4. Build the Settings data-source UI for Emby add/edit/test/remove using existing design tokens and Vue Composition API patterns.
5. Build/extend SourceLibraryView to show libraries and media items for the active DataSource.
6. Add reusable MediaCard/MediaGrid or MediaRow components where necessary, handling missing posters gracefully.
7. Connect item play actions to `getStreamURL()` and route to Player without changing the current no-external-window mpv safety behavior.
8. Add redaction and safe error display for tokenized URLs and API errors.
9. Update roadmap status after implementation.

## Decision (ADR-lite)

**Context:** The earlier roadmap planning recommended embedded video rendering first, but the user has a ready Emby server and wants the next Player milestone to prioritize real media library browsing and Emby UI.

**Decision:** Re-scope the current execution task from embedded video rendering to Emby DataSource/UI MVP. Keep embedded rendering as a later dedicated task.

**Consequences:** Player will become useful as a real media library browser sooner, while actual in-window video rendering remains incomplete. Playback actions should integrate with the existing route/backend flow but must not reintroduce external mpv windows or claim embedded video completion.

## Out of Scope

- Completing native embedded video rendering / `MpvRenderContext` in this task.
- Implementing Jellyfin/OpenList/Alist/CloudDrive2/local folder sources beyond keeping abstractions extensible.
- Full Emby admin/settings coverage, multi-user profile management, parental controls, live TV, downloads, or server-side playback reporting.
- Full playback progress sync/scrobbling back to Emby unless it is trivial after core browsing works.
- TMDB scraping/local metadata cache for Emby items.
- Windows-native manual UI inspection by the assistant.

## Validation Plan

Run from WSL/Linux:

- `cd player && npm run typecheck`
- `cd player && npm run lint`
- `cd player && npm run build`
- `cd player/src-tauri && cargo check` if Tauri/Rust/secure-storage code changes
- `cd player && npm run tauri:build:windows`

Do not claim Windows runtime visual verification from these commands alone; report that the package builds and leave Windows runtime inspection to the user.

## Technical Notes

- Follow `.trellis/spec/frontend/type-safety.md` for DataSource response typing and avoiding broad `any`.
- Follow `.trellis/spec/frontend/state-management.md` for Pinia/store boundaries.
- Follow `.trellis/spec/frontend/directory-structure.md` for Player services/components organization.
- Follow `.trellis/spec/frontend/component-guidelines.md` for component structure, media components, and liquid-glass styling.
- Follow `.trellis/spec/frontend/quality-guidelines.md` for validation expectations.
- Consult `docs/architecture/07-security-design.md` before persisting Emby credentials or tokenized URLs.
