# 修复原始文件源分类与单文件电影识别

## Goal

修复 OpenList/Alist 原始文件源扫描中“清晰单文件电影不识别、一级手动分类目录不生效”的问题，让已手动分类目录和未分类混放目录都能按预期生成海报墙分类。

## Requirements

* 已手动分类的路径应尊重用户路径分类：
  * `动漫/剧名/Season 01/S01E01.mkv` → 剧集/分集候选，路径分类为 `动漫`。
  * `电影/阿凡达.mp4` → 电影候选，路径分类为 `电影`。
  * `华语电影/阿凡达.mp4` → 电影候选，路径分类为 `华语电影`。
  * `电影/阿凡达/阿凡达.mp4` → 电影候选，路径分类为 `电影`。
* 未手动分类的混放路径应自动识别作品类型：
  * `阿凡达.mp4` → 电影候选，分类由 TMDB 元数据规则或电影兜底决定。
  * `阿凡达/阿凡达.mp4` → 电影候选，分类由 TMDB 元数据规则或电影兜底决定。
  * `剧名/Season 01/S01E01.mkv`、`剧名/S01E01.mkv` → 剧集/分集候选，分类由 TMDB 元数据规则或剧集兜底决定。
* 明显噪声文件名如 `sample.mp4`、`4K.mp4`、`video.mp4` 不应被误当作电影。
* 修复应保留 OpenList/Alist 只读边界，不写回远端。
* 修复后必须重新生成 Windows GNU release `ohmycine-player.exe`。

## Acceptance Criteria

* [x] `阿凡达.mp4` 扫描后成为 movie candidate，并参与 TMDB 匹配。
* [x] `电影/阿凡达.mp4` 和 `华语电影/阿凡达.mp4` 扫描后保留对应路径分类。
* [x] `阿凡达/阿凡达.mp4` 不伪造路径分类，分类走 TMDB 规则或电影兜底。
* [x] 既有动漫多季剧集识别不回退。
* [x] 新增/更新 verify 覆盖上述路径。
* [x] 重新跑 typecheck/lint/build/相关 verify。
* [x] 重新跑 Windows GNU Tauri build，确认 exe 时间戳刷新。

## Out of Scope

* 不做新的 DataSource。
* 不做图片二进制本地缓存。
* 不改 OpenList/Alist 远端文件结构。

## Technical Notes

* Parent task: `.trellis/tasks/07-08-openlist`
* Likely files:
  * `player/src/services/scraper/parser.ts`
  * `player/scripts/verify-scraper-title-classification.ts`
  * `docs/architecture/06-roadmap.md` if roadmap wording changes

## Verification

* `TMPDIR=/tmp node node_modules/tsx/dist/cli.mjs scripts/verify-scraper-title-classification.ts` passed.
* `node node_modules/vue-tsc/bin/vue-tsc.js --noEmit` passed.
* `node node_modules/eslint/bin/eslint.js src --ext .ts,.vue` passed.
* `node node_modules/vite/bin/vite.js build` passed.
* `rustup run stable cargo fmt --manifest-path player/src-tauri/Cargo.toml --check` passed.
* `RUSTC="$(rustup which rustc)" rustup run stable cargo check --manifest-path player/src-tauri/Cargo.toml` passed.
* `node scripts/setup-libmpv.mjs windows` passed.
* `RUSTC="$(rustup which rustc)" node node_modules/@tauri-apps/cli/tauri.js build --target x86_64-pc-windows-gnu --bundles nsis` passed.
* Refreshed exe: `player/src-tauri/target/x86_64-pc-windows-gnu/release/ohmycine-player.exe`, timestamp `2026-07-08 14:55:29 +0800`, size `29860846` bytes.
