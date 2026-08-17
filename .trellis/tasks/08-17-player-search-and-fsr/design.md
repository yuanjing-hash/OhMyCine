# Player 作品级搜索、剧内检索与 FSR 超分 — Design

## Architecture

This parent task coordinates three independently testable Player changes:

1. DataSource search returns work-level entities.
2. Series detail owns episode-title search and navigation.
3. Playback-engine settings control a native libmpv FSR 1 shader on Windows and Android.

No feature depends on OhMyCine Server. The parent has no standalone product-code patch; child tasks own implementation, while the parent owns integration checks and documentation consistency.

## Work-level search boundary

- Change Emby/Jellyfin's provider query from `Movie,Series,Episode` to `Movie,Series` so the provider returns the correct entity and artwork instead of asking the UI to guess parent series.
- Add a small shared work-search normalizer at the DataSource aggregation boundary. It removes `season` and `episode` entities from top-level search but preserves `movie`, `series`, and raw `file`/`folder` results for sources that do not expose work entities.
- Reuse the normalizer in single-source library search and cross-source aggregation. Keep source identity, per-source limits, failure isolation, and `sourceId:id` deduplication unchanged.
- Do not merge same-name movies and series: IDs and types remain distinct work entities.

## Series episode search

- `MediaDetailView.vue` owns a detail-lifetime `Map<seasonId, MediaItem[]>` cache. Existing season children seed the cache; missing seasons load through the same `DataSource.list(season.id)` boundary used by normal browsing.
- Opening/searching starts a generation-scoped load across all known seasons. Results are flattened to `{ season, episode, seasonIndex, episodeIndex }`, then matched by trimmed, case-insensitive title containment.
- The search overlay/sheet shows `SxxExx` (or readable fallbacks) and title, with explicit loading, partial-failure, empty, and retry states.
- Selecting a result closes search, selects the season, restores that season from cache, selects the matching episode by stable ID, updates the desktop episode window, and scrolls after `nextTick`. It never calls playback.
- Route changes invalidate generations and cache so late provider responses cannot mutate the next detail page.

## FSR settings contract

Extend `PlayerInteractionSettings` and the native `MpvEngineSettings` payload with:

```text
fsrMode: off | auto | force       default auto
fsrSharpness: 0..100              default 35, higher means visually sharper
fsrDenoise: boolean               default true
fsrTarget: auto | 1080p | 1440p | 2160p
```

The frontend normalizes persistence. Rust and Kotlin validate again at the trust boundary. Runtime status is separate from requested settings:

```text
off | inactive-no-upscale | active | fallback
reason?: bounded, sanitized text
```

`force` bypasses only conservative capability detection. The shader remains conditional on actual upscaling, and all errors still fall back.

Target resolution is an upper bound for the FSR intermediate output, not a forced display mode. Numeric presets cap the output picture's shorter edge at 1080, 1440, or 2160 pixels while preserving aspect ratio; `auto` follows libmpv's actual output size. MPV still owns the final framebuffer and window/full-screen/rotation changes.

## FSR shader and packaging

- Use a self-contained mpv GLSL implementation derived from AMD's official MIT FSR 1 EASU + RCAS reference. Keep an in-file provenance header and repository third-party license copy.
- Use portable mpv user-shader `PARAM`/`WHEN` features common to `vo=gpu-next` and `vo=gpu`; do not use FSR 2/3 temporal inputs.
- Windows embeds the reviewed source at compile time, hashes/materializes it into the active profile cache, and passes only that managed path to libmpv.
- Android build setup copies the same canonical file into APK assets even when the mpv runtime is already installed. Runtime copies it to the private `filesDir/mpv` directory before applying it.
- Enable/disable via libmpv `change-list glsl-shaders clr/append`; update sharpness/denoise through shader options. The application never accepts arbitrary shader paths.

## Fallback and diagnostics

- Settings application is split into essential playback options and optional FSR application. An FSR failure cannot fail `initializeRender`, `load`, pause/seek, subtitles, or danmaku.
- Android uses its existing bounded log observer. Windows extends event draining to capture shader-related log failures after requesting libmpv logs.
- On shader load/compile error, clear the shader list, mark `fallback`, keep normal mpv scaling, and expose a sanitized reason in playback diagnostics. Do not show a blocking dialog on Android.
- Switching to `off` clears the shader immediately. Switching mode/parameters during playback does not reload the media.

## UI placement

- Windows: extend `PlayerSettingsPanel.vue` with an “FSR 超分” section containing mode choices, target cap, sharpness slider, denoise toggle, and compact runtime status.
- Android: add `fsr` to `MobilePlayerControls.vue` panels and an “FSR 超分” row inside the right-top `more` sheet. The FSR child sheet contains the same mode, target cap, tuning, and status controls.
- Controls emit settings changes to `PlayerView.vue`, which persists the global setting and invokes the native engine update. Errors are shown inline and do not lock chrome visibility.

## Compatibility and rollback

- Emby and Jellyfin share the provider adapter and must pass the same work-level search checks.
- Raw DataSources retain file/folder fallback search results; recognized Series are not expanded.
- Both `gpu-next` and `gpu` are supported targets; unsupported Android GPU/driver combinations fall back.
- Rollback is independently possible: restore Episode to provider query only if product behavior is intentionally reverted; disable the detail search UI without changing DataSource; default FSR to `off` or clear managed shaders without affecting playback.
