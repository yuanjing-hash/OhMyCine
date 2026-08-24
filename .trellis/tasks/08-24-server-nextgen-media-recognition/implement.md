# Server 下一代媒体识别引擎执行计划

## 实施顺序

### 1. 冻结基线与测试语料

- [x] 建立 provider-neutral 脱敏 fixture schema、加载器与报告格式。
- [x] 收录当前已知成功、失败和负样本，首先固定 `Ming Dynasty in 1566 HQ -BlackTV` 及 49 集结构事实。
- [x] 为当前引擎生成可重复 baseline，记录 Top-1、Top-k 召回、误匹配、未识别、请求数和耗时。
- [x] 基于 MoviePilot 可公开观察行为与 Emby 官方命名规范补充 corpus 覆盖对照；报告明确这不是 MoviePilot、Emby 或历史 Server 的真实实测，并标注资料与许可证边界。

验证门：baseline 在无公网环境可重复运行，fixture 不包含绝对路径、provider ID、凭据或 URL。

### 2. 实现结构化解析器

- [x] 在 `internal/mediarecognition` 定义输入事实、解析事实、查询变体、证据和诊断类型。
- [x] 实现有界输入规范化、目录/文件集合分析、强弱类型证据和 extra/disc/stack 识别。
- [x] 实现状态化 token 解析，覆盖年份、季集、`HQ`、资源规格、来源、编码、字幕、版本和尾部发布组。
- [x] 实现原始、去组、去规格、去季集、去年份、中英文拆分、父目录等多级候选生成，保留每个候选理由。
- [x] 将 Profile 识别词和显式 ID 处理接入新结构，保持既有安全限制和优先级。
- [x] 为每条规则加入表驱动正例与反例，特别保护数字标题和合法连字符标题。

验证门：Parser fixture 全部通过，本例输出 `Ming Dynasty in 1566`、TV、2007，并保留 49 集证据。

### 3. 重构 TMDB 候选召回

- [x] 增加候选集合接口，支持 movie/tv、未知类型 multi-search 或等价并行召回。
- [x] 实现精确年份、`±1` 和无年份的有界回退与请求去重。
- [x] 搜索结果纳入本地化标题和原标题；短名单详情纳入 alternative titles、translations 和季信息。
- [ ] 为候选详情、失败和版本化识别结果补充合理缓存，限制每次识别的最大外部请求数。
- [ ] 用假 Server/cassette 覆盖超时、限流、无结果、无效响应和部分详情失败。

验证门：正确身份进入冻结 corpus 的 Top-k，且请求数不超过设计上限。

### 4. 实现统一排名与自动决策

- [x] 实现 Unicode/标点/空白/大小写规范化与可替换的简繁等价层。
- [x] 实现标题/别名、年份、类型、季集、目录集合、一致性、唯一性和弱流行度评分。
- [x] 实现强冲突惩罚、Top-1/Top-2 分差与可解释理由码。
- [x] 使用冻结 corpus 校准自动匹配、低置信和冲突阈值，禁止硬编码未经数据验证的 `.80`。
- [x] 输出单一自动决策；不新增 Top-k 人工选择流程。

验证门：候选顺序变化不改变相同证据下的结果；负样本不会静默入库；本例自动命中正确 TMDB ID。

### 5. 接入共享识别入口

- [x] 重构 `recognizeMedia` 使用新解析、召回和排名接口。
- [x] 下载 manifest 与媒体库扫描继续共用该入口，等价输入输出相同身份和理由码。
- [ ] 将旧 `downloadSearchTitles` / `Search` 收敛为兼容包装，清除首条结果短路行为。
- [x] 识别 cache key 纳入引擎版本，避免旧负缓存遮蔽修复。
- [x] 保持 classification、snapshot、NFO/海报和 Transfer/Import 下游契约兼容。

验证门：现有下载、媒体库、classification 和人工 ID override 回归全部通过。

### 6. 增加安全诊断与迁移

- [ ] 设计并实现版本化、有大小上限的识别诊断持久化。
- [ ] 增加数据库迁移及旧记录兼容测试。
- [ ] 在 API 中返回安全的失败原因和评分摘要，不返回绝对路径、provider ID、凭据、URL 或完整 TMDB 响应。
- [ ] 添加恶意超长名称、超多文件和诊断放大测试。

验证门：敏感字段扫描通过，旧数据库迁移后可读取，回滚版本可忽略新字段。

### 7. 修复 115 已完成任务的恢复语义

- [x] 区分下载失败与下载完成后识别/入库失败的 UI 状态。
- [x] 对后者提供“重新识别并入库”，复用 manifest 与云端数据，禁止重新提交 115 离线下载。
- [x] 自动重识别仍失败时提供折叠的“人工介入”：可输入关键词搜索 TMDB，选择结果自动回填媒体类型与 TMDB ID，也可直接填写 ID。
- [x] 搜索候选只返回有界安全摘要；用户确认后必须经 `GetByID` 验证身份再继续入库，不能信任浏览器回传的标题、年份、分类或图片。
- [x] 正常流程不显示候选选择；失败后的主动关键词搜索与自动排名内部 Top-k 严格分离。
- [ ] 覆盖成功、重复点击、并发恢复、源数据失效、权限不足和下游 Notify 失败。

