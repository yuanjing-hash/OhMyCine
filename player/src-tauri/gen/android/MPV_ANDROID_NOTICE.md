# mpv-android Runtime Notice

OhMyCine's Android playback build uses the ARM64 native runtime extracted from the official
`mpv-android` release `2026-04-25`:

- Project: <https://github.com/mpv-android/mpv-android>
- Asset: `app-default-arm64-v8a-release.apk`
- SHA-256: `4400bcba6be9cec1128e24d1eba153d8727384926b0639fa7fe44d4e36b04f81`

The runtime contains libmpv, FFmpeg libraries, the mpv-android JNI bridge, and their transitive
native dependencies. OhMyCine is GPL-3.0 licensed and distributes these components under their
respective upstream licenses. Source and build scripts for the exact runtime are available from
the tagged upstream project above.
