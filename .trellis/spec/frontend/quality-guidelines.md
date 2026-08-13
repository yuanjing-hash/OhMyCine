# Frontend Quality Guidelines

> Code quality standards for OhMyCine frontend development.

---

## Overview

Frontend quality is measured by Player independence, type safety, immersive UI consistency, secure credential handling, and passing local checks. Do not rewrite existing Player work just to match a planned architecture; adopt the current state and improve incrementally through Trellis tasks.

---

## Required Patterns

- Vue 3 Composition API with `<script setup>`.
- TypeScript strict mode.
- Pinia for shared state.
- Vue Router for route-level navigation.
- Vue I18n for user-facing text where configured.
- UnoCSS + CSS variables for styling.
- DataSourceManager/DataSource abstractions for all media source browsing/playback.
- libmpv integration through Rust/Tauri modules, keeping platform rendering explicit.
- Secure storage for credentials when available.
- Disabled/placeholder states for Server-dependent UI when Server is disconnected.
- Route views should retain orchestration and template composition; reusable pure business logic belongs in typed services or composables.

---

## Forbidden Patterns

- Making Server required for basic local/Emby/Jellyfin/OpenList/Alist/CloudDrive2 playback.
- Storing API keys, cookies, passwords, AI keys, or Server tokens in regular config/localStorage/Pinia persistence.
- Sending local absolute paths or credentials to AI providers by default.
- Hard-coding `Alist` user-facing support without acknowledging OpenList/Alist compatibility.
- Assuming raw file source scraping requires fixed physical top-level folders such as `movie`, `tv`, `Movies`, or `TV`; use the user-selected root and infer structure below it.
- Writing scrape logs, TMDB matches, poster/cache files, category assignments, renames, moves, or deletes back to OpenList/Alist or other raw providers as part of Player-side scraping.
- Assuming Android/iOS parity with desktop before implementation.
- Adding persistent controls that obstruct artwork/video when hover-revealed chrome is more appropriate.
- Using broad `any` for core media/config/API types.
- Rewriting current Player modules during Trellis migration tasks.

---

## Testing and Verification

When `player/` exists, run local checks from Windows PowerShell:

```powershell
cd player
npm run typecheck
npm run lint
npm run build
```

For Tauri/Rust changes, also run the relevant Cargo command when configured, such as `cargo check` from `player/src-tauri`.

Before completing Player dependency-security work, run `npm audit` against the official npm registry when the configured mirror does not provide an advisory endpoint. Known vulnerabilities with available fixes must be resolved; a mirror audit `404` is not evidence that the dependency graph is clean.

For Rust/Tauri quality-gate work, the required zero-warning command is:

```bash
cargo clippy --manifest-path src-tauri/Cargo.toml --all-targets -- -D warnings
```

Do not weaken or skip the all-target Clippy gate to make a task appear complete.

For Windows-native development, use the installed Windows Rust toolchain directly and treat a successful native `cargo check` / `tauri dev` run as the authoritative desktop result. The Linux/WSL `x86_64-pc-windows-gnu` cross-build path remains a CI/release compatibility path; when running that path, prefer the rustup toolchain explicitly if `PATH` contains another Rust distribution and pass the same `RUSTC` override to package builds.

Runtime verification must be non-destructive by default. Preserve the owner's existing standard and portable Player profiles, including data sources, credentials, settings, playback history, scrape caches, and WebView state. Tests that need a clean profile should use an isolated temporary/portable directory. Never clear the real profile unless the owner explicitly requests it or approves the exact destructive test scope in advance.

When a Player task changes Tauri runtime, libmpv, windowing, or rendering behavior, the verification contract is:

