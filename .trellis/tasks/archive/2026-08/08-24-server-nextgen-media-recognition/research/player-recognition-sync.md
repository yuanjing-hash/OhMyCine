# Research: Player 媒体识别契约同步与独立运行边界

- Query: 调查 Player 现有本地/OpenList/ServerDataSource 媒体识别、文件名解析、TMDB 查询与缓存架构；设计 Server Go `internal/mediarecognition` 与 Player TS/Rust 间的契约、fixture、规则版本和词包同步方式，避免两套算法静默漂移，同时保持 Player 可离线独立运行。
- Scope: mixed（仓库内部实现为主，辅以 MoviePilot、Go WebAssembly、JSON Schema 与浏览器 Worker 的公开资料）
- Date: 2026-08-25

## Findings

### 1. Files Found

| 文件 | 作用 |
| --- | --- |
| `player/src/services/scraper/types.ts` | Player 原始文件事实、路径提示、识别候选、TMDB 匹配结果和本地扫描缓存 DTO。 |
| `player/src/services/scraper/parser.ts` | Player 当前独立运行的文件名/路径解析器；从文件、父目录和季目录提取标题、年份、季集。 |
| `player/src/services/scraper/pathRecognition.ts` | 显式记录 file → parent → grandparent 的路径合并次序。 |
| `player/src/services/scraper/recognition.ts` | Player 的多标题候选、TMDB 查询规划、候选评分与拒绝阈值；当前版本为 `player-nextgen-v2`。 |
| `player/src/services/scraper/tmdb.ts` | Player 独立 TMDB 凭据、查询、详情/别名/译名获取、图片与网络 fallback。 |
| `player/src/services/scraper/metadataEnrichment.ts` | 将本地候选分组后调用 TMDB，写入自动匹配、分集元数据和分类结果。 |
| `player/src/services/scraper/localScanCache.ts` | 只读全量/增量扫描、识别缓存、人工结果保留、引擎版本失效和安全反序列化。 |
| `player/src/services/datasource/local.ts` | 本地文件 DataSource；优先展示 Player 本地扫描缓存，未扫描时回退文件浏览。 |
| `player/src/services/datasource/alist.ts` | OpenList/Alist DataSource；与本地源共用 Player 本地扫描/刮削层。 |
| `player/src/services/datasource/server.ts` | ServerDataSource；直接消费 Server 已识别的媒体库、作品、季集、演员和播放版本 DTO。 |
| `player/src-tauri/src/commands/raw_scan_cache.rs` | 以 `source_type + source_id + root_path` 哈希为键，将扫描 JSON 存入 Player SQLite。 |
| `player/scripts/verify-nextgen-recognition.ts` | 已开始读取 Server 冻结 corpus，对 Player 的候选决策做跨语言离线回归。 |
| `player/scripts/verify-scraper-title-classification.ts` | Player 刮削、分类、人工识别、引擎版本失效和分集元数据的综合验证脚本。 |
| `server/internal/mediarecognition/domain_types.go` | Server 完整 provider-neutral 输入/解析/排名契约；字段明显比 Player 当前 DTO 丰富。 |
| `server/internal/mediarecognition/parser.go` | Server 当前 `nextgen-domain-v8` 纯领域解析器。 |
| `server/internal/mediarecognition/packs.go` | `tv-v1`、`anime-v1` 固定顺序、来源 commit、规则数和嵌入资源。 |
| `server/internal/mediarecognition/wordprocessor.go` | Server 内置/自定义词处理器，使用 `regexp2` 兼容规则语法并施加输入、匹配、总时长和应用次数上限。 |
| `server/internal/mediarecognition/snapshots/` | 322 条固定内置规则、来源清单和 MoviePilot-Help 许可证。 |
| `server/internal/mediarecognition/testdata/corpus.v1.json` | Server 与 Player 已共同消费的 provider-neutral 冻结语料。 |

### 2. Player 当前识别数据流

#### 2.1 本地文件与 OpenList/Alist

本地源和 OpenList/Alist 都是“原始文件源”，应由 Player 自己识别，以保证没有 Server 时仍可浏览、刮削和播放：

```text
DataSource.list()
  → RawFileRecord（provider-relative）
  → 结构检测 + parseRawMediaCandidate
  → 候选按作品/剧集分组
  → Player 自己查询 TMDB
  → RawScrapedMediaItem
  → SQLite raw_scan_cache
  → 海报墙 / 详情 / 播放
```

