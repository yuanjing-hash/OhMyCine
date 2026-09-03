# Frame Interpolation Third-Party Status

OhMyCine does not bundle or invoke Lossless Scaling, LSFG, `Lossless.dll`, or any extracted
commercial model or shader. Public descriptions of those products were used only as architecture
references for capture/output separation, adaptive quality, scene-cut handling and frame pacing.

The flow/mask model is RIFE v4.6 from `nihui/rife-ncnn-vulkan`, pinned to commit
`a7532fc3f9f8f008cd6eecd6f2ffe2a9698e0cf7`. The model files are distributed in that MIT-licensed
repository and are installed only after SHA-256 verification; their exact hashes and source URLs
are recorded in `runtime-manifest.json` and `model-manifest.json`. The upstream `LICENSE` is
installed beside the model files.

The Android inference runtime is Tencent ncnn `20260526`, distributed under BSD-3-Clause with
the additional third-party notices contained in its `LICENSE.txt`. The pinned archive and license
checksums are recorded in `runtime-manifest.json`; the build installs only its ARM64 Vulkan SDK.

The Windows inference runtime is Microsoft ONNX Runtime DirectML 1.24.4 (MIT) with Microsoft
DirectML 1.15.4. Both NuGet archives, their source versions and SHA-256 checksums are pinned in
`runtime-manifest.json`; only the Windows x64 runtime files and their notices are packaged.

The Windows flow/mask ONNX is reproducibly exported from the official Practical-RIFE v4.6
checkpoint published at commit `f6b5132517695127bdb5d5a8c3727e719f0fda22`. That release states
that its downloadable model content is MIT-licensed. The exporter exposes only `flow_pixels` and
`blend_mask`, and two clean exports must match the pinned output SHA-256.

OhMyCine extracts internal flow blob `327` and sigmoid mask blob `332`. It does not use RIFE's
final RGB `out0` for either SDR or HDR content. The model sees a bounded tone-compressed proxy;
the final warp and blend always operate on the original FP16 frames. SDR is linearized into that
common pipeline and encoded back to SDR only at presentation, so rejecting `out0` does not reject
or disable SDR interpolation.

The Android `rife.Warp` custom layer and its Vulkan bilinear-sampling kernels are derived from the
same pinned MIT-licensed `nihui/rife-ncnn-vulkan` commit. OhMyCine's device probe executes that
layer as part of a synthetic Vulkan inference and verifies finite four-channel flow plus a bounded
one-channel mask before the backend may pass its model gate.

HDR10+, Dolby Vision RPU reshape and Profile 7 enhancement pairing remain responsibilities of the
pinned mpv/FFmpeg/libplacebo runtime. Generated output may contain their already-applied pixel
mapping in HDR10/PQ/scRGB form; OhMyCine does not claim certified Dolby Vision metadata passthrough
and does not synthesize Dolby Vision RPU metadata for generated frames.
