# Server 下一代媒体识别引擎技术设计

## 1. 设计目标

把当前“清洗若干标题后逐个调用 TMDB，并接受第一条结果”的过程改造成一个来源无关、结构感知、默认全自动、可解释且可回归测量的识别引擎。下载入库与媒体库扫描必须共用同一入口；正常流程不展示 Top-k 候选，也不等待人工确认。

本设计只借鉴 MoviePilot v3 和 Emby 公开资料中可观察的行为与架构思想，不复制 MoviePilot GPLv3 源码，不把旧版 Emby.Naming 当作当前闭源 Emby Server 的完整实现。

## 2. 模块边界

### 2.1 纯领域层：`server/internal/mediarecognition`

新增或扩展以下无网络、无数据库能力：

- `InputFacts`：包名、provider-relative 文件路径、文件大小、视频数量、目录层级、来源类别、Profile 预处理结果。
- `ParsedFacts`：标题片段、中英文标题、年份、季集范围、资源规格、来源平台、编码、字幕、发布组、光盘/多版本/extra 结构、类型证据。
- `QueryVariant`：保留来源、清洗阶段、标题、年份、建议类型和生成理由的有序查询候选。
- `Evidence` / `ScoreBreakdown`：标题、别名、年份、类型、季集、目录与唯一性证据，以及冲突惩罚。
- `Decision`：`matched` 或带理由码的 `unrecognized`；包含有界诊断，但不包含绝对路径、provider ID、凭据或 URL。

解析器按确定顺序运行：安全规范化 → 显式身份/Profile 词处理 → 目录与文件集合结构分析 → token 状态解析 → 多级查询候选生成。每一步保留事实和理由，禁止在唯一字符串上做不可追踪的破坏性清洗。

### 2.2 编排层：`server/internal/services`

`recognizeMedia` 继续作为下载与媒体库共享入口，但内部改为：

1. 构造有界 `InputFacts`。
2. 调用纯领域解析器生成结构事实和查询变体。
3. 显式 TMDB ID 存在时走 `GetByID` 并验证类型。
4. 否则调用候选召回接口收集完整候选集。
5. 调用统一排名器评分、自动消歧并作出单一决策。
6. 对匹配结果执行现有 classification；对失败结果保存准确理由和安全诊断。

现有调用方仍消费 `MediaRecognitionResult`，迁移期间追加诊断字段而不破坏已有字段。旧 `downloadSearchTitles` 可暂作兼容包装，最终只委托新解析器，避免两套规则漂移。

### 2.3 TMDB 适配层：`server/pkg/metadata/tmdb`

把 `Search` 的“首条即结果”职责拆分为候选召回与详情获取：

- 按电影、电视剧及未知类型执行有界搜索；未知类型可使用 multi-search 或等价的 movie/tv 并行召回。
- 查询顺序覆盖精确年份、`year + 1`、`year - 1`、必要时无年份回退；同一请求内去重。
- 搜索结果保留本地化标题、原标题、日期、类型、流行度等安全字段。
- 只对初排短名单请求详情、alternative titles 和 translations，限制外部请求放大。
- 详情结果按 TMDB ID 和语言缓存；网络错误、无候选和无效响应保持不同错误码。

旧 `Search` 保留为兼容入口，在新统一排名器稳定后改为调用候选召回并返回自动决策结果。`SearchCandidates` 不再服务正常入库交互，只可用于受权限保护的诊断或后续高级工具。

## 3. 解析规则设计

### 3.1 强结构证据

强证据优先于弱标题猜测：

- 显式 TMDB ID。
- `SxxExx`、`1x02`、日期集、Season/Specials 目录。
- 多个视频文件形成稳定集数序列，例如本例 49 个文件。
- BDMV、VIDEO_TS、disc/stack、多版本和 extra 目录结构。
- 分类规则或库上下文提供的媒体类型，但只作为证据而非无条件覆盖远端身份。

### 3.2 状态化 token 解析

解析器按 token 位置和状态区分标题与资源信息，至少覆盖：年份、季集、分辨率、来源、编码、音轨、字幕、版本、`HQ`、发布组。四位数字只有在满足年份上下文时才成为年份，确保 `Ming Dynasty in 1566`、`1917`、`1984`、`3 Body Problem` 不被误删。

