# Implementation Plan

## Gate 0 — Preserve Existing Work

- [x] Work only in files owned by this task; do not overwrite current media-detail changes.
- [x] Record the current dirty worktree and isolate overlapping edits before every implementation slice.
- [x] Keep frame interpolation disabled by default until all platform gates pass.

## Phase 1 — Reproducible Media Runtime and Capability Evidence

- [x] Define pinned Windows and Android libmpv/FFmpeg/libplacebo build manifests.
- [ ] Verify build features for HDR10, HLG, HDR10+ ST2094-40, DV P5/P7/P8, `dovi_split`, enhancement pairing and hardware decoders.
- [x] Add a non-product probe for Windows WGC FP16 capture of the mpv source HWND.
- [ ] Add a non-product Android probe for mpv output into RGBA16F AImageReader/AHardwareBuffer and Vulkan import.
- [ ] Validate representative GPUs/devices before exposing settings.
- [x] Audit RIFE code and selected weights, ONNX Runtime DirectML, ncnn and shader licenses.

Rollback point: probes and build manifests remain isolated; no UI or saved settings depend on them.

## Phase 2 — Shared Settings, State and Diagnostics

- [x] Extend `PlayerInteractionSettings` and native `MpvEngineSettings` with validated interpolation fields.
- [x] Add backward-compatible SQLite normalization/defaults.
- [x] Define shared capability, status, reason and metrics DTOs for desktop/mobile.
- [x] Replace desktop hardcoded hardware decoder diagnostics with real mpv properties.
- [x] Observe required mpv properties and media-generation events on both platforms.
- [x] Add deterministic TypeScript/Rust/Kotlin validation tests for settings and state transitions.

Rollback point: settings remain hidden and default off; backend is a no-op capability provider.

## Phase 3 — Windows Native Backend

- [ ] Add source/output HWND ownership without changing transparent WebView ordering.
- [ ] Implement WGC `R16G16B16A16_FLOAT` capture and adapter-LUID validation.
- [ ] Implement D3D11On12/shared-resource bridge, D3D12 queues, fences and swapchain pacing.
- [ ] Add FP16 proxy conversion and source-frame ring buffer.
- [ ] Integrate ONNX Runtime DirectML flow/mask model with GPU-bound I/O.
- [ ] Implement scene-cut/confidence gate, FP16 warp/composite and generated-frame presentation.
- [ ] Implement HDR-aware FSR after synthesis.
- [ ] Implement atomic bypass so source mpv HWND is visible before output resources are released.

Rollback point: hide/remove the output HWND and retain the existing mpv underlay path unchanged.

## Phase 4 — Android Native Backend

- [x] Add API/Vulkan/FP16/AHardwareBuffer/HDR dataspace capability probe.
- [ ] Introduce offscreen `AImageReader`/AHardwareBuffer source Surface while retaining direct Surface fallback.
- [ ] Import AHardwareBuffer into Vulkan without CPU readback.
- [ ] Integrate ncnn Vulkan flow/mask model with matching preprocessing and output semantics.
- [ ] Implement FP16 warp/composite, HDR-aware FSR and display-timed present to the real Surface.
- [ ] Handle Activity lifecycle, Surface replacement, rotation, refresh-rate changes, thermal state and memory pressure.
- [x] Verify Android ARM64 packaging and deterministic model extraction/checksum.

Rollback point: detach the offscreen Surface and reattach libmpv directly to the real SurfaceView.

## Phase 5 — Pacing, Audio and Overlays

- [ ] Implement rational/VFR target timestamp scheduling for 48/60/120/auto.
- [ ] Add audio-delay compensation and smooth enable/disable transitions.
- [ ] Flush frame/model queues on seek, track switch and media-generation changes.
- [ ] Move text subtitle rendering after frame generation with libass-equivalent styling.
- [ ] Keep danmaku and Vue controls above generated output.
- [ ] Detect graphic subtitle tracks and apply the documented initial bypass rule.

## Phase 6 — Product UI and Release Gating

