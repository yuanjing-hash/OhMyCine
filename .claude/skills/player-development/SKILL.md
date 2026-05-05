---
name: player-development
description: Use this skill when developing, designing, reviewing, or debugging OhMyCine Player, including Tauri/Vue/libmpv, DataSource integrations, local scraping, AI recommendation, playback UI, keyboard shortcuts, or Player-Server integration surfaces.
---

# Player Development Skill

## When to use

Use this skill for tasks involving OhMyCine Player:

- implementing or reviewing Tauri/Vue/Rust player code
- adding or changing DataSource implementations
- working on local files, Emby, Jellyfin, OpenList/Alist, CloudDrive2, 115 placeholders, or ServerDataSource
- integrating libmpv playback, subtitles, audio tracks, keyboard shortcuts, drag-and-drop, playlists, or playback history
- designing Cinema OS UI, poster walls, media detail pages, home layout, or player controls
- implementing local metadata scraping, TMDB integration, poster cache, local SQLite media index
- implementing AI recommendation in Player
- adding Server-connected pages or disabled/placeholder states

Do not use this skill for Server-only media pipeline work unless Player integration is also affected.

## Project context

Player is the primary user-facing application and must be independently useful without Server. Server enhances Player but must not be required for basic browsing, scraping, recommendation, or playback.

Player target stack:

- Tauri v2
- Vue 3 + TypeScript
- Pinia
- Vue Router
- Vue I18n
- UnoCSS + CSS variables
- Rust Tauri backend
- libmpv through Rust FFI

## Architecture principles

### Player independent-first

Player must work without OhMyCine Server. When adding features, classify them as:

1. independent Player feature
2. optional Server integration
3. Server-only feature that should appear as an unavailable/placeholder entry in Player

If Server is not connected, Server-related pages must have clear disabled or empty states rather than breaking the app.

### DataSource abstraction

All media sources should be accessed through the DataSource interface concept:

```text
list / search / getDetail / getStreamURL
```

Expected DataSource types:

- local file
- Emby
- Jellyfin
- OpenList/Alist
- CloudDrive2/WebDAV
- ServerDataSource
- future 115 / 123 / Quark and other cloud sources

Do not couple UI directly to one provider's API if the behavior belongs in the common media browsing/playback flow.

### libmpv direction

The product direction favors embedded libmpv over MPV sidecar.

Use sidecar only as an explicit spike/prototype if requested. The intended product path is:

```text
Vue UI → Tauri command/event bridge → Rust libmpv wrapper → native rendering
```

Keep platform rendering concerns explicit. Do not assume Windows, macOS, Linux, Android, and iOS have the same rendering/windowing behavior.

## Common workflow

1. Identify whether the feature is independent Player functionality or Server-connected UI.
2. Check `docs/architecture/03-player-design.md` for intended module boundaries.
3. If playback or media source security is involved, check `docs/architecture/07-security-design.md`.
4. Preserve the DataSource boundary.
5. Keep Player usable without Server.
6. For UI work, maintain Cinema OS direction: dark, immersive, liquid-glass, poster-centric, smooth but not over-abstracted.
7. For playback work, validate subtitle/audio/progress/seek/control behavior, not only compilation.
8. Update architecture docs when changing DataSource shape, playback architecture, AI flow, or Server integration contracts.

## Module responsibilities

### Vue frontend

- UI layout and views
- player controls and state presentation
- media grids, poster wall, details
- settings and datasource management
- AI recommendation UI
- Server-connected surfaces

### Tauri Rust backend

- libmpv wrapper and lifecycle
- Tauri commands/events for playback
- local file/system operations
- platform window/rendering bridge
- secure credential access through OS storage where appropriate

### Player services

- datasource clients and adapters
- metadata scraping
- local database/cache
- AI indexing and recommendation
- config sync client

## AI recommendation rules

AI recommendation is primarily Player-side.

- Use the user's own API key.
- Recommend only media already in the user's library.
- Default outbound AI payloads should contain media metadata, not local absolute paths or credentials.
- AI must not directly perform destructive actions such as delete files, change config, or start downloads without explicit user-driven flows.

## Security reminders

- Player credentials should use OS secure storage when available.
- Plain config files should store non-sensitive settings and credential references, not raw secrets.
- Config export should default to redacted credentials.
- External URL requests should use timeout/error handling.
- Treat custom AI provider base URLs as user-configured external services and warn about data exposure when appropriate.

## Validation commands

Use only after the Player project exists:

```bash
cd player
npm install
npm run tauri dev
npm run build
npm run typecheck
npm run lint
```

If Tauri/Rust code exists:

```bash
cd player/src-tauri
cargo check
cargo clippy
cargo test
```

## Documentation to consult

- `docs/architecture/03-player-design.md`
- `docs/architecture/01-overview.md`
- `docs/architecture/06-roadmap.md`
- `docs/architecture/07-security-design.md`
- `DEVELOPMENT.md`
- `CLAUDE.md`