尾部组识别支持 `-Group`、` -Group`、`- Group`、`[Group]`，但必须结合尾部位置、相邻规格 token、已知标题完整性与反例测试；不得把所有合法连字符尾词当作发布组。MoviePilot/Emby 词表只用于扩充测试维度，规则实现保持 OhMyCine 自有且可解释。

### 3.3 多语言规范化

统一使用 Unicode NFC、大小写折叠、空白和标点等价；保留原始展示值。搜索名称覆盖原始标题、中英文拆分、简繁等价及父目录标题。TMDB 侧比较本地化标题、原标题、alternative titles、translations。简繁能力若不能由现有依赖可靠提供，应通过可替换接口接入受维护的本地转换库，不调用外部文本服务。

## 4. 自动排名与决策

所有召回候选进入同一个确定性评分器，不再按查询顺序接受第一条。评分包含：

- 最佳标题/原标题/别名/译名相似度。
- 年份精确、`±1`、缺失与冲突。
- 候选类型与结构类型证据。
- 季集、Season 目录和文件集合一致性。
- 包名、父目录、主文件名多个来源是否相互印证。
- 候选唯一性和仅作为弱先验的流行度。
- 年份、类型、季集等强冲突惩罚。

排名器输出 Top-1、Top-2 分差和逐项分数，但这些只用于内部决策、诊断与 benchmark。自动匹配阈值、冲突阈值和分差阈值由冻结 corpus 校准。满足阈值即自动入库；未满足时返回 `tmdb_no_match`、`tmdb_low_confidence`、`tmdb_candidate_conflict` 或上游错误。系统不会弹出候选选择步骤。

## 5. 失败恢复与界面

### 5.1 已完成下载

下载完成但识别失败时，下载任务保持“数据已完成、入库待恢复”语义：

- 主动作显示“重新识别并入库”。
- 动作复用 manifest/115 云端文件和现有 Transfer → Import → Notify 管线。
- 不再次提交 115 离线下载，不复制 294.4 GiB 数据。
- 自动识别成功后继续既有入库流程，并由另一个通知任务负责 Emby/Jellyfin 与 Player 刷新。

### 5.2 高级纠错

正常流程没有候选列表。如果完整自动链仍失败，管理员可在人工介入区输入关键词发起 TMDB 搜索；页面展示有界的安全结果摘要，选择结果后自动回填媒体类型与 TMDB ID。管理员也可直接填写媒体类型与 TMDB ID。无论 ID 来自搜索结果还是手工输入，Server 都必须通过 `GetByID` 重新拉取并验证身份，浏览器提交的标题、分类、图片和年份均不可信。成功后可保存有作用域、可撤销的本地别名规则，但默认不自动扩大到所有来源。

下载任务和媒体库页面都应先提供“重新识别”；只有再次失败或用户主动展开时才显示“人工介入”。人工介入中的关键词搜索复用受权限保护的安全候选 API，并与当前失败记录的标题、年份和媒体类型预填信息解耦，允许用户修改关键词。搜索结果只是帮助获得显式身份的恢复工具，不参与正常自动识别，也不自动写入匹配；用户确认后才调用 `GetByID` 并继续入库。自动排名的内部 Top-k 诊断不作为完成任务所需操作。

## 6. 诊断与持久化

在现有识别记录上新增版本化诊断 JSON 或等价有界字段：

- 引擎版本、输入事实摘要、查询变体及生成理由。
- 远端候选安全摘要、评分分解、阈值和最终理由码。
- 最大候选数、字符串长度、诊断字节数和外部请求数上限。

迁移必须向后兼容；旧记录无诊断时仍可读取。诊断不得存绝对路径、115 文件 ID、cookie、API key、Authorization、签名 URL 或完整上游响应。数据库迁移遵循现有顺序并提供迁移回归测试。

## 7. Benchmark 与测试架构

在 Server 仓库中建立脱敏 fixture corpus 和离线 runner：

- Parser fixture 不需要网络。
- Retrieval/Ranking 使用假 TMDB 或冻结 cassette。
- 同一 corpus 运行 current baseline 与 candidate，生成 JSON/Markdown 差异报告。
- Top-k 仅衡量正确身份是否被召回；产品验收看 Top-1、误匹配率和未识别率。
- 每个新清洗规则必须同时有正样本和合法标题反例。

首个硬性样本为 `Ming Dynasty in 1566 HQ -BlackTV`：必须解析标题 `Ming Dynasty in 1566`，保留数字 1566，利用 49 集结构判定 TV，年份为 2007，并自动命中冻结的正确 TMDB 身份。

