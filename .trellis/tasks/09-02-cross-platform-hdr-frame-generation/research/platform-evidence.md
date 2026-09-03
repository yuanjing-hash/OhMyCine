# Platform Evidence

## Local Player

- `src-tauri/src/mpv/player.rs:286-309` initializes the Windows renderer through an owned HWND, `wid`, `vo=gpu-next|gpu`, and the configured `hwdec` before `mpv_initialize`.
- `src-tauri/src/commands/player_shared.rs:34-109` owns the validated shared engine settings. Defaults are `gpu-next` and desktop `auto-safe`.
- Desktop diagnostics now read `hwdec-current`, `current-vo`, `vo-configured`, codecs and playback events from the live libmpv handle; no synthetic hardware-decoder result remains.
- `src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvSurfaceHost.kt:188-223` maps shared engine settings to Android `mediacodec` candidates.
- The Android host observes `hwdec-current` and reports it in playback diagnostics; its Surface lifecycle is handled in `surfaceCreated/surfaceChanged/surfaceDestroyed`.
- `src-tauri/gen/android/app/build.gradle.kts:28-34` sets compile/target SDK 36 and minSdk 24.
- `src-tauri/resources/shaders/ohmycine-fsr-v1.glsl:73` and the second pass currently define `FSR_PQ 0`; existing FSR is not the HDR frame-generation output stage.
- `src/composables/useMpv.ts:593-636` identifies SDR, HDR10/PQ, HLG and Dolby Vision for presentation, but does not configure a native HDR interpolation pipeline.

## Bundled Desktop Runtime

The inspected Windows runtime reports mpv `v0.41.0-1012-ge8673660a`, FFmpeg `N-126335-gb32f8d1c2`, and libplacebo `v7.371.0`. It exposes hardware-copy decoder variants and FFmpeg `minterpolate`; the latter is rejected as the formal HDR backend because its supported pixel formats do not cover the required P010/10-bit/FP16 path.

## mpv HDR and Dolby Vision

- mpv `--target-colorspace-hint=auto|yes` sets output colorspace metadata on supported D3D11/winvk contexts with `vo=gpu-next`.
- mpv `--target-colorspace-hint-mode=source-dynamic` explicitly states it does not send full HDR10+ or Dolby Vision metadata; it uses that information to produce HDR10 with per-scene luminance values.
- Current mpv source contains `demux/dovi_split.c` for Profile 7 enhancement-layer splitting and warns when the FFmpeg `dovi_split` bitstream filter is unavailable, in which case it renders the base layer only.
- Current mpv source includes enhancement pairing and passes Dolby Vision mapping data into the gpu-next/libplacebo render path.

References:

- https://github.com/mpv-player/mpv/blob/master/DOCS/man/options.rst
- https://github.com/mpv-player/mpv/blob/master/demux/dovi_split.c
- https://github.com/mpv-player/mpv/blob/master/filters/f_enhancement_pair.c

## Frame-generation References

- Lossless Scaling publicly describes LSFG as a proprietary model with fixed/adaptive frame generation and a performance model. Its commercial model/DLL is not reusable.
- lsfg-vk documents swapchain interception, double buffering, shared semaphores, frame pacing, Flow Scale and same-GPU constraints. It still requires the user's Lossless Scaling DLL and is architecture reference only.
- RIFE v4.6 `flownet.param` and `flownet.bin` are committed in the MIT-licensed
  `nihui/rife-ncnn-vulkan` repository at `a7532fc3f9f8f008cd6eecd6f2ffe2a9698e0cf7`.
  The reproducible setup verifies SHA-256 `724569...f1053c` and `f334ed...08958` respectively.
- Network inspection proves blob `327` is cumulative full-resolution pixel flow in channel order
  frame0 dx/dy + frame1 dx/dy, and blob `332` is the sigmoid frame0 blend weight. The upstream
  `out0` is deliberately prohibited for both SDR and HDR output. It is a proxy-domain composite,
  not an output-format gate: SDR is linearized into FP16, composed from the original source frames,
  and encoded back to SDR at presentation.
- Android ncnn is pinned to release `20260526`, archive SHA-256 `26909c...2804`, under its
  BSD-3-Clause plus bundled third-party notices.
- Windows Graphics Capture documentation recommends `R16G16B16A16_FLOAT` for HDR capture to avoid overexposed/clipped results.

References:

- https://store.steampowered.com/app/993090/Lossless_Scaling/
- https://github.com/PancakeTAS/lsfg-vk/blob/develop/docs/Journey.md
- https://github.com/PancakeTAS/lsfg-vk/blob/develop/docs/Configuration.md
- https://github.com/hzwer/ECCV2022-RIFE
- https://github.com/nihui/rife-ncnn-vulkan
- https://learn.microsoft.com/windows/uwp/audio-video-camera/screen-capture

## Decisions Derived from Evidence

- Preserve libmpv as the playback/HDR metadata owner instead of rewriting demux, network, audio, seek and clocks.
- Keep the original mpv HWND/Surface alive as the immediate fallback.
- Use a post-mpv high-precision surface so HDR10+/Dolby Vision mapping is applied before interpolation.
- Separate motion estimation from final HDR pixel synthesis.
- Maintain one product protocol and model semantics with platform-native inference runtimes.

## Local Capability Probe — 2026-09-02

The committed Windows prerequisite probe created a hardware D3D11 device and a real
`R16G16B16A16_FLOAT` shader-resource/render-target texture, queried Windows Graphics Capture, and
loaded the DirectML runtime. On the current Windows 11 build 26200 / NVIDIA Quadro RTX 6000 host it
reported:

```json
{"d3d11Fp16Texture":true,"directMlRuntime":true,"featureLevel":"0xb000","format":"R16G16B16A16_FLOAT","supported":true,"windowsGraphicsCapture":true}
```

The probe now also accepts `--hwnd=<value>` / `OHMYCINE_MPV_HWND`, or discovers the live
`OhMyCineMpvSurface` window by class name. In that mode it starts a free-threaded WGC frame pool in
`R16G16B16A16_FLOAT`, retrieves the captured D3D11 texture and compares source/capture adapter
LUIDs. `--require-hwnd` turns this into a strict gate. The current evidence run did not have an
active Player HWND, so live capture, inference, HDR presentation and rollback remain unproven.

The Android capability path now includes an ARM64 native probe. It creates a real RGBA16F
`AImageReader`, allocates RGBA16F AHardwareBuffer storage, creates a Vulkan 1.1 device with FP16
shader/storage features, and calls `vkGetAndroidHardwareBufferPropertiesANDROID` to prove external
memory import. A pinned ncnn Vulkan runtime then loads both verified RIFE model files with a custom
layer registration. The ARM64 C++ library links successfully with NDK 27.2 on this host. Physical
mpv attachment, HDR dataspace negotiation, device execution and the inference/present loop remain
release gates, so capability intentionally stays unsupported.