- [x] Add compact desktop/mobile settings UI for mode, target and quality.
- [x] Display capability/effective status and concise reason; never present unsupported as active.
- [x] Add diagnostics for backend, hwdec, HDR input/output, Flow Scale, P50/P95 inference time and drops.
- [x] Keep all new copy localized consistently with existing Chinese UI.
- [x] Update Player architecture docs and third-party notices.
- [ ] Measure Windows/Android package-size growth and cold model-load time.

## Validation Matrix

- [ ] Inputs: H.264 SDR, HEVC Main10 HDR10, HLG, HDR10+, DV P5/P7/P8, AV1 10-bit.
- [ ] Rates: 23.976, 24, 25, 29.97, 30, 50, 60 and VFR.
- [ ] Operations: open, seek, pause/resume, speed change, track switch, resize, fullscreen, display move, foreground/background, rotation and Surface recreation.
- [ ] Overlays: SRT, ASS, PGS/VobSub, danmaku and controls.
- [ ] Failures: no hwdec, software mode, model missing/corrupt, GPU device lost, HDR surface unavailable, different adapter, thermal throttling and OOM.
- [ ] Visual HDR checks: no 8-bit conversion, highlight clipping, invalid primaries, NaN pixels, severe dark-scene flicker or silent SDR fallback.
- [ ] Timing checks: generated cadence, drop rate, A/V sync and recovery hysteresis.

## Required Commands

```powershell
npm install
npm run typecheck
npm run lint
npm run build
cargo check --manifest-path src-tauri/Cargo.toml
npm run verify:fsr-upscaling
```

Add and run dedicated frame-interpolation protocol, state-machine, asset-license, Windows native and Android ARM64 Release checks as their components land. Runtime completion additionally requires Windows-native playback plus physical Android-device coverage; emulator-only verification is insufficient for MediaCodec, Vulkan and HDR.

### 2026-09-03 Android build evidence

- Tauri `aarch64` debug build completed and produced `app-universal-debug.apk` with only `lib/arm64-v8a` native entries.
- Latest APK SHA-256 for this non-release validation build: `fc34658a4eaec58b5dbb97686bb8aef93a8a0e373e43677e9a0c4b4d9bd5da5d`.
- APK contains `libohmycine_framegen.so`, ncnn-linked RIFE assets, manifest and license notice.
- SDR uses its own verified linear-FP16 dataspace gate; lack of PQ/HLG output support does not disable SDR interpolation capability.
- JVM policy/controller tests and CMake `-Werror` ARM64 build pass inside the full Gradle/Tauri build.
- Application `minSdk=24` remains unchanged; native API 29 functionality uses weak imports plus an explicit runtime API gate, so older devices retain ordinary playback.

### 2026-09-03 product-gate audit

- Windows MSVC Release and NSIS packaging complete with the DirectML bridge and bundled runtime; the local validation installer is 70,588,323 bytes.
- Android ARM64 preview packaging completes with the ncnn/Vulkan bridge; the local validation APK is 122,979,213 bytes.
- Both native paths can import FP16 source frames, run flow/mask inference, composite original FP16 pixels and perform a hidden/output-surface Present without using RIFE `out0`.
- SDR capability is independent from HDR carrier support. HDR/Dolby Vision additionally require both a physical HDR display and verified HDR plus linear-HDR dataspaces.
- Windows Beta capability is now open only after the DirectML asset self-test, actual hardware decoding, reliable CFR timing, target-rate eligibility and a real hidden FP16 composite Present. It applies one-frame audio look-ahead compensation, reveals the output, and restores the user's original audio delay on every bypass path.
- Android Beta capability is open only after API 29, Vulkan 1.1, FP16 AHardwareBuffer/dataspace, ncnn model and inference self-tests pass. `active` is still withheld until `vkQueuePresentKHR` succeeds for the current generation; API 24-28 retain ordinary playback through runtime `libnativewindow` symbol resolution.
- The Beta is intentionally an in-field validation build. Windows VFR and any selected subtitle are explicitly bypassed; Android currently presents one generated midpoint per real frame pair, so full 48/60/120 pacing, audio compensation, thermal/OOM adaptation and physical HDR-device validation remain acceptance work after device results.
- Ordinary mpv hardware playback remains the atomic fallback on both platforms. Physical HDR/Dolby Vision color, cadence and A/V-sync observations must be captured during Beta testing before stable release.

