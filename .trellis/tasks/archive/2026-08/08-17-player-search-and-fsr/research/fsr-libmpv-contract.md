# FSR / libmpv integration research

## Repository evidence

- `player/src/services/playerInteractionSettings.ts` already owns persistent cross-platform playback-engine settings and normalizes every field before storage.
- `player/src/composables/useMpv.ts:178` sends those settings through `mpv_apply_engine_settings` before render initialization and before each load.
- Windows applies the validated contract in `player/src-tauri/src/mpv/player.rs:144`; Android mirrors it in `MpvPlugin.kt` and `MpvSurfaceHost.kt:158`.
- Windows uses `vo=gpu-next` or `vo=gpu` with an owned video HWND. Android uses the same VO choices with `gpu-context=android` and OpenGL ES, so both paths support libmpv user shaders without routing video through WebView.
- `PlayerSettingsPanel.vue` is the existing desktop playback-settings surface. `MobilePlayerControls.vue` already has the right-top `more` sheet and can host an FSR child panel.

## Upstream contracts

- AMD's official `GPUOpen-Effects/FidelityFX-FSR` repository identifies its license as MIT and publishes `license.txt` plus the FSR 1 reference header. FSR 1 provides EASU spatial upscaling and RCAS sharpening and does not require motion vectors.
- mpv documents `glsl-shaders` as the runtime list of custom GLSL hooks. `change-list glsl-shaders append <file>` adds one file; the general list contract provides `set` and `clr`, so OhMyCine can atomically replace its managed shader and clear it on disable/failure without parsing platform path separators.
- mpv user shaders support conditional `WHEN` expressions using input/output texture sizes. The FSR shader can therefore remain loaded while skipping all passes unless output width or height exceeds the hooked source size.
- mpv documents tunable `PARAM` blocks and `glsl-shader-opts`. `vo=gpu` supports a smaller subset than `vo=gpu-next`, so the shipped shader must use only portable float/int parameter features and avoid `gpu-next`-only dynamic/constant parameter types.
- mpv explicitly calls user-shader syntax unstable. The implementation must verify against the pinned libmpv builds used by Windows and Android and keep a native clear-and-fallback path.

## Chosen runtime contract

1. Ship one reviewed, self-contained mpv GLSL file derived from the official MIT FSR 1 EASU/RCAS algorithm, with provenance and license notices in the file and repository third-party notices.
2. The shader owns the `OUTPUT > source` condition. Native code also reports whether dimensions indicate an upscale; this is diagnostic state, not a second image-processing implementation.
3. Runtime switching uses `change-list glsl-shaders clr ""` followed by `append <managed-path>` when enabling. This avoids Windows `;` versus Unix/Android `:` path-list escaping.
4. Runtime parameters use normalized OhMyCine values: intuitive RCAS sharpness `0..100`, an RCAS denoise boolean, and an `auto/1080p/1440p/2160p` target cap. Numeric caps constrain the FSR intermediate output's shorter edge while preserving aspect ratio; native code clamps/maps values and invalid values restore defaults.
5. `auto` requires known compatible VO/context and a successful shader load. `force` bypasses conservative capability prechecks but still preserves the shader's upscale-only condition and the compile/runtime fallback. Neither mode can make playback failure fatal.
6. Windows materializes the compile-time reviewed shader into the current storage profile's managed cache before passing its path to libmpv. Android copies the same reviewed shader from APK assets into `filesDir/mpv` before loading it. Neither accepts arbitrary user shader paths.
7. Windows requests sanitized libmpv log messages and Android reuses its existing log observer. A shader compile/load error clears the managed shader, records a bounded reason, and leaves normal mpv scaling active.

## Sources checked on 2026-08-17

- https://github.com/GPUOpen-Effects/FidelityFX-FSR
- https://raw.githubusercontent.com/GPUOpen-Effects/FidelityFX-FSR/master/license.txt
- https://raw.githubusercontent.com/GPUOpen-Effects/FidelityFX-FSR/master/ffx-fsr/ffx_fsr1.h
- https://github.com/mpv-player/mpv/blob/master/DOCS/man/options.rst (`glsl-shaders`, user shader `PARAM` / `WHEN`, `glsl-shader-opts`)
- https://github.com/mpv-player/mpv/blob/master/DOCS/man/input.rst (`change-list`)
