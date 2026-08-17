# Player 作品级搜索、剧内检索与 FSR 超分 — Implementation Plan

## Order

1. Implement and verify work-level search child task.
2. Implement and verify series episode-title search child task.
3. Implement FSR settings, shader packaging, native runtime, diagnostics, and both platform UIs.
4. Run parent integration checks, update Player architecture/Trellis frontend contracts, and inspect the diff against the dirty worktree before any commit.

## Work-level search checklist

- Update Emby/Jellyfin search query to request only Movie and Series.
- Add and reuse a top-level search result normalizer in single-source and cross-source paths.
- Add verification for provider query, Episode/Season filtering, raw file preservation, type/ID separation, limits, and failed-source isolation.

## Episode search checklist

- Add detail-lifetime season episode cache and generation cancellation.
- Add cross-season title matching and stable selection/scroll behavior.
- Add desktop and Android-accessible UI states without autoplay.
- Verify current-season browsing, resume initial selection, and mobile horizontal/vertical layouts remain unchanged outside search.

## FSR checklist

- Add canonical FSR 1 GLSL, provenance, MIT license, and package/setup verification.
- Extend TypeScript, Rust, and Kotlin setting contracts with mode, sharpness, denoise, and target-cap validation.
- Materialize/copy only the application-owned shader path on Windows and Android.
- Implement runtime `glsl-shaders` clear/append and parameter updates.
- Add upscale-only condition, capability behavior for auto/force, sanitized status, shader-error fallback, and runtime disable.
- Add Windows controls in the playback settings panel and Android controls in the right-top `more` menu child panel, including the automatic/1080p/1440p/2160p target cap.
- Verify controls remain interactive while shaders load and do not affect subtitle, danmaku, gestures, hardware decoding, full screen, or Windows video HWND movement.

## Validation

Run Windows-native unless a command explicitly builds Android:

```powershell
cd player
npm run typecheck
npm run lint
npm run build
npm run verify:home-aggregate-search
npm run verify:mobile-ui
npm run verify:android-playback
npm run verify:player-controls-autohide
cd src-tauri
cargo fmt --check
cargo check --target x86_64-pc-windows-msvc
cargo clippy --target x86_64-pc-windows-msvc --all-targets -- -D warnings
cargo test --target x86_64-pc-windows-msvc
```

Add focused verification scripts for work search, episode search, and FSR packaging/contracts. Build Android from Windows with the existing preview command after the static checks. The owner performs final Android GPU and visual-quality validation on real media/device; Windows runtime validation uses an isolated profile and preserves existing user data.

## Risky files / guardrails

- `player/src/views/MediaDetailView.vue` has existing selection, progress, and virtualization behavior; reuse it rather than adding a second episode state machine.
- `player/src-tauri/src/mpv/player.rs` shares a mutex with control commands. Shader log handling must be bounded/non-blocking and must not synchronously poll large properties.
- `MpvSurfaceHost.kt` runs on Android's native control path; shader failure handling must not recurse or repeatedly reload the VO.
- `setup-libmpv-android.mjs` must sync the shader even when `runtimeReady()` is true.
- The repository has many unrelated Server/workflow changes. Stage only explicit Player/task/spec/doc paths and never revert or overwrite unrelated edits.