### 2026-09-03 v1.1.38 field-failure correction

- A Windows field device returned `DXGI_ERROR_INVALID_CALL (0x887A0001)` from the product `CreateSwapChainForHwnd` path. The flip-model scRGB back buffers no longer request `DXGI_USAGE_UNORDERED_ACCESS`; synthesis now targets a dedicated `R16G16B16A16_FLOAT` UAV texture and performs a GPU copy into a render-target-only present buffer.
- An Android field device reached ncnn Vulkan but rejected the forced packed-FP16 custom Warp pipeline while loading the verified RIFE model. Pack4/pack8 shaders are now created only when their packing modes are active, and flow/mask inference retries with unpacked Vulkan FP32 tensors when packed FP16 is rejected. Decoded source frames and final composition remain typed RGBA16F, so this compatibility retry is not an SDR or 8-bit fallback.
- Contract tests now reject reintroducing a swapchain UAV usage flag and require both the dedicated Windows FP16 composite/copy path and the Android unpacked model fallback.
- Windows DirectML model execution (10 Rust tests), Windows scRGB capability probing, and Android ARM64 full-session native linking with `-Werror` pass after the correction. Physical-device confirmation remains the Beta acceptance gate.

### 2026-09-03 v1.1.39 cadence-freeze correction

- Field playback proved that one successful hidden generated `Present` is not a sufficient active gate: the DirectML worker could later fall behind while the visible output HWND retained its last texture, leaving audio running over a frozen frame.
- Windows now skips expired target ticks instead of completing historical inference work. Source-aligned ticks bypass the RIFE inference call entirely; only true intermediate timesteps execute the flow/mask model.
- The output is an owned overlay of the mpv source HWND and is continuously checked to remain immediately above the source and below the Tauri/WebView window across focus and Z-order changes.
- Reveal requires two consecutive source pairs with generated presents and no expired output. Two missed pairs, a source discontinuity while visible, unsafe Z-order, or a 350ms Present stall hides the output immediately and restores ordinary mpv playback.
- Native telemetry now reports successful/generated presents, expired ticks, inference samples, latest model time and measured output cadence to Rust. Rust feeds model time/drop counters into the existing diagnostics and removes its one-frame audio compensation whenever native output is bypassed.
- Queued WGC textures are indexed from their `SystemRelativeTime` mapped onto the mpv timing anchor, never from the later worker-consumption time. A separate 250ms inference watchdog requests ONNX Runtime termination so the Present watchdog and stop path are not trapped behind the same synchronous DirectML call.
- Audio-delay restoration retains its saved baseline until the mpv property write succeeds, allowing later bypass/stop paths to retry instead of permanently losing the user's value.
- Contract, Rust state-machine, Clippy, frontend production build and Android ARM64 native full-session verification cover the correction; physical observation remains Beta acceptance rather than a publication blocker.

## High-risk Files and Boundaries

- `src-tauri/src/mpv/player.rs`: mpv lifecycle, Windows source HWND and fallback ordering.
- `src-tauri/src/commands/player_shared.rs`: cross-platform validated settings contract.
- `src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvSurfaceHost.kt`: Surface/libmpv lifecycle.
- `src/services/playerInteractionSettings.ts`: persisted setting compatibility.
- `src/composables/useMpv.ts`: diagnostics and native event/state coordination.
- `src-tauri/resources/shaders/ohmycine-fsr-v1.glsl`: legacy FSR behavior; do not mutate HDR behavior without regression coverage.

## Review Gates

- [ ] Gate A: runtime HDR/DV and capture-surface probes prove the design on at least one Windows HDR system and one Android HDR device.
- [ ] Gate B: shared state and no-op backends pass without changing ordinary playback.
- [ ] Gate C: each platform backend survives forced failure and restores the original mpv surface without reload.
- [ ] Gate D: full codec/HDR/frame-rate/device matrix is green before settings become generally visible.