证据：

- `RawFileRecord` 只保存 `rootPath/providerPath/relativePath/parentPath/fileName` 等 provider-relative 事实，见 `player/src/services/scraper/types.ts:34`。
- `RawMediaCandidate` 当前保存 `kind/title/year/seriesTitle/seasonNumber/episodeNumber/categoryHint/confidence/signals`，见 `player/src/services/scraper/types.ts:93`。
- Player 解析器按路径提取标题与季集，见 `player/src/services/scraper/parser.ts:143`、`player/src/services/scraper/parser.ts:218`。
- 路径候选明确按 file、parent、grandparent 合并，见 `player/src/services/scraper/pathRecognition.ts:18`。
- 全量扫描先收集只读快照，再调用 TMDB enrichment，并在完成后持久化，见 `player/src/services/scraper/localScanCache.ts:84`。
- 增量扫描发现文件变化或识别引擎版本漂移时升级为全量重算；人工识别结果保留，见 `player/src/services/scraper/localScanCache.ts:146`、`player/src/services/scraper/localScanCache.ts:322`、`player/src/services/scraper/localScanCache.ts:328`。
- `LocalFileDataSource` 与 `AlistDataSource` 均优先从本地 raw scan cache 构造列表、详情与主页栏目，见 `player/src/services/datasource/local.ts:92`、`player/src/services/datasource/local.ts:155`、`player/src/services/datasource/local.ts:185`，以及 `player/src/services/datasource/alist.ts:174`、`player/src/services/datasource/alist.ts:213`、`player/src/services/datasource/alist.ts:243`。

#### 2.2 TMDB 查询与本地缓存

- Player 支持用户安全存储的 API Key/Read Access Token，以及构建时注入的内置 token；用户凭据优先且类型必须匹配，见 `player/src/services/scraper/tmdb.ts:158`、`player/src/services/scraper/tmdb.ts:167`、`player/src/services/scraper/tmdb.ts:172`。
- 自动识别最多规划 10 次搜索、最多 enrich 3 个详情，见 `player/src/services/scraper/recognition.ts:7` 和 `player/src/services/scraper/recognition.ts:8`。
- 查询覆盖首选/备选媒体类型、精确年份、`±1` 与无年份回退，见 `player/src/services/scraper/recognition.ts:102`。
- TMDB 详情请求已附带 `alternative_titles` 和 `translations`，见 `player/src/services/scraper/tmdb.ts:307`；最终决定再次基于详情别名排名，见 `player/src/services/scraper/tmdb.ts:225`。
- `RawScrapedMediaItem` 缓存 TMDB metadata、episode metadata、匹配来源和 `recognitionEngineVersion`，见 `player/src/services/scraper/types.ts:119`。
- Tauri 用 SQLite 保存最多 50 MiB 的有界 JSON payload，缓存身份是 `source_type + source_id + root_path` 的 SHA-256，见 `player/src-tauri/src/commands/raw_scan_cache.rs:7`、`player/src-tauri/src/commands/raw_scan_cache.rs:97`、`player/src-tauri/src/commands/raw_scan_cache.rs:144`。

#### 2.3 ServerDataSource 不属于 Player 本地识别链

ServerDataSource 已经正确处于另一条边界：

```text
Server 已识别数据库事实
  → /api/v1/player/* DTO
  → ServerDataSource 严格解析
  → Player 展示/播放
```

- `listLibraries()` 直接消费 Server 的物理/在线媒体库，见 `player/src/services/datasource/server.ts:231`。
- 物理库导航由 Server categories/catalog/detail API 提供，见 `player/src/services/datasource/server.ts:247`。
- 详情直接映射 Server 的 genres/directors/writers/cast/people/TMDB ID/剧照/播放版本，见 `player/src/services/datasource/server.ts:388`。
- `parseItem` 要求 Server 返回有效 `work_identity`，只接受 `tmdb|server` scheme 和 `movie|series` 类型，见 `player/src/services/datasource/server.ts:1007`。

因此 ServerDataSource 内容不得再次进入 `parseRawMediaCandidate`、Player TMDB 搜索或 `raw_scan_cache`。否则会出现：Server 已人工修正的身份被 Player 覆盖；同一媒体被重复请求 TMDB；Server 分类与 Player 分类冲突；Server 更新后 Player 仍显示自己的旧识别。Player 对 Server DTO 只做边界校验、展示缓存和播放目标合并。

