# Pinned Windows libmpv build recovery

`setup-libmpv.mjs` first uses the existing LGPL x86-64 archive from
`zhongfly/mpv-winbuild`, release `2026-10-09-fd9265a905`:

- Archive: `mpv-dev-lgpl-x86_64-20261009-git-fd9265a905.7z`
- SHA-256: `7978aa8f00bcdbf21d300c97e96975e7a5b4a1c720d5ad331638cf9d21d7e73c`

That upstream uses rolling release retention. Only an HTTP 404 or 410 for this
archive enables recovery from the already published [Player v1.1.80 standard
package](https://github.com/yuanjing-hash/OhMyCine/releases/tag/v1.1.80). Other HTTP,
network, timeout, checksum or extraction failures stop the build. Recovery does
not select a newer upstream release or alter Player playback settings.

| Recovery input | SHA-256 |
| --- | --- |
| `OhMyCine-Player-v1.1.80-windows-x64-standard.zip` | `5f80b7e49dc93e288b96a4a297a17ca3b94f366d070767ae9420c9fcc7299d53` |
| Its `libmpv-2.dll` (101,179,392 bytes) | `bed9c39249b74554305e128d3f19c1348c7010832c8f4794daa24a9ed881d8e2` |
| `mpv-windows-x86_64-20261009.dll.a` (53,308 bytes) | `bef1b89f534bc86b33135e1f04fa2d5064b9d48b5de8bc9866665bbf43def793` |

The recovery DLL is byte-identical to the Windows runtime already shipped in
v1.1.80, built from this same fixed upstream archive. The small import library is
retained from that runtime setup; it contains import stubs, not a second runtime
DLL. The 54 referenced mpv API exports are present in the published DLL. Both
Windows toolchains use these stubs: GNU resolves `libmpv.dll.a`, and MSVC resolves
its identical `mpv.lib` copy. Runtime DLLs are not checked into this directory.

The existing [LGPL-2.1 license](../../src-tauri/lib/LICENSE) is retained and is
packaged as `THIRD-PARTY-LICENSES.txt` by the official Player release workflow.
Upstream build project/source references:

- <https://github.com/zhongfly/mpv-winbuild>
- <https://github.com/mpv-player/mpv>

On recovery a clean build temporarily downloads the 125,210,796-byte historical
standard package, verifies its fixed archive checksum, extracts only the named
DLL, verifies that DLL, and verifies the checked-in import library before use.
The remaining historical application and remux files are never installed.
