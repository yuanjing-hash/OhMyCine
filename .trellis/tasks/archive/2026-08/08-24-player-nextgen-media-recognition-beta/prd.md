# 升级 Player 多语言媒体识别器并发布 Beta

## Goal

让 Player 在不连接 Server 时，仍能对本地文件、OpenList/Alist、CloudDrive2、WebDAV、123、夸克及未来原始文件源执行可靠的自动媒体识别；能力不局限于中文，并与 Server 下一代识别器使用同一套 provider-neutral corpus 防止长期漂移。质量门通过后，将本次变更合入远端 `develop` 并直接发布 Player Beta。

## Background

- Player 当前在 `player/src/services/scraper/parser.ts` 和 `tmdb.ts` 中维护独立 TypeScript 识别链路，不能依赖 Server Go 包。
- 当前实现已有 `SxxExx`、`1x02`、英文 `EP`、中文 `第N集/话/季` 的基础能力，但仍会破坏合法括号/连字符标题，只支持有限中文数字，标题候选主要按中文/拉丁正则切分，并在 TMDB 搜索中接受首个“可接受”结果。
- Server 已建立 `server/internal/mediarecognition/testdata/corpus.v1.json`，覆盖数字标题、简繁、英文、日文、韩文、合法连字符、年份冲突和候选冲突；Player 应复用该脱敏 corpus 的输入与预期，不复制 MoviePilot GPL 源码，也不宣称掌握 Emby 私有算法。
- Emby/Jellyfin 和 ServerDataSource 默认使用其服务端元数据，不进入 Player 原始文件刮削识别流程。

## Requirements

1. Parser 必须以 Unicode 为默认边界，保留任何文字系统的合法标题字符；显式覆盖简体/繁体中文、英文及带变音符拉丁文字、日文假名/汉字、韩文、Cyrillic，并用不依赖脚本白名单的 fallback 支持 Arabic、Thai 等其它文字。
2. 季集解析必须继续支持 `S01E02`、`1x02`、`E/EP/Episode` 和中文数字 `第N集/话/季`，并增加常见日文、韩文及欧洲语言季集标签；结构 token 只能在作品标题仍有有效内容时删除，不能破坏《第八集》、`[REC]`、`Spider-Man`、`Tinker-Tailor-Soldier-Spy` 等合法整标题。
3. 路径识别保持 file → parent → grandparent 的既有契约，但 TMDB 查询预算必须优先给不同来源的 canonical 标题，再消费同一来源的噪声回退变体；请求数有明确上限且顺序确定。
4. TMDB 自动匹配必须同时考虑 movie/tv、本地化标题、原标题、alternative titles、translations、年份、解析类型、季集结构和候选唯一性；不得继续以结果数组首项作为最终身份。低置信和候选冲突必须返回未识别，正常流程不新增人工候选确认。
5. 失败后的既有人工关键词搜索、TMDB ID 精确获取和本地 override 流程保持可用；自动重算不得覆盖有效人工身份。
6. Player 与 Server 复用同一脱敏 corpus 和可比较的决策预期。Player 可有独立实现和阈值，但 corpus 中 `must_match` / `must_reject` 结果不得无解释漂移。
7. 新识别引擎必须有显式版本。旧自动失败/低质量识别缓存应在安全迁移后重新计算，但已有有效人工识别、用户元数据/图片 override、数据源配置、凭据、播放历史及扫描文件事实不得被删除或重置。
8. 不向 TMDB、日志、缓存或诊断发送/保存本地绝对路径、provider 凭据、签名 URL 或完整敏感响应。
9. 发布必须遵守 Player Release Packaging 规范：本次代码提交到最新远端 `develop`，Beta 只能从与 `origin/develop` 完全相同的提交发布；版本使用发布时远端最新 release/tag 的下一个未占用 patch 版本，GitHub Release 标记 prerelease。

## Acceptance Criteria

- [ ] `斗罗大陆/斗罗大陆 - - 第1集.mp4` 与第 2 集自动解析为同一 TV 作品《斗罗大陆》，季集号正确，不需要人工选择。
- [ ] corpus 的中文简繁、English、Japanese、Korean、数字标题、合法连字符、年份冲突及候选冲突用例通过；额外 fixture 覆盖 Latin diacritics、Cyrillic、Arabic/Thai 标题保留和常见多语言季集标签。
- [ ] 《第八集》、`[REC]`、`Spider-Man`、`Tinker-Tailor-Soldier-Spy` 不因结构/发布 token 清理而变空或被错误截断。
- [ ] TMDB 候选输入顺序变化不改变相同证据下的决策；明确冲突和强年份冲突均保持未识别；查询和详情 enrich 均在固定预算内。
- [ ] 旧缓存迁移只使需要重算的自动识别结果失效，并保留人工识别与其它用户数据；迁移回归测试通过。
- [ ] `verify:scraper`、相关缓存/调度验证、typecheck、lint、Vite build 及适用的 Rust/Tauri 检查通过。
- [ ] `docs/architecture/03-player-design.md` 与 `.trellis/spec/frontend/directory-structure.md` 记录多语言、共享 corpus、候选决策和缓存版本契约。
- [ ] 本次文件被独立暂存和提交，不夹带工作区其它任务改动；提交已推送到最新 `origin/develop`。
- [ ] GitHub Player Beta workflow 成功，Release 包含 Windows 安装包、标准 ZIP、便携 ZIP、校验文件，以及工作流当前配置的 Android ARM64 APK；Release 为 prerelease。

## Out of Scope

- 不让 Player 运行时依赖 Server，也不在前端复用 Go 二进制代码。
- 不重写 Emby/Jellyfin 自带识别或 ServerDataSource 的服务端元数据。
- 不复制 MoviePilot GPL 源码，不逆向或冒充 Emby 私有识别算法。
- 不在自动匹配正常流程增加人工候选选择页；人工搜索仍只用于失败恢复。
- 不在本任务新增新的元数据 provider、远程文件写回、重命名或整理功能。

## Release Authority

用户已明确授权：质量门通过后直接提交、推送并发布 Player Beta，不需要再次确认发布动作。Trellis 的实现阶段批准仍按流程要求在最终规划摘要之后单独取得。