| Case | Required check | Completion rule |
|------|----------------|-----------------|
| Web/UI-only Player change | `npm run typecheck`, `npm run lint`, `npm run build` | All pass |
| Rust/Tauri backend change | Above plus `cargo check` for `player/src-tauri` | All pass |
| Native Rust portability | Ubuntu with system `libmpv` plus Windows with vendored libmpv run `cargo test --lib` and `cargo check --all-targets` | Both native targets pass before the Windows GNU package job |
| Runtime/render/libmpv change | Above plus `npm run setup:libmpv -- windows` and Windows-native `npm run tauri:dev:windows` when the local graphics/runtime environment can launch it | Report full verification only after the Windows desktop window/runtime is exercised |
| WSL/WSLg compatibility check | `tauri dev` compiles and starts the app process but emits EGL/Mesa/DRI warnings or cannot show a reliable window | Mark as supplementary partial verification; require the Windows-native check for completion |
| Windows-native package change | `npm run setup:libmpv -- windows` plus `npm run tauri:build:windows:native` | The Windows executable/installer is generated and then launch/install/playback is exercised on Windows when in scope |
| Windows GNU CI/release package change | Run the existing `npm run tauri:build:windows` cross-build path in its Linux CI/release environment | Cross-build passes only when the Windows `.exe` and installer are generated; Windows-native runtime/signing/playback remain separate checks |
| Android UI/data-source preview change | `npm run tauri:build:android:preview` with SDK 36, NDK `27.2.12479018`, and the ARM64 Rust target installed | An installable ARM64 debug APK is generated; report playback as unsupported until Android libmpv and native surface rendering are integrated |
| Native file picker / dialog plugin change | Above plus `cargo check` and a Windows-native `npm run tauri:dev:windows` attempt when possible | Static checks alone are insufficient; exercise the Windows dialog/playback interaction when it is in scope |

## Scenario: Windows-Native Desktop And Android Builds

### 1. Scope / Trigger

- Trigger: changing Player packaging, Android Gradle/Kotlin/Rust code, libmpv runtime setup, or local build instructions.
- Windows is the authoritative local host for both Windows MSVC and Android ARM64 builds. WSL is supplementary for native Linux compatibility and must not be the default Android build path.

### 2. Signatures

- Windows desktop: `npm run setup:libmpv -- windows` then `npm run tauri:build:windows:native`.
- Android ARM64: `npm run tauri:build:android:preview`.
- Android entry point: `node scripts/build-android-preview.mjs`; npm scripts must not embed `rm`, `env -u`, `$HOME`, or WSL-only PATH expressions.

### 3. Contracts

- SDK discovery order: `ANDROID_SDK_ROOT` / `ANDROID_HOME`, the owner's `D:\Software\Android\Sdk` convention when present, then the Windows user default SDK directory.
- Required Android packages: Platform 36, Build Tools 35.0.0 and 36.0.0 on Windows, NDK `27.2.12479018`, and Rust target `aarch64-linux-android`.
- JDK discovery respects `JAVA_HOME`; a compatible JDK 17 or newer is accepted.
- `GRADLE_USER_HOME` respects explicit configuration. The owner's Windows profile uses `D:\Software\Android\Gradle`; repository code must not force that location on other machines.
- Local Maven mirror configuration belongs in the user's Gradle home, not repository Gradle files. CI and other developers retain official `google()` / `mavenCentral()` repositories.
- Windows Kotlin compilation may receive Tauri/plugin sources from the C-drive Cargo registry while the project is on D. Disable Kotlin incremental/daemon execution through build-process environment options to avoid cross-root fallback exceptions.

### 4. Validation & Error Matrix

- SDK platform missing -> fail before Tauri starts and report the expected SDK root/version.
- NDK or Build Tools missing -> fail before compilation with the exact missing version.
- JDK missing -> fail with a `JAVA_HOME` instruction; do not install into the repository.
- Google Maven TLS fails locally -> use a user-scoped Gradle mirror while keeping repository sources official.
- C/D Kotlin incremental root mismatch -> compile in-process without incremental mode; do not move the project to WSL.

### 5. Good/Base/Bad Cases

- Good: PowerShell builds both MSVC NSIS and Android APK with SDK/NDK/Gradle caches on D and no WSL process.
- Base: GitHub Linux runner uses the same Node Android entry point with environment-provided SDK/JDK and only Build Tools 36.
- Bad: npm calls `rm -f`, `env -u`, `$HOME`, or invokes Android Cargo/Gradle under `/mnt/d`.

### 6. Tests Required

- `npm run verify:android-playback` asserts the Node build entry and rejects POSIX-only Android npm commands.
- Run `npm run tauri:build:android:preview` from Windows PowerShell and assert `app-universal-debug.apk` exists.
- Run `npm run tauri:build:windows:native` and assert the MSVC executable and NSIS installer exist.
- `git diff --check` and CI release-contract tests must still pass.

