# Frontend Directory Structure

> How OhMyCine Player frontend and Tauri code are organized.

---

## Overview

Player uses Tauri v2 + Vue 3 + TypeScript + UnoCSS. The web UI lives under `player/src/`; Rust/Tauri integration lives under `player/src-tauri/`. Keep desktop runtime concerns explicit and do not assume WSL-built artifacts are Windows-native desktop packages.

The repository is design-first. Use existing directories when present and do not create broad rewrites to match a planned tree unless the task explicitly asks for it.

---

## Target Directory Layout

```text
player/
├── src-tauri/
│   ├── src/
│   │   ├── commands/       # Tauri commands exposed to Vue
│   │   ├── mpv/            # libmpv FFI/player integration
│   │   └── render/         # platform rendering contexts
│   ├── Cargo.toml
│   └── tauri.conf.json
├── src/
│   ├── assets/             # fonts, images, icons
│   ├── components/
│   │   ├── ui/             # reusable Cinema OS UI primitives
│   │   ├── layout/         # app layout, sidebar, window chrome
│   │   ├── player/         # playback UI, controls, track menus
│   │   ├── media/          # cards, grids, hero, poster wall
│   │   └── common/         # search, settings, server status
│   ├── views/              # route-level pages
│   ├── stores/             # Pinia stores
│   ├── composables/        # reusable Composition API logic
│   ├── services/           # datasource, scraper, AI, sync clients
│   ├── router/             # Vue Router
│   ├── i18n/               # translations
│   ├── styles/             # CSS variables, glass, animations, global
│   └── utils/              # pure utilities
└── scripts/                # build/download helpers
```

---

## Module Organization

### Views

Views are route-level orchestration components. They may compose stores, services, and UI components, but should not contain reusable business logic that belongs in composables/services.

Important views:

- `HomeView`: aggregated home from all configured data sources.
- `SourceLibraryView`: one data source's library/file view.
- `PlayerView`: video surface and controls.
- `SettingsView`: local settings and data source management.
- Server-enhanced views such as downloads/discovery/follow must degrade when Server is disconnected.

### Components

- `components/ui`: generic primitives such as buttons, cards, dialogs, inputs, sliders.
- `components/layout`: shell, dynamic data-source sidebar, window chrome.
- `components/player`: playback controls, progress, subtitle/audio menus, playlist, danmaku overlay.
- `components/media`: MediaCard, MediaGrid, MediaRow, HeroCarousel, PosterWall, details.

### Services

- `services/datasource`: DataSource types, manager, Emby/Jellyfin/OpenList/Alist/CloudDrive2/local/server implementations.
- `services/scraper`: filename parser, TMDB, metadata DB/cache.
- `services/ai`: Player-side RAG recommendations using user-provided keys.
- `services/sync`: Player ↔ Server structural/full sync flows.
- `services/danmaku`: parsers, sources, renderer.

---

## Naming Conventions

- Vue components use PascalCase file names: `HeroCarousel.vue`.
- Composables use `useX.ts`: `useMpv.ts`, `useServer.ts`, `useKeyboard.ts`.
- Pinia stores use domain names: `player.ts`, `media.ts`, `server.ts`, `settings.ts`, `ui.ts`.
- DataSource classes use PascalCase and end with `DataSource`.
- User-facing text should say `OpenList/Alist` or `OpenList (Alist-compatible API)`; code may use `alist` identifiers for API compatibility.

---

## Tauri/Rust Boundaries

Tauri commands expose platform capabilities only:

- Player commands for libmpv control.
- File commands for safe local file access under allowed roots.
- System/window commands.
- Danmaku load/fetch commands when implemented.

Rust internals should use structured errors and return strings safe for frontend display. Keep libmpv integration in dedicated modules.

---

## Common Mistakes

- Putting data-source-specific logic directly in views/components instead of DataSource implementations.
- Treating ServerDataSource as mandatory.
- Writing secrets into `config.json` instead of secure storage.
- Replacing existing Player work during Trellis migration instead of adopting current state.
- Assuming mobile support is as mature as desktop without implementation proof.