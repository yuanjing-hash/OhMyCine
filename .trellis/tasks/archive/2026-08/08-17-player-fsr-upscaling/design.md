# 接入 libmpv FSR 超分 — Design

The FSR child follows the parent design in `../08-17-player-search-and-fsr/design.md` and the research contract in `../08-17-player-search-and-fsr/research/fsr-libmpv-contract.md`.

Its implementation boundary is the persistent Player engine-setting contract, application-owned FSR 1 shader/provenance, Windows Rust/libmpv runtime, Android Kotlin/libmpv runtime, diagnostics, and Windows/Android playback controls. It does not change WebView video rendering or add arbitrary user shader loading.

Requested mode and tuning are persisted independently from runtime status. Mode defaults to `auto`; sharpness is an intuitive `0..100` value; denoise defaults on; the target cap is `auto`, `1080p`, `1440p`, or `2160p`. Numeric caps constrain the FSR intermediate output's shorter edge while preserving aspect ratio instead of forcing a display mode. Native code validates and maps values. The shader skips non-upscale frames, and any capability/load/compile failure clears the managed shader and retains ordinary mpv scaling.
