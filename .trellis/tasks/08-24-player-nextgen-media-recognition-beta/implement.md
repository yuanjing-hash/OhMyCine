# Player 下一代多语言识别器执行计划

## 1. Parser 与领域契约

- [x] 冻结现有 Player scraper 行为和跨 Server corpus adapter。
- [x] 增加 Unicode-safe title/token parser、engine version 和结构化 query variant。
- [x] 扩展多语言季集 token pack、中文 ordinal 和合法标题保护。
- [x] 保持 file → parent → grandparent 路径合并，改为跨来源 canonical 优先。

## 2. TMDB 候选决策

- [x] 将 `searchCandidate` 从首个命中改为有界 movie/tv recall、去重聚合和确定性排名。
- [x] top-3 详情 enrich 纳入 original/alternative/translation/year/type/season evidence。
- [x] 增加 low-confidence、candidate-conflict 和请求预算结果，兼容既有调用者 DTO。
- [x] 保留失败后的 `searchChoices` / `getDetail` 人工恢复流程。

## 3. Cache 与人工覆盖兼容

- [x] 持久化 recognition engine version 和 match source。
- [x] 迁移旧 cache：保留安全文件事实、人工身份及用户覆盖，重新计算旧失败/过期自动结果。
- [x] 增量扫描在 engine drift 时触发有界重识别，不删除或重置 Player profile。

## 4. Corpus 与回归

- [x] Player verifier 读取 Server provider-neutral corpus，断言 must-match/must-reject 和候选顺序不变性。
- [x] 增加斗罗大陆中文季集、合法整标题/括号/连字符反例。
- [x] 增加 Latin diacritics、Japanese、Korean、Cyrillic、Arabic、Thai 及多语言季集标签 fixture。
- [x] 覆盖 TMDB 查询/详情预算、部分失败、冲突、强年份冲突和人工 override 保留。

## 5. 文档与质量门

- [x] 更新 Player architecture 和 frontend raw-scraping spec。
- [x] 运行 `verify:scraper`、raw cache/index scheduler、TMDB auth 及其它受影响 verification。
- [x] 运行 typecheck、ESLint、Vite production build、`git diff --check`。
- [x] 若 Rust/Tauri 未修改，记录无需 Cargo 门；若修改则运行 cargo fmt/check/clippy/test。
- [x] 使用 `trellis-check` 完成最终规范、类型、跨层和测试复核。

## 6. Commit、Push 与 Beta

- [ ] 审计 dirty worktree，只暂存本任务拥有的文件。
- [ ] 创建 `feat(player): 升级多语言媒体识别器` 或等价 Conventional Commit。
- [ ] fetch 并将提交安全落到最新远端 `develop`，push 后验证远端 SHA。
- [ ] 选择下一个未占用 patch Beta 版本并触发 `player-beta-release.yml`。
- [ ] 等待 workflow 完成，核对 prerelease、Windows 三包、SHA-256、updater manifest 和 Android ARM64 APK。
- [ ] 记录 release/tag/commit 链接；完成后使用 `trellis-finish-work` 收口任务。

## Validation Commands

```powershell
cd player
npm run verify:scraper
npm run verify:raw-scan-cache
npm run verify:raw-source-index-scheduler
npm run verify:tmdb-auth
npm run typecheck
npm run lint
npm run build
cd ..
git diff --check
```

本机 `npm` 不在 PATH 时，使用 Codex bundled Node 直接运行仓库内 `tsx`、`vue-tsc`、`eslint` 和 `vite` 入口，不触发依赖自动安装。

## Risk / Rollback

- Parser 误删：每种结构 token 必须同时有正例和合法标题反例，可单独回退 token pack。
- TMDB 请求放大：搜索 ≤10、详情 ≤3，所有路径通过计数测试。
- 自动误匹配：低置信/冲突默认未识别；不以“提高命中率”为由降低冻结 corpus 阈值。
- Cache 漂移：迁移只使自动派生结果过期，人工身份和用户数据优先保留。
- Release 失败：不移动 tag 到其它提交，不从 feature/历史/local-only commit 发布；修复后仍从最新 `origin/develop` 重跑。

## Implementation Validation Snapshot (2026-08-24)

- `verify:scraper`: passed; shared `provider-neutral-v1` corpus reports 9 must-match and 3 must-reject cases, with reversed provider candidate order producing the same decision.
- Supplemental parser fixtures passed for Chinese, English, Japanese, Korean, Latin diacritics, Cyrillic, Arabic, Thai, French, and German labels. `斗罗大陆 - - 第2集`, numeric title `1917`, and legal whole-title regressions are frozen.
- Actual mocked TMDB orchestration used 2/10 search requests and 1/3 detail requests; HTTP 429 stopped after the first search request and after the first detail request. Ordinary detail failures degrade safely, while authentication, rate-limit, and cancellation errors retain termination semantics.
- Cache verification passed for conservative legacy-match preservation, stale automatic-engine drift, manual-source persistence, raw cache storage, source index scheduler, and TMDB authentication routing.
- `vue-tsc --noEmit`, full Player ESLint, and Vite production build passed. No Rust/Tauri files changed, so Cargo checks are not applicable to this implementation slice.

## Pre-Start Gate

- [x] 用户明确要求 Player 独立识别器升级并覆盖其它语言文字。
- [x] 用户授权质量门通过后直接发布 Beta，不需再次确认发布动作。
- [x] 已确认当前 Player scraper 文件无未提交修改，可独立拥有。
- [x] 已确认 Server corpus、Player release workflow 和 develop-only Beta guard 可复用。
- [x] 用户在本最终规划摘要之后批准进入实现阶段（Trellis 强制门）。
