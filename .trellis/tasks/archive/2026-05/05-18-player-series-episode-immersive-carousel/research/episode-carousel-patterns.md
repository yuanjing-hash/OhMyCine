# Research: Episode Carousel Patterns

- **Query**: Research episode browsing UI patterns for Emby and comparable media apps (Plex/Jellyfin/Apple TV style if useful) for a desktop media detail page. Focus on horizontal episode cards/carousels, season switching, lazy rendering/pagination, metadata shown on cards, previous/next controls, and how to map the patterns into OhMyCine's Cinema OS dark/liquid-glass/artwork-first design.
- **Scope**: mixed
- **Date**: 2026-05-18

## Findings

### Files Found

| File Path | Description |
|---|---|
| `player/src/views/MediaDetailView.vue` | Current media detail page. It already loads series seasons and episodes, renders season pills, loading skeletons, episode cards, progress bars, and play/detail actions. |
| `player/src/services/datasource/types.ts` | Common `MediaItem`, `MediaDetail`, and `DataSource` shape used by all media sources. Episode metadata fields include `posterUrl`, `backdropUrl`, `overview`, `duration`, `resumePosition`, `progress`, `seasonNumber`, and `episodeNumber`. |
| `player/src/services/datasource/emby.ts` | Emby implementation for fetching seasons/episodes, mapping Emby episode indices, progress, and artwork into common `MediaItem` fields. |
| `player/src/components/media/MediaCard.vue` | Reusable media card behavior. Episodes use 16:9 artwork (`backdropUrl ?? posterUrl`) and metadata subtitles such as episode number and duration. |
| `docs/architecture/03-player-design.md` | Player design direction: independent-first Player, DataSource boundary, Cinema OS dark/liquid-glass/poster-centric UI, and planned media row/detail components. |

### Code Patterns

- `MediaDetailView.vue:18-31` stores detail, seasons, episodes, selected season, loading/error states, and playback progress separately, which matches a season-switching episode browser.
- `MediaDetailView.vue:88-91` loads seasons only when `nextDetail.type === 'series'`; non-series detail only refreshes playback progress for the item.
- `MediaDetailView.vue:110-160` resolves series children, separates `season`/`folder` from direct `episode` items, loads the first season by default, and reloads episodes when a season is selected.
- `MediaDetailView.vue:433-443` currently renders seasons as horizontally scrollable pill buttons.
- `MediaDetailView.vue:445-447` uses a six-card skeleton while season content is loading.
- `MediaDetailView.vue:449-490` currently renders episodes as a two-column responsive grid with 16:9 still artwork, runtime, `第 N 集 · title`, overview, resume badge/progress, and play/detail buttons.
- `MediaDetailView.vue:456-464` uses lazy image loading and an inline watched-progress bar on episode artwork.
- `MediaDetailView.vue:340-346` formats episode titles and runtime from `episodeNumber` and `duration`.
- `player/src/services/datasource/types.ts:0-23` defines the common card-ready fields available to any provider: name, type, poster/backdrop, year/rating/overview, duration, resume/progress, season/episode numbers.
- `player/src/services/datasource/types.ts:122-143` keeps browsing behind `DataSource.list`, `getDetail`, and `getStreamURL`, so season/episode UI should remain provider-agnostic.
- `player/src/services/datasource/emby.ts:601-608` fetches seasons through Emby `/Shows/{seriesId}/Seasons` with `Fields` and image query options.
- `player/src/services/datasource/emby.ts:610-622` fetches episodes through `/Users/{UserId}/Items` using `ParentId`, `IncludeItemTypes: Episode,Folder`, sort by `ParentIndexNumber,IndexNumber,SortName`, and `Limit: 200`.
- `player/src/services/datasource/emby.ts:869-898` maps Emby `ParentIndexNumber` and `IndexNumber` into `seasonNumber` and `episodeNumber`, and maps `UserData.PlayedPercentage` / `PlaybackPositionTicks` into common progress fields.
- `player/src/services/datasource/emby.ts:923-985` prefers episode-specific thumbnail/backdrop imagery, then falls back to parent thumb/backdrop artwork.
- `player/src/components/media/MediaCard.vue:22-31` already has episode-specific subtitle metadata: episode number plus runtime.
- `player/src/components/media/MediaCard.vue:33-42` treats episode cards as 16:9 artwork cards instead of poster-ratio cards.
- `docs/architecture/03-player-design.md:4-7` sets the visual target: independent player, Cinema OS, liquid glass, dark theme, cinematic typography, and libmpv playback.
- `docs/architecture/03-player-design.md:87-95` names planned media display primitives such as `MediaCard`, `MediaGrid`, `MediaRow`, `MediaDetail`, poster wall, hero carousel, and continue-watching panel.

