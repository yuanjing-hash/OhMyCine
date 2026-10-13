# Android mpv runtime

Player requires this complete runtime with the managed Android HDR EGL patch.
The primary route is gpu-next/GLES with MediaCodec hardware frames imported by
AImageReader and external OES. Unknown, SDR and HDR inputs use hardware decoding;
only the explicit software playback setting selects a software decoder. Native
failures do not select software, an SDR carrier or another renderer.

`sources.json` fixes the official 2026-04-25 mpv-android/JNI/FFmpeg dependencies,
shaderc and its DEPS, Android NDK r29, ARM64, and API 24. The builder retains
GPU subtitle and screenshot support. libplacebo links Android's Vulkan loader
through `vkGetInstanceProcAddr`; an `androidvk` context name alone does not prove
that its instance can initialize. Cached Meson dependencies reconfigure in place
when builder options change.

`android-hdr-egl.c` keeps the EGL context and selects an actual ten-bit or FP16
window config. Its HDR callback creates a PQ/HLG Surface and reads back the EGL
colorspace and config component depth before reporting the target. HDR also
requires GLES 3 and renderable high precision textures. libplacebo samples
external OES with highp precision and refuses eight-bit HDR intermediate FBOs.
SDR displays use an intentional SDR output; the saved legacy VO preference only
applies after confirming SDR input.

This fixed FFmpeg revision's MediaCodec copy output only supports eight-bit
NV12/YUV420P, so that copy route is not selected. The external hardware buffer
is imported without a CPU copy or eight-bit texture upload; the actual private
buffer precision and device display behavior remain physical-device checks.
Known Dolby Vision configuration is rejected by this hardware RGB route because
it drops RPU metadata. Explicit software retains the existing RPU processing;
native Dolby hardware output is not claimed by this patch.

`hwdec-software-fallback=no` applies to hardware requests, including decoder
initialization. The native strict hardware exhaustion branch reports an error
instead of a normal EOF, preserving the watched progress after a failure.

Build in an isolated Linux Docker workspace:

```sh
docker build -t ohmycine-android-mpv-builder scripts/android-mpv-runtime
mkdir -p .build/android-mpv-runtime
docker run --rm --cpus=4 --memory=5g \
  -v "$PWD/scripts/android-mpv-runtime:/builder:ro" \
  -v "$PWD/.build/android-mpv-runtime:/work" \
  ohmycine-android-mpv-builder --work-dir /work --jobs 4
node scripts/setup-libmpv-android.mjs
```

The builder never installs tools on the host. Output includes the complete JNI
library set, ELF dependency inspection, CA asset, dependency licenses and a hash
manifest. The importer checks source/build-input provenance, every file hash,
required native symbols, the platform Vulkan loader dependency, compiled HDR EGL,
precision and strict decoder patch markers before copying it to managed Android
build inputs. An interrupted import is retried on the next
setup call. Generated libraries and cache remain ignored by Git.

To import a separately built output directory, set
`OHMYCINE_ANDROID_MPV_RUNTIME` or pass `--runtime-dir <directory>`.
`--verify-only` verifies the installed inputs without changing them.

The existing Android release job caches immutable output using the builder input
hash. Normal builds reuse it; dependency rebuilds occur only after input changes
or cache eviction. No extra cloud test job is added.

This build and the native target metadata do not verify physical HDR display
activation. Device validation still covers hardware PQ/HLG input, the known Dolby
hardware boundary, explicit software Dolby input, display activation,
subtitles, paused/current-frame screenshots, seeking, rotation and surface rebuild.
