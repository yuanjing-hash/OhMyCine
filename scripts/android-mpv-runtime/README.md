# Android mpv runtime

Player requires this Vulkan-capable runtime for the HDR carrier. The former
official APK has Vulkan disabled and cannot be substituted for it. GLES remains
available in the same native set for SDR and renderer fallback.

`sources.json` fixes the official 2026-04-25 mpv-android/JNI/FFmpeg dependencies,
shaderc and its DEPS, Android NDK r29, ARM64, and API 24. The builder retains
GPU subtitle and screenshot support. Vulkan uses MediaCodec copy or software
decode because this mpv revision's zero-copy MediaCodec interop supports GL only.

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
required native symbols and the compiled `androidvk` context before copying it
to managed Android build inputs. An interrupted import is retried on the next
setup call. Generated libraries and cache remain ignored by Git.

To import a separately built output directory, set
`OHMYCINE_ANDROID_MPV_RUNTIME` or pass `--runtime-dir <directory>`.
`--verify-only` verifies the installed inputs without changing them.

The existing Android release job caches immutable output using the builder input
hash. Normal builds reuse it; dependency rebuilds occur only after input changes
or cache eviction. No extra cloud test job is added.

This build and the native target metadata do not verify physical HDR display
activation. Device validation still covers PQ/HLG/Dolby input, display activation,
subtitles, paused/current-frame screenshots, seeking, rotation and surface rebuild.
