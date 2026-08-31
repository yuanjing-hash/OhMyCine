# AGENTS.md

This repository contains OhMyCine Player only. Server/CLI work belongs in `yuanjing-hash/OhMyCine-Server`; official plugin, Plugin SDK, Registry, and Hub work belongs in `yuanjing-hash/OhMyCine-Plugins`.

## Product boundary

- Player must remain independently useful without Server.
- Use the common DataSource abstraction for local, Emby/Jellyfin, OpenList/Alist, CloudDrive2, WebDAV, Server, and future sources.
- Server integration is optional enhancement through versioned HTTP/WebSocket contracts, never a source-code dependency.
- Raw-source scraping, classification, metadata, artwork, logs, and overrides are local Player state and must not mutate the provider.
- AI recommendations are Player-side and must not send credentials or local absolute paths by default.

## Technology and layout

- Vue 3 Composition API with `<script setup>` and strict TypeScript.
- Pinia for shared state, composables for reusable lifecycle behavior, and typed services for domain logic.
- UnoCSS and design tokens for the Cinema OS UI.
- Tauri v2 + Rust + libmpv under `src-tauri/`; keep platform rendering and packaging explicit.
- Windows-native PowerShell development is authoritative. Docker and WSL are not local prerequisites.

## Security

- Credentials use the Player secure credential boundary, not localStorage or plain config.
- Normalize and constrain local/provider paths. Reject traversal and tokenized URLs before persistence or logging.
- Preserve the owner's real standard and portable profiles during tests.
- Server device tokens, provider tokens, API keys, signed URLs, passwords, and cookies must not appear in logs, exports, screenshots, or fixtures.

## Required checks

```powershell
npm install
npm run typecheck
npm run lint
npm run build
cargo check --manifest-path src-tauri/Cargo.toml
```

Use more specific verification scripts from `package.json` for the affected domain. Runtime/libmpv/windowing changes also require Windows-native launch and playback verification when the environment can provide it.

## Git and release

- Normal work targets `develop`; Stable releases come from `main` only after explicit promotion.
- Player tags are `vMAJOR.MINOR.PATCH` and must point to the latest remote `develop` commit for Beta.
- Do not push, tag, or publish unless the owner explicitly requests it.
- Commit messages use English Conventional Commit type/scope and Chinese descriptions.

## Trellis

Read `.trellis/workflow.md` and the relevant `.trellis/spec/frontend/` checklist before editing. Keep `.codex/` and `.agents/skills/` as the supported AI integration.
