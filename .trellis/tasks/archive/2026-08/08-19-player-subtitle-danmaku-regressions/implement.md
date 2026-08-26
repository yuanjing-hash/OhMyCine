# Implementation Plan

1. Add direct tests/helpers for stable track matching and danmaku visibility normalization.
2. Make pending track restoration lossless and external subtitle failures observable.
3. Extend transient playback/subtitle contracts and resolve Emby subtitles from the selected PlaybackInfo MediaSource.
4. Share bounded external subtitle preparation across desktop and Android, including validated transient headers.
5. Expand verification scripts and Rust unit tests for the new contracts.
6. Run focused verifies, typecheck, lint, build, Cargo tests/check/clippy, then inspect the final Player-only diff.

## Verification Result

- `npm run typecheck`, `npm run lint`, and `npm run build` passed.
- Danmaku, playback-preference, Emby HTTP, Android playback, secure routing, source lifecycle, and Emby progress verifies passed.
- `cargo test`, `cargo check`, and `cargo clippy --all-targets -- -D warnings` passed; 89 Rust unit tests passed.
- Windows-native `npm run tauri:build:android:preview` produced the ARM64 preview APK successfully.

Rollback points:

- Frontend preference changes are independent from the Emby/native subtitle transport changes.
- The transient request fields are optional, so non-Emby DataSources remain source-compatible.
- No schema or persisted-data migration is introduced.