### 7. Wrong vs Correct

Wrong:

```json
"tauri:build:android:preview": "rm -f ... && env -u HTTP_PROXY ... tauri android build ..."
```

Correct:

```json
"tauri:build:android:preview": "node scripts/build-android-preview.mjs"
```
| DataSource / external media source UI change | `npm run typecheck`, `npm run lint`, `npm run build`, plus `npm run tauri:build:windows:native` for local Windows packaging or the existing cross-build script in CI | Static checks and package generation pass; live server/runtime browsing may remain user-verified when credentials or Windows host access are user-owned |

### Tauri Windows-Only Packaging Contract

- Keep platform-specific runtime resources out of the shared `player/src-tauri/tauri.conf.json` `bundle.resources`. Tauri validates every declared resource during packaging, so a Windows GNU CI job that only ran `npm run setup:libmpv -- windows` must not require Linux `.so` or macOS `.dylib` files.
- Current-stage Player packaging is Windows-only. Keep `player/src-tauri/tauri.windows.conf.json` as the only platform resource override until Linux/macOS Player rendering and packaging are implemented.
- Windows resources should include only Windows runtime files such as `lib/libmpv-2.dll`, `lib/libmpv-wrapper.dll`, and license text. Do not include `libmpv.dll.a` or `mpv.lib`; they are link-time import libraries, not runtime bundle resources.
- Local Windows development and manual builds use the native `x86_64-pc-windows-msvc` scripts. Player CI and beta release guardrails continue to validate/publish Windows GNU packages through `ubuntu-latest` + `x86_64-pc-windows-gnu`.
- Player CI compiles and tests native Linux and native Windows MSVC Rust targets before the Windows GNU packaging job. A cross-built Windows GNU package does not replace either native compile gate.
- Do not add Linux/macOS Player package jobs, Linux/macOS runtime resource configs, or blocking CI checks for those packages before the corresponding Player renderers and packaging chains are complete. Future Linux/macOS work should return explicit unsupported/future runtime states until implemented, then add CI in the same task that finishes the renderer/package path.

## Scenario: Player Release Packaging

### 1. Scope / Trigger

- Trigger: Any GitHub Actions or packaging change that publishes OhMyCine Player beta or stable assets.
- Scope: Windows GNU target releases that produce release notes, a Windows installer, a standard zip, and a portable zip; the selected channel controls whether GitHub marks the release as prerelease or latest stable.

### 2. Signatures

- Version input/tag: `vMAJOR.MINOR.PATCH`, for example `v1.0.0`.
- App version written into Player files: `MAJOR.MINOR.PATCH` without the leading `v`.
- Build command: `RUSTC="$(rustup which rustc)" npm run tauri:build:windows`.
- Target output root: `player/src-tauri/target/x86_64-pc-windows-gnu/release`.

### 3. Contracts

- Feature and fix branches start from `develop` and merge back into `develop` after verification. Release branches may be used for preparation but are never release sources.
- A tag push always selects `beta`, and the tag commit must exactly equal the latest fetched `origin/develop` commit.
- Manual `workflow_dispatch channel=beta` must select the `develop` ref and run at the latest fetched `origin/develop` commit.
- Manual `workflow_dispatch channel=stable` must select the `main` ref and run at the latest fetched `origin/main` commit. Merge `develop` into `main` only after the owner confirms a Stable release.
- Reject feature/fix/release branch commits, historical commits, and local unpushed commits. Being an ancestor or descendant of the required remote tip is insufficient; the release SHA must be exactly equal to it.
- `beta` channel must publish with `--prerelease --latest=false`; `stable` channel must publish with `--prerelease=false --latest` and must not include Beta in the title.
- Release notes must be generated from git history for the current beta tag. Prefer version-sorted semver-like tags; if no previous `v*.*.*` tag exists, include commits from the repository initial commit through the current release commit.
- Release notes must group commit subjects by Conventional Commit type: `feat`, `fix`, `docs`, `ci`, `chore`, `refactor`, `test`, and `other`. Keep the original subject so scopes such as `feat(player): ...` remain visible.
- Manual `workflow_dispatch.inputs.release_notes` may be appended only as an `Extra Notes` section; do not include it for tag-push releases.
- Release notes must include the selected channel's semantic-version rule, asset descriptions, and the SHA-256 checksum file description.
- Release notes generation must not print secrets, tokens, signed URLs, or GitHub Actions environment dumps. It may use commit subjects and the explicit manual notes input only.
- Release assets must include:
  - `OhMyCine-Player-vMAJOR.MINOR.PATCH-windows-x64-setup.exe`
  - `OhMyCine-Player-vMAJOR.MINOR.PATCH-windows-x64-standard.zip`
  - `OhMyCine-Player-vMAJOR.MINOR.PATCH-windows-x64-portable.zip`
  - `OhMyCine-Player-vMAJOR.MINOR.PATCH-windows-x64.sha256`