验证门：294.4 GiB 已完成样本的恢复路径不会产生新下载任务，成功后只继续 Transfer → Import → Notify。

### 8. 文档、全量验证与发布门

- [x] 更新 OpenAPI（若相关契约存在）、Server 架构、识别规范、安全说明和管理员操作文档。
- [x] 运行 parser、TMDB、services、database、handlers 和 WebUI 定向测试。
- [x] 运行 Server 全量 Go 测试、WebUI 测试、typecheck/lint 和静态检查。
- [x] 生成新旧引擎 benchmark 差异报告，确认 Top-1 提升且误匹配率不超过批准阈值。
- [ ] 进行影子模式/离线重算验证，确认不会覆盖有效人工身份。

发布门：PRD 的 AC1–AC9 全部有测试或报告证据，且默认入库路径全自动。

## 2026-08-24 验证快照

- `go test ./... -count=1`：通过。
- `go vet ./...`：通过（使用仓库内隔离 `GOCACHE`，避免 Windows 沙箱访问用户级缓存）。
- `go test ./internal/mediarecognition -run 'TestEmbeddedBenchmark|TestBenchmark' -count=1`：通过；冻结报告保持字节级可重复。
- `go test ./internal/services -run 'TestDownloadRecognitionOverride|TestRecognizeMedia|TestPan115MediaLibraryScan|TestMediaLibraryRecognition' -count=1`：通过。
- Server WebUI：23 个测试文件、114 个测试通过；permissions check、Vue typecheck、ESLint、Vite production build 通过。
- `git diff --check`：通过，仅有既存的 Git 行尾转换提示。
- 当前任务仍为 `in_progress`：候选/失败缓存、诊断持久化、旧兼容入口收敛、源失效与 Notify 失败恢复、真实历史可比 baseline、影子模式仍未完成，不计入本轮完成范围。

### “斗罗大陆”中文季集回归复核

- 截图形态 fixture `斗罗大陆 - - 第1集/第2集` 已加入 parser 与共享 `recognizeMedia` 回归：中文 `第N集/话`、`第N季` 作为结构事实提取，自动查询标题为 `斗罗大陆`，合法整标题《第八集》《第2季》《第二十条》保持不变。
- 有限 TMDB 查询预算改为优先覆盖 filename/parent/package 不同来源的 canonical 变体，避免同一脏文件名的回退变体挤掉干净父目录标题；`EngineVersion=nextgen-domain-v2` 使旧 `unrecognized` 负缓存自动失效。
- Player 媒体库契约新增 `work_count`，保留 `entry_count`：5 个物理文件、4 个 distinct `work_key` 返回 `entry_count=5`、`work_count=4`；新 Player 优先显示作品数，连接旧 Server 时回退文件数。
- `go test ./internal/mediarecognition ./internal/medialibrary -count=1`：通过。
- `go test ./internal/services -run 'TestRecognizeMediaMatchesChineseEpisodeNamesFromLibraryScan|TestDomainRecognitionSearchQueriesPrioritizeCanonicalVariantsAcrossSources|TestPlayerCategoriesFollowProfileOrderAndFilterCatalog' -count=1`：通过；隔离 `TEMP/TMP/GOCACHE` 后 `internal/services` 全量也通过。
- Player `verify-server-datasource.ts`、Vue typecheck、ESLint、Vite production build：通过；构建仅保留既有的大 chunk 警告。
- 当前脏工作区的 `go test ./... -count=1` / `go vet ./...` 未通过，阻塞来自并行未完成的 CookieCloud 路由引用缺少 handler，以及 v43 已存在但旧迁移测试仍期待 v42；识别、媒体库、services、TMDB、Emby/Jellyfin 等已运行包均通过。不得将本次快照表述为 Server 全量门禁通过。
- `git diff --check`：通过，仅有既存行尾转换提示。任务保持 `in_progress`，未 commit、push 或归档。

## 建议验证命令

```powershell
cd server
go test ./internal/mediarecognition ./pkg/metadata/tmdb ./internal/services ./internal/database ./internal/handlers
go test ./...
golangci-lint run
cd webui
npm run test
npm run typecheck
npm run lint
```

实际脚本名以 `server/webui/package.json` 为准；执行前先检查可用命令，不假定不存在的脚本。

## 高风险点与回滚点

- 解析规则回归：每批 token/发布组规则单独提交并绑定反例；失败时可回退该批规则。
- TMDB 请求放大：详情 enrich 只对短名单执行，并记录每次请求数；超过预算直接停止 enrich。
- 阈值误配：阈值变更必须附 corpus 报告；保留上一版本配置以便回滚。
- 缓存污染：cache key 带引擎版本；不复用旧负缓存。
- 数据库兼容：只新增可空/默认安全字段，不删除旧字段；回滚不需要降级迁移。
- 恢复任务重复执行：使用现有持久任务幂等/租约机制，禁止重复创建下载任务。

## 开始实现前检查

- [x] 用户批准本次最终规划摘要。
- [x] 运行 `task.py start` 将任务状态切换为 `in_progress`。
- [x] 读取 backend、web-admin、database、quality 和 security 相关 Trellis specs。
- [x] 确认当前 52 项未提交改动的所有权，避免覆盖其他任务正在修改的文件。
- [x] 确认 Player/Emby 通知任务的接口边界，仅复用其 Notify 契约，不在本任务重复实现通知功能。