### 3. 当前已有的跨语言同步基础与不足

#### 已有基础

`player/scripts/verify-nextgen-recognition.ts:48` 已直接读取 `server/internal/mediarecognition/testdata/corpus.v1.json`。脚本验证：

- corpus 版本；
- provider 返回顺序变化时决定稳定；
- must-match 的 TMDB identity；
- must-reject 不会静默匹配；
- 多语言路径与季集；
- 请求和详情预算。

这是正确方向：Player 无需运行 Server，也能使用同一脱敏 corpus 做离线一致性测试。

#### 关键不足

1. **只同步了一小段“最终决定”，没有同步完整解析契约。**
   - Server `ParsedFacts` 包含 canonical title、多来源 titles、season year、episode range/count、specifications、release group、structure、type evidence、带 reason/order 的 queries 和 diagnostics，见 `server/internal/mediarecognition/domain_types.go:76`。
   - Player `RawMediaCandidate` 只有单集 `episodeNumber`、少量 signals 和一个 confidence，见 `player/src/services/scraper/types.ts:93`。
   - 当前 Player fixture adapter 只从 corpus 任选一个文件，见 `player/scripts/verify-nextgen-recognition.ts:245`；因此无法验证多文件集数分布、BDMV/VIDEO_TS、extras、发布组、规格和完整查询顺序。

2. **实现版本没有映射关系。**
   - Server 是 `nextgen-domain-v8`，见 `server/internal/mediarecognition/domain_types.go:7`。
   - Player 是 `player-nextgen-v2`，见 `player/src/services/scraper/recognition.ts:6`。
   - 两者独立递增不是错误，但当前没有共同 `contract_version`、`pack_bundle_version` 或 CI 兼容矩阵，无法知道 `player-nextgen-v2` 是否完整实现 `nextgen-domain-v8` 的行为集合。

3. **Player 没有执行现有内置词包。**
   - Server 默认固定 `tv-v1 → anime-v1`，规则数 28 + 294，见 `server/internal/mediarecognition/packs.go:9`、`server/internal/mediarecognition/packs.go:26`、`server/internal/mediarecognition/packs.go:46`。
   - Player 当前依靠 `parser.ts` 中硬编码技术 token、发布组白名单和季集表达；仓库内没有 Player 端 PackCode/PreparedName/AppliedRule 实现。

4. **不能把 Server 的 Go 文件机械复制成 TS。**
   - Go 词处理器使用 `github.com/dlclark/regexp2`，包含匹配超时、总超时、最大匹配数和最大应用规则数，见 `server/internal/mediarecognition/wordprocessor.go:19`、`server/internal/mediarecognition/wordprocessor.go:29`、`server/internal/mediarecognition/wordprocessor.go:91`。
   - 内置词包实际含 `(?<=...)`、`(?=.*...)`、inline `(?i)`、替换 `\1` 以及项目自定义 `=> / && / <> / >>` 语法。浏览器 JavaScript RegExp 的 flags、replacement 和超时语义并不等同；直接 `new RegExp` 会造成规则编译失败、替换错误或主线程 ReDoS。

5. **Player 本地源还有一个邻近缓存风险。**
   - 本地文件 DataSource 对 provider 的根恒为 `/`，见 `player/src/services/datasource/local.ts:42`、`player/src/services/datasource/local.ts:248`。
   - `updateConfig` 会重建 DataSource 和失效展示快照，但没有清除 raw scan cache，见 `player/src/stores/datasource.ts:177`。
   - 因而同一个 source ID 修改物理本地根目录时，raw cache identity 仍可能是 `local + sourceId + /`，旧媒体索引可能被读回。删除源时才显式清理 raw cache，见 `player/src/stores/datasource.ts:228`。这不是 Server 媒体库目录修改 bug 的直接证明，但属于 Player 端相同类别，实施同步时应补回归。

### 4. 推荐同步模型：共享契约与黄金输出，而不是手抄算法

#### 4.1 三个版本必须分开

建议每次识别缓存与测试报告都保存以下三元组：

```json
{
  "contract_version": "media-recognition-contract-v2",
  "implementation_version": "player-nextgen-v3",
  "pack_bundle_version": "tv-v1+anime-v1@sha256:<digest>"
}
```