- Standard and portable zips must be curated from the release directory. Include only Windows runtime files such as `ohmycine-player.exe`, `WebView2Loader.dll`, `libmpv-wrapper.dll`, `libmpv-2.dll`, and license text.
- Standard zip must not contain `portable.flag`, `data`, `cache`, or `logs`; it uses the normal LocalAppData profile.
- Portable zip must contain `portable.flag` but must not ship pre-created `data`, `cache`, or `logs`; its first launch creates an empty EXE-adjacent profile.
- Do not copy the whole `target/.../release` directory or the whole `target/.../release/lib` directory into either zip. Those folders can contain build intermediates or cross-platform resources unrelated to Windows runtime.

### 4. Validation & Error Matrix

- Invalid version format -> fail before install/build steps.
- Tag-push SHA differs from the latest fetched `origin/develop` -> fail before install/build steps.
- Manual Beta SHA differs from the latest fetched `origin/develop` -> fail before install/build steps.
- Manual Stable SHA differs from the latest fetched `origin/main` -> fail before install/build steps.
- Manual Beta selects a ref other than `develop`, manual Stable selects a ref other than `main`, or a tag push attempts Stable -> fail before install/build steps.
- Candidate SHA is from a feature/fix/release branch, is an older commit, or is a local unpushed successor -> fail even when it shares history with the required source branch.
- Missing `bundle/nsis/*setup.exe` -> fail packaging.
- Missing `ohmycine-player.exe` or required Windows DLL -> fail standard and portable packaging.
- Existing GitHub prerelease for the same tag -> upload assets with clobber/update behavior rather than deleting the tag.
- No previous release tag -> generate notes from initial commit through the current release commit.
- Manual dispatch includes `release_notes` -> append them under `Extra Notes` after the generated commit groups.
- Tag push release has no manual notes input -> omit the `Extra Notes` section.

### 5. Good/Base/Bad Cases

- Good: a `v0.0.1` tag at the latest remote `develop` commit selects `beta` and creates a prerelease with installer, standard zip, portable zip, and checksum using the Windows GNU release directory.
- Good: manual `channel=beta` at the latest remote `develop` commit publishes the same Beta asset set.
- Good: manual `channel=stable` at the latest remote `main` commit creates the latest non-prerelease release with the same signed asset set and no Beta label.
- Good: `v0.0.2` release notes use `v0.0.1..v0.0.2`, group commit subjects by type, preserve scopes, and append manual notes only for `workflow_dispatch`.
- Base: manual `workflow_dispatch` with `version=v0.0.2` creates the tag/release at the workflow commit only after that commit exactly matches the selected channel's remote source tip.
- Bad: a tag on `main`, a feature/fix/release branch commit, an old `develop` commit, or a local commit ahead of `origin/develop` attempts to publish Beta.
- Bad: a manual Stable run uses any commit other than the latest fetched `origin/main`.
- Bad: zip contains `deps/`, `.fingerprint/`, `incremental/`, Linux `.so`, or macOS `.dylib` files.
- Bad: release notes dump `$GITHUB_ENV`, `$GH_TOKEN`, full environment output, signed playback URLs, or credential-like values from build steps.

### 6. Tests Required