## 8. 兼容、发布与回滚

- 先以影子模式在测试/诊断中同时运行旧、新引擎，不产生双重写入。
- 冻结基线并校准阈值后，新引擎成为唯一决策者；旧入口只保留短期兼容包装。
- 识别诊断带 `engine_version`，缓存 key 纳入解析/排名版本，避免旧负缓存阻止新规则生效。
- 发布前对已有缓存和未识别记录进行非破坏性重算验证；不批量改写已人工固定的有效身份。
- 回滚时可切回旧决策器并忽略新增诊断字段；数据库新增字段保持向后可读，不做破坏性降级迁移。

## 9. 关键取舍

- 默认全自动，不以人工候选选择弥补算法不足。
- 宁可明确未识别，也不静默错配；但返回失败前必须穷尽有界的结构、多语言、年份和跨类型自动消歧。
- 显式 ID 是失败后的高级逃生通道，不是正常步骤。
- 先用 benchmark 证明“更强”，不以规则数量或宣传用语证明。
- 只借鉴公开行为和测试思想，保持许可证边界清晰。

## 10. 搜索、目录与失败任务重选目标

- Explore 保留每个站点自己的 `page/has_next/items/status`，顶部 tab 只切可见渠道；翻页替换当前页，不把第 N 页追加到第 1 页。
- 搜索排序由独立 `sort=seeders|published|size` 与 `direction=asc|desc` 组合驱动，默认 `seeders+desc`；缺失数值/非法日期始终稳定置后，站点、标题和 opaque token 提供不随方向反转的确定性兜底顺序。
- 预识别事实与最终 manifest 识别分别标注；自动匹配只有在 Server 通过可信详情接口验证后才写入 claim。
- 媒体库配置继续存 provider-relative 根；WebUI 额外组合 Storage 可读根与相对根，`/` 显示为“数据源根目录”，不改变安全路径契约。
- `PUT /api/v1/downloads/:id/import-target` 只接受失败 Transfer 的新媒体库 ID。服务在事务内锁定 DownloadTask、TransferTask 和 Job，验证没有部分写入/checkpoint/cleanup，再重建完整目标与 Profile 快照、清理旧计划并将原 Transfer Job 重新入队。已完成 manifest 和验证身份保持不变，不调用 downloader。
- qBittorrent 分类路由以 DownloadTask 的 `staging_absolute_path` 为唯一当前任务边界；legacy Storage-relative 快照只在 `load()` 成功解析后提升为内存绝对快照。发现同名分类的路径为空或指向旧暂存根时，调用 `editCategory`，随后重新读取并比较规范化路径，验证成功后才能 `setCategory → setLocation → resume`。

## 11. Player 独立识别同步

- Player 使用自己的 `player-nextgen-v3` 实现版本并声明共享 contract；Local/OpenList 等原始源消费同步后的通用解析事实和冻结语料。
- ServerDataSource、Emby/Jellyfin 继续消费权威 DTO，不进入 Player 本地 parser/TMDB/cache。
- Player cache identity 之外再比较数据源配置根；同一 source ID 修改物理根时先删除旧 raw scan cache，再创建新运行时源。

## 12. 敏感输入组件

- Server WebUI 与 Player 各自提供同契约 `SecretInput`，支持单行/多行、`configured` 安全布尔、默认遮挡和可访问的眼睛按钮。
- `configured && modelValue === ''` 只显示星号占位。用户输入替换值后，眼睛切换该表单内存值；用户主动请求旧值时，组件调用 `loadSecret`，把结果放入独立的短生命周期 `revealedValue`，不触发 `update:modelValue`，隐藏、切换对象、卸载或竞态失效时立即清除。
- Server 通过 `POST /api/v1/credentials/reveal` 按资源类型、资源 ID、字段三元组硬编码白名单读取；路由要求认证、CSRF、`connections.secrets.export` 和 `Cache-Control: no-store`，每次成功/失败均记录不含值的审计。普通 DTO 只返回逐字段 `configured` 布尔。
- Player 直接读取本机 provider-specific 安全凭据 envelope；`omc_player_*` Server 设备访问令牌、OhMyCine 密码和内置 TMDB 凭据没有 loader，不能回显。
- 静态 inventory 测试扫描全部 Vue 文件，拒绝原生 `type="password"` 和敏感 `v-model` 绕过共享组件。
