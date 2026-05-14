# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project status

OhMyCine is currently in the requirements analysis and architecture design phase. The repository may contain product and design documentation before runnable component code exists. Treat README quick-start snippets and planned directories as target design unless the corresponding files actually exist.

When reviewing or editing documentation, do not interpret phased roadmap language as cutting scope. The intended product scope is complete; roadmap changes should adjust implementation order and dependencies, not remove planned capabilities.

## Product understanding

OhMyCine is an open-source, self-hosted, cross-platform home cinema ecosystem. It is not just a player and not just a media automation backend. The final system combines:

- immersive media playback
- media source browsing
- local and cloud-drive playback
- metadata scraping and poster-wall library views
- STRM generation
- 302 direct-link playback
- PT resource discovery
- automated downloading
- automated transfer and library organization
- Emby/Jellyfin refresh notifications
- TV follow/subscribe automation
- AI recommendations based on the user's own library
- plugin distribution
- CLI automation and diagnostics

Core media pipeline:

```text
Discover → Download → Transfer → Import → Notify
```

Typical full flow:

```text
PT/search result → downloader → category rule → storage destination → STRM/302 → Emby/Jellyfin refresh → Player display
```

Existing-media flow:

```text
Local / Emby / Jellyfin / OpenList / CloudDrive2 / 115 → Player browse → scrape metadata if needed → play → recommend
```

## Component responsibilities

### Player

Player is the primary user-facing application and must be independently useful without Server. It should be developed first as a complete client, with Server-related surfaces left as integration entry points when Server functionality is unavailable.

Player responsibilities:

- play local and remote video using Tauri + Vue + Rust + libmpv
- provide Cinema OS style UI: dark theme, liquid glass, poster wall, hero banner, continue watching, animations
- support local files, drag-and-drop playback, file association, playlists, playback history, subtitles, audio tracks, keyboard shortcuts
- implement a DataSource abstraction so all media sources expose common operations: list, search, getDetail, getStreamURL
- connect directly to Emby/Jellyfin via native APIs
- connect directly to OpenList/Alist and CloudDrive2/WebDAV
- support local-file media sources
- reserve future DataSources such as 115, 123, Quark, and ServerDataSource
- scrape metadata locally for file/cloud sources that lack media metadata: filename parse → TMDB lookup → local SQLite/cache → poster wall
- implement AI recommendation on the Player side using the user's API key and local media metadata/RAG; it should recommend only media the user already has
- connect to Server for enhanced features such as downloads, discovery, follow tasks, sync, and real-time events, but not depend on it for basic playback

Playback architecture preference:

- The product direction favors libmpv embedding through Rust FFI over an MPV sidecar because the goal is a deeply integrated, immersive player UI.
- MPV sidecar is easier and faster to validate but has weaker UI/video integration.
- libmpv embedding has a higher product ceiling but requires careful platform-specific rendering, packaging, and mobile strategy.

### Server

Server is the self-hosted automation engine. It is not merely an API backend for Player; it owns the media pipeline.

Server's core abstraction is the three-layer storage model:

```text
Connections → Storage Destinations → Category Rules
```

- Connections answer: what external service can OhMyCine connect to? Examples: Emby, Jellyfin, OpenList/Alist, CloudDrive2, 115, local filesystem, qBittorrent, Transmission, PT sites.
- Storage Destinations answer: where should files ultimately live? Examples: movie library on OpenList, TV library on 115, documentary folder on local NAS.
- Category Rules answer: what content type should go to which destination, with which naming template and transfer strategy?

Server responsibilities:

- manage media server, cloud drive, local file, downloader, and PT-site connections
- support the initial must-have storage set: 115, OpenList/Alist, CloudDrive2, and local files
- manage storage destinations and classification rules
- control qBittorrent/Transmission downloads
- implement STRM generation, incremental sync, full sync, invalid STRM cleanup, NFO/poster generation
- implement 302 proxy/direct-link playback for cloud media
- notify Emby/Jellyfin to refresh libraries
- expose REST API and WebSocket event streams
- support PT aggregation, one-click download, and metadata matching
- support follow/subscribe tasks for TV series: search missing episodes, apply quality/group filters, and submit downloads
- support multi-user permissions eventually; do not remove this scope from docs, but it can be phased after core flows
- support plugins eventually through Hub integration