- Parse workflow YAML and run bash syntax checks for embedded run blocks.
- Exercise the same release-source guard used by build jobs against synthetic Git history. Cover valid latest-remote Beta and Stable commits plus rejected wrong-branch, release-branch, historical, and local-unpushed commits.
- Dry-run the release-notes generator against synthetic git history covering previous-tag range selection, no-previous-tag fallback, Conventional Commit grouping, manual `Extra Notes`, and omission of environment secrets.
- Rehearse the packaging script against an existing `target/x86_64-pc-windows-gnu/release` directory when available.
- Run `cargo check --target x86_64-pc-windows-gnu`; run full `npm run tauri:build:windows` when local Node/npm and cross-build dependencies are available.
- Document local environment limitations when npm lint/build cannot run.

### 7. Wrong vs Correct

#### Wrong

```bash
zip -r player-portable.zip player/src-tauri/target/x86_64-pc-windows-gnu/release
```

#### Correct

```bash
for file in ohmycine-player.exe WebView2Loader.dll libmpv-wrapper.dll libmpv-2.dll; do
  cp "$file" standard/
  cp "$file" portable/
done
touch portable/portable.flag
zip -r OhMyCine-Player-v0.0.1-windows-x64-standard.zip standard/
zip -r OhMyCine-Player-v0.0.1-windows-x64-portable.zip portable/
```

For Emby/Jellyfin/OpenList/Alist/CloudDrive2 source work, also review:

- Settings data-source management flow: list, empty add state, type selection, provider-specific fields, cancel/add/save, edit, delete, enable/disable, and browse actions.
- Emby setup uses account/password authentication and automatic token capture, not manual access-token entry as the primary UX.
- Source sidebar rendering from ordered configs, including bottom plus navigation to data-source management and disabled-source affordance.
- SourceLibrary loading, empty, disabled, error, auth-required, library, and item states.
- Raw-source maintenance actions stay out of the media-library heading: desktop exposes rescrape, scan management, and folder browsing through the existing right-edge floating utilities, while mobile exposes the same context actions in the explicit Quick bottom sheet and never depends on hover.
- Official release builds may inject the read-only OhMyCine TMDB application credential only through CI secrets. User TMDB credentials override it through secure storage; neither value may appear in Git, ordinary settings, logs, diagnostics, or exports.
- Missing poster/backdrop fallbacks.
- Generic DataSource playback flow: UI obtains stream URLs through `DataSource.getStreamURL()`, not provider-specific route code.
- Token/API-key redaction in errors, logs, player labels, and exported config.
- Persistence boundary: new credentials are not written to localStorage or regular config; desktop credentials survive restart through the SQLite credential boundary, and browser-only fallback is visibly limited.
- Credential schema hardening: `DataSourceConfig` must not expose top-level `apiKey`, `username`, or `password`, and persistence/export must drop sensitive `extra` keys.

Do not treat Docker as a local development prerequisite.

---

## Code Review Checklist

- Components use `<script setup lang="ts">` and explicit props/events.
- Views do not bypass DataSource abstractions.
- Large route views keep reusable domain, formatting, grouping, and option-building logic in typed services/composables instead of accumulating it in the view module.
- Server disconnected state is handled.
- Planned provider cards such as 115 remain visibly disabled and non-callable until their DataSource is implemented.
- Credentials are stored securely and not exposed in logs/config/export.
- External requests have error handling and timeouts where service code controls them.
- Dependency security checks use a functioning advisory source and have no known fixable vulnerabilities.
- Rust/Tauri changes pass `cargo clippy --all-targets -- -D warnings` with zero warnings.
- Player-side AI only uses allowed metadata by default.
- Cinema OS tokens/classes are used instead of arbitrary styling.
- Keyboard shortcuts avoid input focus conflicts.
- Roadmap status is updated when implementation completion changes.

---

## Documentation Consistency

- Use `OpenList/Alist` or `OpenList (Alist-compatible API)` in docs and UI copy.
- Preserve Player independent-first wording.
- Keep Server as enhancement/automation layer.
- Clarify README quick-start or architecture examples as target design if files/features are not yet implemented.
- Keep final planned scope documented; adjust order, not scope.
- Before publishing README/docs screenshots, verify every referenced image exists and scan visible text plus PNG strings for credentials, private IPs, personal domains, absolute local paths, and tokenized URLs.
- Public docs and UI placeholders should use reserved example domains such as `.example.test` instead of private LAN IPs or personal hostnames.
