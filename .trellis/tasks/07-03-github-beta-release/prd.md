# brainstorm: GitHub beta 自动发版

## Goal

为 OhMyCine Player 增加 GitHub Actions beta 发版流水线：从 `v0.0.1` 开始使用 `v0.0.x` 的最后一位作为 beta 迭代号，自动构建 Windows 安装包和 portable zip，并发布到 GitHub prerelease。

## What I already know

* 用户希望 GitHub 自动编译 Player，并在 Release 中提供安装包和 `release` 程序目录打包 zip。
* 版本语义约定为：`v0.0.1`、`v0.0.2`、`v0.0.3` 代表 beta 迭代；前两位代表当前产品版本阶段，最后一位代表 beta 序号。
* GitHub Release 应标记为 beta/prerelease，即使 tag 形式是标准 `vX.Y.Z`。
* 仓库已有 `.github/workflows/player.yml`，会在 Player 变更时跑 lint、typecheck、多平台 Tauri build，并上传构建 artifact。
* 仓库已有 `.github/workflows/manual-build.yml`，支持手动构建各组件。
* `player/package.json` 当前版本为 `0.1.0`，包含 `tauri:build:windows` 脚本：`tauri build --target x86_64-pc-windows-gnu --bundles nsis`。
* `player/src-tauri/tauri.conf.json` 和 `player/src-tauri/Cargo.toml` 当前版本均为 `0.1.0`。
* Tauri bundle 已声明 Windows 运行期资源：`libmpv-2.dll`、`libmpv-wrapper.dll` 等。

## Assumptions (temporary)

* 本任务只实现 Player 的 beta 发版，不改 Hub、Server、CLI 发版。
* GitHub Actions 使用仓库默认 `GITHUB_TOKEN` 创建 GitHub Release 和上传资产。
* beta tag 由用户手动创建或通过 workflow 手动输入版本触发；流水线不自动决定下一版本号。
* portable zip 应尽量从构建后的 release 可执行目录整理出干净目录，而不是无过滤地打包整个 `target`。

## Open Questions

* 暂无阻塞问题；优先按用户已确认的 `v0.0.x` beta 版本语义实现。

## Requirements (evolving)

* 新增或调整 GitHub Actions，使其可以自动发布 Player beta prerelease。
* 支持从 `v0.0.1` 这类 tag 触发发版，并保留手动触发能力。
* 发版时同步 `player/package.json`、`player/src-tauri/tauri.conf.json`、`player/src-tauri/Cargo.toml` 的版本号到 tag 去掉 `v` 后的版本。
* 构建 Windows Player 安装包。
* 构建 Windows portable zip，包含可运行程序和必要资源。
* Release 资产命名包含产品、版本、平台、架构和产物类型。
* Release 标记为 prerelease/beta。
* 普通 Player CI 的 Windows 包构建不再使用 `windows-latest`/MSVC Tauri build，统一使用 `ubuntu-latest` + `x86_64-pc-windows-gnu` + `npm run setup:libmpv -- windows` + `RUSTC="$(rustup which rustc)" npm run tauri:build:windows`。
* 手动 build workflow 中的 Player Windows 构建同样走 Windows GNU cross-build，避免重新引入 MSVC/libmpv 链路。
* Player CI 保留并显式运行 `lint`、`typecheck`、`build`，以及现有 scraper/auth/fault-isolation/index-scheduler/TMDB verify 脚本。
* Beta release workflow 需要有 PR/branch push 的 dry-run/validate 路径，验证版本解析和脚本结构，但不执行 `gh release` 发布。
* `workflow_dispatch.inputs.version` 不直接插入 bash run block，必须先通过环境变量传入再读取。

## Acceptance Criteria (evolving)

