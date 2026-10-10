# Offline remux runtime

The Player uses native FFmpeg stream copy to combine downloaded finite tracks.
Provider plugins cannot select an executable, command arguments or local paths.
The input protocol is restricted to local files and the demuxers to MP4/MOV,
MPEG-TS and Matroska/WebM; nested playlists and external MOV references are disabled.

## Windows x64

`npm run setup:remux` installs the pinned LGPL shared build before packaging:

- Build: `ffmpeg-n8.1.3-14-g330caae0c1-win64-lgpl-shared-8.1`
- Archive SHA-256: `0314e211fbd06b92ffb529b96d3358a2c60c61d4ab0071695d940b20ce776fa5`
- [Archive and build metadata](https://github.com/BtbN/FFmpeg-Builds/releases/tag/autobuild-2026-10-06-13-06)
- [Build scripts](https://github.com/BtbN/FFmpeg-Builds)
- [Corresponding FFmpeg source revision](https://github.com/FFmpeg/FFmpeg/tree/330caae0c1)

The downloaded archive is checksum verified. The executable and adjacent DLLs
are verified against the tracked `remux-windows.sha256` at setup and each runtime
invocation. Include the entire generated `resources/remux` directory, including
`LICENSE.txt`, in installers and portable archives. Do not substitute a PATH binary.

## Android ARM64

`node scripts/setup-remux.mjs --android` obtains the headers for the already bundled
FFmpeg libraries at revision `fc4960b155aa33b9a08cf26c5e0a0530f0545f24`.

- [Corresponding source](https://github.com/FFmpeg/FFmpeg/tree/fc4960b155aa33b9a08cf26c5e0a0530f0545f24)
- Source archive SHA-256: `10ff00bd0f4e2fff4e8a0c7ca59ed5b5cfe2091d607e324f941aacc8fba8156e`
- Required ABI: avformat `62.13.102`, avcodec `62.30.100`

The C bridge is compiled by `build.rs` and links the app's existing ARM64 shared
libraries. It rejects an incompatible runtime ABI and uses a native interruption
callback for pause/cancel. Updating the vendored libraries requires updating the
matching source pin, archive hash and bridge ABI guard together.

Desktop tests do not establish Android cross-compilation, APK assembly or device
SAF behavior. Verify those with the configured Android SDK/NDK and Rust target.
