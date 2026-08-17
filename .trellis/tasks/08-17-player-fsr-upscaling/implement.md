# 接入 libmpv FSR 超分 — Implementation Plan

1. Add the reviewed FSR 1 GLSL and MIT/provenance notices.
2. Extend TypeScript/Rust/Kotlin engine settings and normalization tests for mode, target cap, sharpness, and denoise.
3. Add Windows managed-cache materialization, shader list switching, parameter mapping, log-based fallback, and diagnostics.
4. Add Android setup-time asset sync, private-file installation, shader switching, log-based fallback, and diagnostics.
5. Add Windows playback-settings controls and Android right-top-more child panel.
6. Add focused static/unit checks, then run the parent validation matrix and real-device follow-up.

Rollback points are the persisted mode default and the native managed-shader clear path. FSR errors must never be returned as fatal playback-engine initialization errors.