- `contract_version`：Go/TS/Rust 必须共同理解的字段、理由码、排序与安全边界；只有 schema 变化才升级。
- `implementation_version`：Server 与 Player 各自递增，用于各自缓存失效；不要求字符串相等。
- `pack_bundle_version`：词包 code、执行顺序、固定来源 commit、文件 SHA-256 与解释器语义版本的组合；任一变化都必须使自动识别缓存失效。

不能只强行令 Player 常量等于 `nextgen-domain-v8`：同名会掩盖实现并未等价的问题。

#### 4.2 Go 产出黄金输出，Player 必须消费完整事实

Server Go 领域层继续作为当前行为的参考实现，但公共真相应是语言无关文件：

```text
provider-neutral input fixture
  → Go 参考实现
  → expected ParsedFacts + Decision golden JSON
  → Go test 与 Player verify 同时消费
```

每条 fixture 至少冻结：

- 安全输入：package name、source kind、所有 provider-relative files、类型/年份/季集 hint、prepared names；
- 解析事实：canonical title、多阶段 title facts、year/season/season year、episode min/max/count、specifications、release group、structure、suggested type/type confidence；
- 查询计划：title/year/type/source/reason/order；
- 候选与最终决定：identity、confidence、runner-up gap、reason；
- 诊断 code，不比较面向人的 summary 文案；
- `must_match` / `must_reject` 策略。

Player 不应像当前 adapter 那样只选一条文件构造候选。它必须接收完整文件集合并产生同结构结果；UI 仍可把结果映射回现有 `RawMediaCandidate/RawScrapedMediaItem`。

#### 4.3 内置词包使用“一个上游快照 + 机械同步 + 语义一致性门禁”

本轮不应在 TS 中再复制 322 行文本。建议：

1. 继续以现有 Server snapshots、`sources.json` 和许可证为唯一上游快照。
2. 增加同步脚本，把 byte-identical 快照、来源清单和许可证机械复制到 Player generated assets；提交生成物以保证 Player 源码和发布构建不需要联网。
3. 增加 `--check` 模式，在 CI 中比较 SHA-256、规则数、固定执行顺序和许可证，任何手工修改都失败。
4. Player 端词处理器只能运行已固定的 built-in packs；任意用户自定义回溯正则仍留在 Server，除非未来单独设计沙箱权限和安全预算。
5. Player 兼容解释器必须输出 `PreparedName { value, source, appliedRules }`，并把 direct TMDB hint 仅作为查询指导；最终仍调用 TMDB `GetByID` 验证，不能把词包中的标题/图片当权威 metadata。
6. 任一规则在 Player 端无法编译时必须使验证/构建失败，禁止静默跳过后继续标记同一 pack version。

#### 4.4 RegExp 安全与精确同构的现实取舍

本轮最实用方案是 Player Web Worker 中运行“固定内置词包兼容器”：

- Worker 与主 UI 隔离；整体超时后 `terminate()`，避免灾难性回溯冻结播放器。
- 输入长度、总时间、最大匹配数、最大 applied rules 对齐 Go `DefaultLimits`。
- 兼容层显式翻译 inline flags 和 replacement group 语法，并以词包 golden fixture 验证。
- Worker 只加载打包的可信快照，不加载网络规则或用户任意正则。

若验收要求**每个未来规则在 Server 与 Player 上字节级、语义级完全一致**，应升级为“同一 Go 领域核心编译为 WebAssembly，在 Player Worker 中运行”。这可真正消除 parser/wordprocessor/ranker 双实现，但会引入 Go WASM 体积、`wasm_exec.js` 生命周期、Vite/Tauri CSP、Android WebView 兼容和 Player 构建时 Go 依赖，不能在未做体积/平台 spike 前混入本轮。当前阶段应以完整黄金输出 parity 作为可量化门禁，而不是宣称两套源码天然等价。

### 5. 本轮可交付文件

以下是可独立验收的最小交付，不要求 Server 进程参与 Player 运行：

#### 共享/Server 测试资产

- `server/internal/mediarecognition/testdata/contract.v2.schema.json`
  - 定义 InputFacts、PreparedName、ParsedFacts、QueryVariant、Evidence、Decision 和版本三元组。
- `server/internal/mediarecognition/testdata/golden.v2.json`
  - 从现有 corpus 扩展为完整输入与 Go 黄金输出，不只保存期望标题/ID。