### External References

- [Emby REST API: ItemsService `getItems`](https://dev.emby.media/reference/RestAPI/ItemsService/getItems.html) — Documents query parameters relevant to lazy episode browsing: `StartIndex` starts from a record index, `Limit` caps returned records, `ParentId` localizes a query to a folder/item, `IncludeItemTypes` filters item types, and `Fields` controls extra metadata returned.
- [Jellyfin OpenAPI: Items `GetItems`](https://api.jellyfin.org/#tag/Items/operation/GetItems) — Jellyfin exposes comparable paging and filtering concepts (`startIndex`, `limit`, `parentId`, `includeItemTypes`, `fields`), so a provider-agnostic UI can model pagination without assuming Emby-only APIs.
- [Jellyfin web issue #892: display episodes on the show core page](https://github.com/jellyfin/jellyfin-web/issues/892) — Shows a comparable media-app UX concern: for single-season/no-explicit-season shows, users may expect episodes directly on the series detail page instead of requiring another navigation level.
- [Apple TV app user guide](https://support.apple.com/guide/tvapp/welcome/web) and [Apple TV season-by-season article](https://appleosophy.com/2023/04/16/how-to-see-complete-series-of-shows-season-by-season-in-the-apple-tv-app/) — Useful as a reference for modern streaming-app expectations around "Up Next"/continue context and season-by-season episode access, though not an official UI component specification.
- [Emby Theater Web App](https://emby.tv/emby-theater-web.html) — Useful for positioning: Emby’s desktop/TV-style client emphasizes a lean-back media browsing experience, so episode browsing should support remote/keyboard-friendly horizontal navigation as well as mouse.

### Comparable UI Patterns

#### Horizontal episode cards / carousels

- Streaming and media-server clients commonly use 16:9 episode still cards for episodes, not 2:3 posters. This matches OhMyCine’s existing `MediaCard.vue` behavior for `episode` items and Emby’s episode image mapping.
- A desktop detail page pattern is: large series hero at the top, then an episode rail underneath. The rail shows a single season’s episodes horizontally when the focus is artwork-first browsing, or a denser grid/list when information density is more important.
- Horizontal rails usually expose partial next cards at the right edge to communicate scrollability. Desktop apps often pair this with visible previous/next chevrons that appear on hover/focus.
- Episode cards in horizontal rails are usually wide enough for a still image and compact metadata, with the synopsis either truncated under the card or revealed/expanded on hover/focus.

#### Season switching

- Common patterns are horizontal season pills/tabs, a season dropdown, or a compact selector above the episode rail.
- Horizontal season pills are effective when season count is modest and match OhMyCine’s current implementation in `MediaDetailView.vue:433-443`.
- A dropdown/combobox pattern scales better for very long-running shows; a hybrid pattern is common: show selected season label/count prominently and keep the selector near the rail heading.
- Single-season shows can skip or de-emphasize the season selector and show episodes directly; this is reflected by the Jellyfin web issue requesting episodes on the show core page for one-season/no-season shows.

#### Lazy rendering / pagination

- Emby and Jellyfin item APIs support `StartIndex`/`Limit` (Emby) and `startIndex`/`limit` (Jellyfin), plus parent scoping via `ParentId`/`parentId`. These map naturally to paged episode loading per season.
- Current OhMyCine Emby code fetches up to `Limit: 200` episodes per season (`emby.ts:610-620`). This covers typical seasons but is not a true incremental paging model.
- For UI rendering, current images already use `loading="lazy"` and `decoding="async"` on episode images (`MediaDetailView.vue:457`), matching carousel/grid lazy-image expectations.
- For very long seasons, comparable apps avoid mounting every rich card at once by paging, virtualizing, or loading the next page as the rail nears the end.

#### Metadata shown on episode cards

Common episode-card metadata across Emby/Jellyfin/Plex/Apple-TV-style layouts:

| Metadata | Typical placement | OhMyCine field |
|---|---|---|
| Episode number | Title prefix or small label (`S01E03`, `第 3 集`) | `seasonNumber`, `episodeNumber` |
| Episode title | Primary text below/over artwork | `name` |
| Runtime | Small secondary text | `duration` |
| Synopsis | Truncated body text or hover/focus reveal | `overview` |
| Resume/watched progress | Thin progress bar on image bottom; resume badge/action | `progress`, `resumePosition` |
| Still image | 16:9 thumbnail/backdrop | `backdropUrl ?? posterUrl` |
| Play action | Center overlay or button below card | `getStreamURL(id)` via `playItem` |
| Detail action | Secondary action, often context menu or card click | route to media detail |
| Air date | Often shown by media-server apps when available | Not currently in `MediaItem` |
| Rating | Sometimes shown, usually less prominent for episodes | `rating` exists but current episode UI does not emphasize it |

#### Previous/next controls

- Desktop horizontal rails generally need both native wheel/trackpad scrolling and explicit previous/next buttons.
- Previous/next buttons are usually placed at the right of the section heading or as edge overlays centered vertically on the rail.
- In cinematic apps, controls often stay hidden until hover/focus to avoid competing with artwork.
- Keyboard/remote behavior usually treats each episode card as focusable and moves focus left/right; the rail scrolls to keep the focused card visible.

### Mapping to OhMyCine Cinema OS

- Keep the series hero as the artwork-first anchor: background/backdrop, dark gradients, poster/logo/title, and a minimal instruction that episodes are selected below.
- Move the episode area toward a cinematic rail: wide 16:9 still cards on a dark/liquid-glass surface, with partial offscreen cards and soft edge fades to imply horizontal navigation.
- Use liquid-glass season controls: translucent pill tabs or a glass dropdown above the rail, with the selected season count (`Season name · N 集`) preserved from the current detail page.
- Keep card metadata restrained: episode number/title, runtime, progress, and a short synopsis. Avoid making cards visually heavier than the artwork.
- Use hover/focus-revealed chrome: play button, detail button, and previous/next arrows can appear when the user hovers the rail or focuses a card.
- Keep resume state visual and immediate: a white progress strip at the bottom of the still image and a subtle "继续播放"/"可继续播放" affordance.
- Preserve provider-agnostic behavior: the rail should consume `MediaItem[]` and `DataSource.list`, not Emby-specific records.
- For single-season/direct-episode series, show the episode rail directly and reduce season selector emphasis.

### Related Specs

- `docs/architecture/03-player-design.md` — Player visual and architectural direction for Cinema OS UI and DataSource boundaries.
- `.trellis/spec/frontend/type-safety.md` — Related frontend spec is modified in the working tree but was not read in detail for this UI-pattern research.

## Caveats / Not Found

- No official Emby/Plex/Jellyfin design-system document for exact episode-carousel UI behavior was found through the available web checks; findings combine accessible API docs, public issue/article references, current code, and common media-app UI conventions.
- Plex-specific official UI documentation for episode-card layout was not located in the available search results; Plex is included only as a comparable app pattern, not as a cited implementation source.
- Current `DataSource` has no paginated `list` signature and `MediaItem` has no air-date field, so API-supported pagination and air-date display are research findings rather than existing OhMyCine contracts.