Server MVP ordering should prioritize the user's immediate needs: 115, OpenList, CloudDrive2, local files, STRM, 302 proxy, and media-server refresh. PT aggregation, follow tasks, AI-related integrations, plugin system, multi-user permissions, and larger cloud-drive lists remain planned but can follow once the required storage/playback loop is stable.

### Hub

Hub is the plugin ecosystem distribution site, not a runtime backend. It is planned as a static site plus registry/release mechanism.

Hub responsibilities:

- browse and search plugins
- show plugin detail pages, versions, documentation, ratings/comments
- provide installation commands or links for Server/CLI/Player UI
- distribute plugin manifests and packages

Plugin categories include cloud drivers, PT scrapers, metadata providers, download clients, transfer strategies, notifications, player extensions, AI providers, and UI extensions.

Security note: existing Hub design may mention Go plugin loading, while security design prefers safer long-term isolation such as WASM or external processes. If editing plugin docs, keep Go plugin as a candidate rather than a settled security decision unless the project owner decides otherwise.

### CLI (`omc`)

`omc` is the automation and operations interface for advanced users and scripts.

CLI responsibilities:

- manage Server lifecycle, status, logs, updates, import/export
- manage configuration
- operate media libraries: list, search, scan, inspect, remove
- manage cloud drives and generate STRM
- search resources and submit downloads
- manage download tasks
- manage plugins
- run system diagnostics through `omc doctor`
- support table, JSON, and YAML output for both humans and scripts

## Roadmap principles

- Player independent-first: Player must work without Server and should be developed as a full client first.
- Preserve final feature scope: do not recommend deleting PT search, follow/subscribe, AI, plugins, multi-user permissions, or large driver expansion from the product design.
- Adjust order, not scope: move complex capabilities later if needed, but keep them documented as planned capabilities.
- Server early work should focus on the storage/playback loop required by the owner: 115, OpenList, CloudDrive2, local files, STRM, 302 proxy, and Emby/Jellyfin refresh.
- Server-related Player pages should be implemented with clear disabled/placeholder states when Server is not connected.

## Security architecture

The project handles many sensitive assets: media-server API keys, 115 cookies, OpenList tokens, CloudDrive2/WebDAV credentials, PT cookies/passkeys, downloader passwords, AI API keys, JWT/session tokens, and proxy URLs.

Important security expectations:

- Server API should require authentication by default except minimal login/health endpoints.
- `/proxy/*` must not be publicly open by default. Prefer signed URLs for STRM use; authenticated and trusted-LAN modes are possible deployment choices.
- Server-side sensitive configuration should be encrypted at rest, preferably AES-GCM with a deployment-provided or locally generated master key.
- Player credentials should use OS secure storage where available: Windows Credential Manager/DPAPI, macOS Keychain, Linux Secret Service/libsecret, Android Keystore, iOS Keychain.
- Player ↔ Server sync should default to structural sync only. API keys, cookies, passwords, AI keys, and PT passkeys require explicit user confirmation for full sync.
- Logs must redact Authorization, cookies, API keys, passkeys, JWTs, downloader passwords, CDN token URLs, and AI keys.
- Local file operations must constrain paths to configured roots and defend against traversal and symlink escape.
- STRM cleanup should operate only inside configured STRM roots and should support dry-run behavior when implemented.
- Plugin installation/update should not be automatic by default. Third-party plugins should declare permissions and should not receive global credential access by default.
- AI recommendations should not send local absolute paths or credentials to LLM providers by default.

Consult `docs/architecture/07-security-design.md` before implementing credential storage, 302 proxy, config sync, plugin loading, file operations, or AI integrations.

## Local development environment

The project owner's local development environment is WSL/Linux-first, not Windows PowerShell/CMD-first. Prefer bash/Linux commands and assume Node, Rust, Go, Tauri CLI, linters, and test tools are installed inside WSL unless explicitly told otherwise.

Docker is not available locally and must not be treated as a development prerequisite. Docker-related files and commands are for later deployment, CI integration testing, NAS/server environments, or release workflows.

When working on Windows desktop packaging, WebView2, MSVC, Windows-specific libmpv binaries, or native Tauri desktop runtime issues, explicitly distinguish between:

- WSL development/build commands
- Windows-native desktop runtime or packaging requirements

Do not silently assume a WSL-built artifact is equivalent to a Windows-native Tauri desktop build.

## Development commands

The repository currently may not have runnable component directories yet. Use the commands below only once the relevant component files exist. Prefer running them inside WSL unless the task specifically requires Windows-native desktop packaging or runtime testing.

Player target commands:

```bash
cd player
npm install
npm run tauri dev
npm run build
npm run typecheck
npm run lint
```

Server target commands:

```bash
cd server
go mod download
go run ./cmd/server
go test ./...
golangci-lint run
```

Run a single Go test when Server exists:

```bash
cd server
go test ./internal/services -run TestName
```

CLI target commands:

```bash
cd cli
go build -o omc ./cmd/omc
./omc --help
go test ./...
```

Hub target commands:

```bash
cd hub
npm install
npm run dev
npm run build
```

Docker target command when Server Docker files exist:

```bash
cd server
docker compose up -d
```

## Planned repository structure

The intended monorepo layout is:

```text
player/   Tauri v2 + Vue 3 + TypeScript + libmpv player
server/   Go + Gin + GORM backend automation engine
hub/      VitePress plugin marketplace
cli/      Go + Cobra command-line tool
docs/     architecture and design documents
.github/  CI/CD workflows
```

Do not assume a planned directory exists until it is present in the working tree.

## Key implementation conventions from project docs

### Go Server / CLI

- Use Go 1.22+.
- Keep HTTP handlers thin; business logic belongs in services.
- Use `internal/` for private application code and `pkg/` for reusable drivers/clients.
- Use `context.Context` in external calls and long-running services.
- Use GORM for persistence and transactions for related writes.
- API routes use `/api/v1/`.
- Standard response envelope is:

```json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

- Planned packages include cloud drivers, media-server clients, download clients, PT scrapers, metadata, 302 proxy, and STRM generation.

### Vue / TypeScript Player

- Use Vue 3 Composition API and `<script setup>`.
- Use Pinia stores for shared state.
- Use composables such as `useMpv`, `useServer`, `useTheme`, and `useKeyboard` for reusable behavior.
- Keep DataSource implementations behind the common DataSource interface.
- Use UnoCSS and CSS variables for the Cinema OS design system.
- Player services include datasource, scraper, AI, and sync.

### Rust / Tauri Player backend

- Tauri commands expose player, file, system, and window operations to the Vue frontend.
- libmpv integration belongs in dedicated Rust modules.
- Use structured error types internally and return command errors as strings to the frontend.
- Keep platform rendering and libmpv packaging concerns explicit; do not assume desktop and mobile behavior are identical.

### API and documentation

- API changes should update OpenAPI once `api/openapi.yaml` exists.
- Architecture-changing work should update `docs/architecture/`.
- Documentation is intentionally important in this repository because the project is currently design-first.

## Documentation consistency notes

When editing docs, keep these points consistent:

- Use OpenList/Alist consistently. Prefer wording like `OpenList/Alist` or `OpenList (Alist-compatible API)` if both are relevant.
- Keep Player independent from Server in both architecture and wording.
- Keep Server as an enhancement/automation layer, not a hard dependency for Player.
- Avoid presenting future mobile support as equally mature as desktop unless the implementation proves it. Desktop is the practical first target; Android/iOS are longer-term targets.
- If README contains product quick-start examples before code exists, clarify that they describe the intended target structure/usage.
- Keep AI primarily Player-side unless explicitly designing a Server-side AI feature.

## Commit and PR style

Project docs specify Conventional Commits:

```text
<type>(<scope>): <description>
```

Common scopes: `player`, `server`, `hub`, `cli`, `docs`, `api`, `db`.

Common types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`.

Commit message language rule: keep the Conventional Commits `type` and optional `scope` in English, but write the short description and body in Chinese. Standard footer/trailer fields such as `Closes #123` and `Co-Authored-By: Codex Opus 4.7 <noreply@anthropic.com>` may remain in English.

Example:

```text
docs: 更新 Codex 项目指导文档

补充 WSL 本地开发环境、项目级 skills 和提交信息语言规范，确保后续 Codex 会话保持一致。
```