- `server/internal/mediarecognition/testdata/wordpack-golden.v1.json`
  - 覆盖默认包顺序、direct hint、replace、block、offset、combined 和合法标题反例。
- `server/internal/mediarecognition/contract_test.go`
  - 校验 schema、黄金输出可重复、字段有界、无绝对路径/URL/provider ID。

#### Player 运行时代码

- `player/src/services/scraper/recognitionContract.ts`
  - 与 schema 对齐的只读 TS 类型和安全反序列化；不复用 ServerDataSource DTO。
- `player/src/services/scraper/wordpacks/generated/{tv.txt,anime.txt,sources.json,LICENSE.MoviePilot-Help}`
  - 机械生成、byte-identical 的离线资产。
- `player/src/services/scraper/wordpacks/worker.ts`
  - 固定词包解释器 Worker 与超时终止边界。
- `player/src/services/scraper/wordpacks/client.ts`
  - 输入有界化、版本三元组、Worker 生命周期与 safe failure；失败时降级为未应用词包，不伪装成功版本。
- `player/src/services/scraper/parser.ts`
  - 改为消费完整文件集合/PreparedNames，并产出契约字段；现有单文件 API 可保留兼容 wrapper。
- `player/src/services/scraper/recognition.ts`
  - 排名输入/理由码对齐 contract；实现版本独立但声明支持的 contract version。
- `player/src/services/scraper/types.ts`
  - 为 episode range/count、season year、specifications、release group、structure、diagnostic codes 和版本三元组补字段。
- `player/src/services/scraper/localScanCache.ts`
  - 缓存失效从单一 implementation version 改为三元组；人工结果仍保留。
- `player/src/stores/datasource.ts` 与 `player/src-tauri/src/commands/raw_scan_cache.rs`
  - 在本地 source 物理根变化时清旧 cache，或为 cache identity 加不暴露绝对路径的 root revision/hash。

#### 生成与验证脚本

- `player/scripts/sync-recognition-assets.mjs`
  - `sync` 与 `--check`；同步词包、来源、许可证和 hash manifest，不联网。
- `player/scripts/verify-recognition-contract-parity.ts`
  - 消费完整 `golden.v2.json`，逐字段比较 ParsedFacts、queries、reason、identity 和版本。
- `player/scripts/verify-wordpack-parity.ts`
  - 校验 322 条规则、默认顺序、hash、编译失败为硬错误、golden 行为一致。
- `player/scripts/verify-server-datasource.ts`
  - 增加断言：Server item 不调用 Player parser/TMDB/raw cache；Server work identity、人工修正和 episode metadata 原样映射。
- `player/scripts/verify-raw-scan-cache-storage.ts`
  - 增加本地 root 修改同 source ID 不读取旧媒体的回归。
- `player/package.json`
  - 增加 `sync:recognition-assets`、`verify:recognition-contract`、`verify:wordpack-parity`。

### 6. 本轮测试矩阵

1. **跨语言契约**：同一 input files 集合下，Go 与 Player 的 canonical title、year、season、episode range/count、suggested type、query order 和 decision reason 一致。
2. **真实命名**：继续覆盖银色子弹 E1210、黑ネズミ E1210、M21/M28、Doomdos E1266、`第118-2集`、Nyaa 动漫、拼音/英文/日文/韩文。
3. **反例**：`Scary Movie`、`[REC]`、`1917`、标题内合法 `-Group` 字样、跨类型同名、同类型同年同名必须拒绝或保留。
4. **集合结构**：完整 49 集、只下载第 6 集、S02 complete、多集范围、BDMV/VIDEO_TS、extras 与多版本。
5. **词包**：默认 322 条、TV → anime 顺序、direct hint 必须 GetByID、无法编译规则 hard fail、Worker timeout 可恢复。
6. **缓存**：implementation/contract/pack 任一版本改变只重算自动结果；人工确认不被覆盖；本地物理根修改不复用旧 cache。
7. **DataSource 边界**：Local/OpenList 进入 Player 识别；ServerDataSource、Emby/Jellyfin 权威 metadata 不进入 Player 识别。
8. **离线性**：所有 parity 测试使用冻结 TMDB 候选和详情，不访问公网；Player 发布后不需要 Server daemon。
9. **安全性**：fixture、diagnostics、cache 不出现盘符绝对路径、URL query、cookie/token/provider file ID；字符串、文件数、候选数与日志大小受限。

