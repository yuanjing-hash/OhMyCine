# Player 下一代多语言识别器设计

## 1. Boundary

本任务只修改 Player 原始文件源的独立刮削链路：

```text
DataSource.list()
  → provider-neutral RawFileRecord
  → Unicode/path parser
  → bounded query variants
  → TMDB movie+tv recall
  → top candidate enrichment
  → deterministic rank/decision
  → local cache + category projection
```

UI、DataSource 协议和远端文件保持只读。Emby/Jellyfin/ServerDataSource 不进入该流程。

## 2. Parser Model

- 在 `services/scraper` 内增加纯 TypeScript 识别领域类型：canonical title、title variants、season/episode/year、source/reason、type evidence、diagnostics 和 engine version。
- 标题清洗从“中文/拉丁正则提取”改为 Unicode NFC + 状态化 token 清理。Unicode letters/numbers/marks 均是标题字符，具体脚本不作为允许名单。
- 技术规格、来源、字幕和发布组只作为有界独立 token/尾部 token 删除；方括号、圆括号和连字符默认保留，只有可证明为年份、TMDB hint 或技术结构时才处理。
- 季集 token pack 支持现有格式及常见语言标签。所有 ordinal 解析有上下界；中文数词支持 `零/〇/一…千/两/兩`，其它语言的结构数字先支持阿拉伯数字。
- 整个标题等于一个季集样式时保留标题表面；只有删除后仍存在 meaningful title 才将 token 从查询标题中移除。

## 3. Query Recall And Ranking

- file、parent、grandparent 各自产生 canonical 变体，按来源轮转优先；之后才加入去组、去规格、无年份等 fallback。
- 自动请求预算固定：最多 10 次搜索，覆盖偏好类型、跨类型、精确年份、`±1` 和无年份回退；同一请求去重。
- 聚合 TMDB summary，而不是每次搜索命中就立即返回。只对合并后排名靠前的最多 3 个身份调用详情；详情附带 `alternative_titles`、`translations`、`external_ids` 和 `images`。
- 排名使用标题/原标题/别名/翻译、年份、类型、季集结构、一致性、唯一性和弱流行度证据。候选顺序不参与分数。
- 输出 `matched | no_match | low_confidence | candidate_conflict`。只有 matched 才写入正式 metadata；其它状态映射到既有未识别体验。

## 4. Shared Corpus

- Server 的 `provider-neutral-v1` JSON 是跨实现契约。Player verification 读取同一文件，并将 provider-neutral path 转成 `RawFileRecord` fixture。
- Player 增加自身补充 corpus，覆盖 Latin diacritics、Cyrillic、Arabic、Thai 及常见多语言结构标签；这些用例只验证公开可观察行为，不声称外部产品内部实现。
- 生成/输出 Player 决策摘要，至少报告 parser accuracy、Top-1、冲突拒绝和请求预算。

## 5. Cache Compatibility

- 新增 `PLAYER_RECOGNITION_ENGINE_VERSION` 并写入自动识别结果/scan cache。
- 为 `RawScrapedMediaItem` 增加来源标记，区分 `automatic` 与 `manual`。所有新人工识别明确写 `manual`；旧记录不能可靠区分时采用保守迁移：已有 matched metadata/本地 override 保留，旧失败结果与无 metadata 的自动结果允许重算。
- 增量扫描发现 cache engine 过期时，复用安全的文件记录和扫描事实，重新 parse/enrich，不删除数据源配置、凭据、播放历史、图片/元数据手工覆盖。
- cache schema 采用向后兼容 sanitizer；不通过删除整个 app-data/profile 实现升级。

## 6. Security And Failure

- 识别输入只使用 root-relative provider path 和文件名；绝对本地路径、URL、凭据和签名参数不得进入 TMDB query/diagnostic。
- 单个 TMDB 搜索/详情失败降级为其它候选或未识别；取消/认证错误保持终止语义，不扩大重试域名和请求预算。
- 日志只记录安全标题摘要和理由码，不打印 upstream payload、Authorization 或 provider path。

## 7. Release Flow

1. 在当前工作区实现并运行完整 Player 门禁。
2. 只暂存本任务文件并创建 Conventional Commit（中文说明）。
3. fetch 远端，确保本次提交基于/合入最新 `origin/develop`；若远端前进，安全 rebase/merge 本任务提交，不触碰未提交的其它任务文件。
4. push `develop`，验证 `origin/develop` 精确指向本次发布提交。
5. 查询最新 semver tag/release，选取下一个未占用 patch Beta 版本。
6. 从 `develop` 手动触发 `player-beta-release.yml`，等待工作流完成并核对 prerelease 与资产。

若远端更新与本任务文件冲突、CI 失败或 release source guard 拒绝，则停止发布并修复；不得从 feature/local-only commit 绕过 guard。