* [x] 存在可由 `workflow_dispatch` 手动触发的 Player beta release workflow。
* [x] 推送 `v*.*.*` tag 时可触发 Player beta release workflow。
* [x] workflow 会校验 tag/version 格式，并将版本写入 Player 的三个版本源文件。
* [x] workflow 构建并上传 Windows 安装包。
* [x] workflow 构建并上传 Windows portable zip。
* [x] GitHub Release 使用对应 tag，标记为 prerelease，并附带安装包和 zip。
* [x] 本地至少完成 workflow YAML 语法/结构检查，以及 Player 现有 lint/typecheck 能力范围内的验证。
* [x] 普通 Player CI 的 Windows 包构建已切换为 Ubuntu 上的 Windows GNU cross-build。
* [x] 手动 Player Windows build 已切换为同一 Windows GNU cross-build 路径。
* [x] 普通 Player CI 显式运行 Player 关键质量门和 verify 脚本。
* [x] Beta workflow 增加不发布 GitHub Release 的 dry-run/validate 路径。
* [x] Beta workflow 的手动 version 输入通过 env 传入 bash，避免直接插入 run block。

## Definition of Done (team quality bar)

* Tests added/updated where appropriate.
* Lint / typecheck / relevant static checks pass or failures are documented.
* CI/release docs or PRD updated if behavior changes.
* Rollout/rollback considered: release workflow remains manually controllable and prerelease-only.

## Out of Scope (explicit)

* 不实现稳定版正式 release 自动化。
* 不实现自动递增版本号或 changelog 生成。
* 不实现代码签名、公证、Windows 证书签名。
* 不实现 macOS/Linux release 资产。
* 不调整 Player 产品代码和播放能力。

## Technical Notes

* Likely impacted files:
  * `.github/workflows/player.yml`
  * `.github/workflows/manual-build.yml`
  * new `.github/workflows/player-beta-release.yml` or equivalent
  * optional helper scripts under `player/scripts/`
* Existing Player commands:
  * `npm run lint`
  * `npm run typecheck`
  * `npm run build`
  * `npm run tauri:build:windows`
* Existing Windows target path mentioned by user:
  * `player/src-tauri/target/x86_64-pc-windows-gnu/release`
* Candidate implementation:
  * Add a dedicated `Player Beta Release` workflow.
  * Use `workflow_dispatch` input `version` and `push.tags: ["v*.*.*"]`.
  * Use Bash on `ubuntu-latest` to update JSON/TOML versions, package portable zip, generate checksums, and create prerelease via GitHub CLI.
  * Use the existing npm lockfile and npm cache.
  * Use Rust stable and the existing Windows GNU Tauri build path.
* Implemented approach:
  * Added `.github/workflows/player-beta-release.yml`.
  * Uses `ubuntu-latest` plus the existing Windows GNU cross-build path to preserve `player/src-tauri/target/x86_64-pc-windows-gnu/release`.
  * Uses GitHub CLI in the workflow to create or update a prerelease.
  * Portable zip includes only Windows runtime files from the release directory: `ohmycine-player.exe`, `WebView2Loader.dll`, `libmpv-wrapper.dll`, `libmpv-2.dll`, and the bundled third-party license.
  * Local validation covered YAML parsing, bash syntax checks for workflow run blocks, packaging script rehearsal against an existing Windows GNU release directory, direct `vue-tsc --noEmit`, and `cargo check --target x86_64-pc-windows-gnu`.
  * Local npm-based `lint`/`build` could not be completed because WSL only exposes Windows Node/npm; Windows npm fails on UNC working directories and Windows Node cannot use Linux-installed Rollup optional dependencies.
* CI alignment update:
  * `.github/workflows/player.yml` keeps Linux/macOS native Tauri builds, but replaces the Windows job with `player-windows-gnu` on `ubuntu-latest`.
  * `.github/workflows/manual-build.yml` exposes `windows-gnu` instead of `windows-latest` for Player manual builds.
  * Both ordinary CI and manual Windows Player builds run `npm run setup:libmpv -- windows` before `RUSTC="$(rustup which rustc)" npm run tauri:build:windows`.
  * `.github/workflows/player-beta-release.yml` now separates dry-run validation from actual prerelease publishing; PR/branch push validation does not call `gh release`.