### 7. 明确边界

- **本轮应同步**：Provider-neutral 解析事实、查询计划、排名理由、默认内置词包、版本/cache 失效语义和已知真实 corpus。
- **本轮不让 Player 复用**：Server Profile 的用户自定义正则、数据库别名学习、下载任务状态、115 provider ID、Server 的分类/转移规则。
- **Player 本地识别适用**：local、OpenList/Alist、CloudDrive2、WebDAV、123、Quark 等原始文件源。
- **Player 不重新识别**：ServerDataSource、直连 Emby/Jellyfin 已发布的权威作品元数据。Server 中同一媒体即使来自 115/STRM，在 Player 看来仍是 Server DTO，而不是一个需要再次解析的原始文件名。
- **人工结果**：Player 本地人工识别归 Player 本地 cache；Server 人工识别归 Server。两者未来若同步，必须同步规范 identity 和 provenance，不能同步绝对路径或直接覆盖另一侧。

## External References

- MoviePilot v2 meta architecture: `https://github.com/jxxghp/MoviePilot/tree/v2/app/core/meta`；公开结构将普通影视与动漫识别分开，并保留结构化字段。仅参考可观察架构，不能复制 GPL 源码。
- MoviePilot `MetaAnime`: `https://github.com/jxxghp/MoviePilot/blob/v2/app/core/meta/metaanime.py`；用于核对动漫预处理/解析阶段边界。
- Anitopy: `https://github.com/igorcmoura/anitopy`；说明成熟动漫解析器输出多个结构化字段，而不是只返回清洗标题。
- GuessIt: `https://github.com/guessit-io/guessit`；说明跨语言发行名识别通常以结构化 property contract 和 fixture 驱动。
- JSON Schema Draft 2020-12: `https://json-schema.org/draft/2020-12`；适合定义 Go/TS 共同消费的版本化 JSON 契约。
- Go WebAssembly: `https://go.dev/wiki/WebAssembly`；仅作为未来“同一 Go 核心在 Player Worker 运行”的技术候选，不是本轮既定实现。
- MDN Web Workers `terminate()`: `https://developer.mozilla.org/docs/Web/API/Worker/terminate`；用于隔离并终止固定词包兼容器的超时执行。

## Related Specs

- `.trellis/spec/frontend/server-online-media.md`：Server 在线/物理媒体库由 ServerDataSource 消费统一 DTO，Player 不硬编码 provider。
- `.trellis/spec/frontend/state-management.md`：跨页面持久状态、缓存失效和来源隔离。
- `.trellis/spec/frontend/type-safety.md`：所有外部 DTO 在边界处收窄，避免 `any` 和盲目信任。
- `.trellis/spec/guides/cross-layer-thinking-guide.md`：跨 Go/TS/Rust 字段必须检查生产者、传输、消费者与缓存。
- `.trellis/spec/backend/pt-discovery.md`：站点识别、claim 与人工 override 的安全事实边界。
- `docs/architecture/03-player-design.md`：Player 独立优先、原始数据源本地刮削与 ServerDataSource 职责。
- `.trellis/tasks/08-24-server-nextgen-media-recognition/prd.md`：R1、R2、R5、R8 与 AC8 要求统一 provider-neutral 输入和同一行为集合。

## Caveats / Not Found

- 当前共享 corpus 已经存在，但 Player 只验证了约化后的单文件候选和最终 identity；不能把这视为 Server v8 与 Player v2 已完全同步。
- Player 端尚未找到内置 `tv-v1/anime-v1` 词包解释器或 pack version；“322 条仍在 Server”不等于“Player 已使用 322 条”。
- 原始词包使用的正则/替换语义不是 JavaScript RegExp 的直接子集；未完成兼容性 fixture 和 Worker timeout 前，不应直接导入运行。
- Go WASM 能最大程度消除实现漂移，但尚未验证 Player 的 Windows/Android WebView、CSP、包体积、启动时延和 release workflow；本研究不把它列为本轮默认路径。
- Player 本地 cache 的物理根修改风险是静态代码审查发现，未在本研究中运行破坏性复现；实现时应先添加回归再决定清理旧键还是引入 root revision。
- 本研究不修改业务代码，也不判断用户截图中的 Server 媒体库目录保存 bug 根因；那需要在 Server Library update → persisted snapshot → transfer planner 另一条链路单独验证。
