# Research: MoviePilot 多语言、错拼与系列副标题召回

- Query: 对比 MoviePilot v2/v3 与 OhMyCine，解释 `迪迦·奥特曼 1080p`、`ULRAMAN+TIGA` 和 `The Final Odyssey` 的漏识别，并给出不复制 GPL 源码的通用方案。
- Scope: mixed
- Date: 2026-08-25

## Findings

### 1. 当前失败主要发生在“远端召回之前”，不是最终字符串比较

- OhMyCine 的 `comparisonKey` 已将 Unicode 标点和空白视为等价，仅保留字母和数字；只要正确的《迪迦奥特曼》候选已经进入集合，`迪迦·奥特曼` 与 `迪迦奥特曼` 应是精确归一化匹配（`server/internal/mediarecognition/normalize.go:40-50`）。
- 但 TMDB 请求仍直接使用解析出的展示标题（`server/internal/services/media_recognition.go:457-490`）；这个排序阶段的归一化不会改写上游查询。故应为检索生成有界的标点变体，例如同时尝试 `迪迦·奥特曼`、`迪迦奥特曼`，而不是只在候选返回以后归一化。
- `ULRAMAN+TIGA` 是真实错拼，当前测试只覆盖了拼写正确的 `ULTRAMAN+TIGA`（`server/internal/mediarecognition/parser_test.go:183-188`）。MoviePilot 的 TMDB 名称比较本质上也是“去特殊字符后的精确相等”，不能凭空修正错拼（`.tmp/mp-ref-6.py:116-132`）。它若表现更好，通常来自副标题、站点分类、父目录、已知搜索身份或辅助识别源，而不是一条万能错拼正则。
- 安全的错拼召回应是：原查询失败后，有限尝试规范化 token 窗口/辨识度高的 token（本例可由 `TIGA` 召回候选），再用完整标题编辑相似度、年份、媒体类型和唯一性统一排名。不能写死 `ULRAMAN -> ULTRAMAN`，也不能把任意 `TIGA` 直接认成电视剧。

### 2. `The Final Odyssey` 的现有测试比真实 TMDB 搜索响应理想化

- 当前排名器会把候选中的冒号副标题作为别名，因此候选标题若已经是 `Ultraman Tiga: The Final Odyssey`，`The Final Odyssey` 可以命中（`server/internal/mediarecognition/ranker.go:279-305`）。
- 现有服务测试正是直接伪造了这个英文候选标题（`server/internal/services/media_recognition_test.go:299-305`）。这没有覆盖 `language=zh-CN` 下搜索摘要只给本地化 `title` 与 `original_title`、而英文译名/别名只在详情接口中的真实形态。
- OhMyCine 的 `SearchCandidates` 每种类型最多读取前十条搜索摘要，摘要阶段只保存标题与原标题（`server/pkg/metadata/tmdb/client.go:1056-1115`）；alternative titles / translations 要到详情 enrich 才出现（`server/pkg/metadata/tmdb/candidate_enrichment.go:51-67,128-147`）。
- enrich 又只处理 3 个候选，并先按“每个查询结果页的顺序”轮询挑选，而不是保证先挑对系列副标题最相近的候选（`server/internal/services/media_recognition.go:321-334,412-453`）。因此目标即使在 TMDB 结果中，也可能在补全英文译名前被预算淘汰。这是 `The Final Odyssey` 仍低置信的核心结构风险。
- 通用修复是“召回池与详情预算分层”：先用搜索摘要、站点副标题和搜索上下文给候选做轻量预排序；对最可能包含系列副标题的候选 enrich；然后把本地化标题、原标题、alternative titles、translations 全部放回统一排名。不要给该电影写专属别名字典。

### 3. OhMyCine Explore 正在丢失 MoviePilot 会使用的站点上下文

- OhMyCine 搜索结果已经包含 `Subtitle`（`server/internal/services/site.go:124-135,657-662`），搜索请求也已有 `MediaType`、`SearchBy` 和可验证的 `TMDBID`（`server/internal/services/site.go:117-122,569-588`）。
- 但结果 claim 只保存 `ActorID/SiteID/TorrentID/Title`（`server/internal/services/site.go:60-64`），签发时也只写 Title（`server/internal/services/site.go:657-659`）；快速识别最终只把 `claim.Title` 送进解析器和共享识别器（`server/internal/services/site.go:668-708`）。副标题、查询媒体类型和精确搜索身份全部丢失。
- MoviePilot v2/v3 的站点搜索链会把 `torrent.title` 与 `torrent.description` 一起传给 `MetaInfo`；v2 公开代码见 `app/chain/search.py:600,919,958,1236,1534`，v3 见 `app/chain/search.py:1105,1363,1917`。其种子链还会在站点分类明确为 TV 时补充 TV 类型（v3 `app/chain/torrents.py:619-624`）。
- MoviePilot 的副标题并不会通用地“猜一个系列前缀”。公开解析器主要用它补季集，并在标题被判断为拼音时尝试从中文描述恢复中文名（`.tmp/mp-ref-3.py:194-211,219-241`）。文件识别则显式融合文件名、父目录和上上级目录（`.tmp/mp-ref-0.py:446-476`）。
- 因而 `The Final Odyssey` 不应无条件拼成 `Ultraman Tiga: The Final Odyssey`。合理规则是：
  - 按 TMDB ID 发起的精确搜索：claim 保存已由 Server `GetByID` 验证的身份，结果卡直接继承该身份，无需重新猜。
  - 普通标题搜索：搜索关键词、站点副标题和站点媒体类型仅作为带来源的弱/中等证据及额外查询变体；它们可帮助召回，但不能覆盖发行名或直接确定身份。
  - 本地/下载完成识别：继续使用父目录、manifest 文件集合等结构事实，不依赖站点专属逻辑。

### 4. MoviePilot 可借鉴的行为边界（v2/v3 一致）

- 搜索摘要先比本地化标题和原标题，失败后才拉详情比较 alternative titles 与 translations（v2 `app/modules/themoviedb/tmdbapi.py:329-421,543-583`；本地 v3 快照 `.tmp/mp-ref-6.py:329-421,543-583`）。
- 类型未知时可走 multi-search；电影、TV 和年份 `0/+1/-1` 有有界回退（`.tmp/mp-ref-6.py:147-155,281-327,543-583`）。
- 标题/副标题、路径父级、站点分类、精确搜索身份是不同强度的事实；没有公开证据表明 MoviePilot 会对所有“副标题电影”盲目添加 franchise prefix。
- GPLv3 源码不能复制。可复用的是上述分层行为、输入事实模型、调用次序和测试维度；OhMyCine 应保持自己的 Go 实现与确定性评分器。

### 5. 建议实现顺序与回归语料

1. 扩展 actor-bound claim，私有保存有界 `subtitle`、查询媒体类型、搜索方式，以及仅在 TMDB-ID 搜索时保存已验证身份；公开 DTO 仍只返回安全摘要。
2. 共享解析输入增加“辅助标题事实”，但保留来源和权重；不要字符串拼接后破坏主标题。
3. 检索层增加标点删除/标点空格两类变体；只在原召回不足时增加有界 typo-recall token 查询。
4. enrich shortlist 改由初步相关度与跨类型公平性共同决定，不再让每个查询的第一条天然占满三个名额。
5. 使用接近真实 `zh-CN` TMDB 响应的冻结 cassette，而不是直接给 fake candidate 填最终英文别名。

必测正例：

- `迪迦·奥特曼 1080p` → TV《迪迦奥特曼》。
- `ULRAMAN+TIGA 1996 BluRay ...` → 在错拼、年份和唯一候选共同成立时命中 TV；没有 1996/副标题/站点类型时允许保持低置信。
- `The Final Odyssey 1080p WEB-DL ...` + 站点副标题含完整中英文作品名 → 电影《迪迦奥特曼：最终圣战》。
- 同一发行名通过 TMDB-ID 精确搜索产生时直接沿用已验证 movie 身份。

必测反例：

- `The Odyssey`、`Final Destination`、只有 `Odyssey`，不得被拼到迪迦系列。
- `TIGA` 单词同时召回剧集、剧场版和外传时，没有年份/类型/副标题优势必须拒绝自动匹配。
- `迪迦·奥特曼：最终圣战` 不得因标点删除被误认成 TV 正剧。
- 普通标题搜索关键词不得被当作所有结果的强制身份；只有已验证的 TMDB-ID 搜索可继承确定身份。

## Files Found

- `server/internal/services/site.go` — Explore 搜索、claim 与快速识别，当前只把发行标题送入识别。
- `server/internal/services/media_recognition.go` — TMDB 查询预算、跨类型召回、enrich shortlist 与最终排名编排。
- `server/internal/mediarecognition/normalize.go` — Unicode/标点比较归一化，只作用于本地评分。
- `server/internal/mediarecognition/ranker.go` — alternative/translation 与系列副标题排名。
- `server/pkg/metadata/tmdb/client.go` — 搜索摘要边界与前十条限制。
- `server/pkg/metadata/tmdb/candidate_enrichment.go` — 最多三个候选的详情补全。
- `.tmp/mp-ref-0.py`、`.tmp/mp-ref-3.py`、`.tmp/mp-ref-6.py` — MoviePilot v3 忽略目录研究快照；分别覆盖父目录融合、主副标题解析、TMDB 匹配。

## External References

- MoviePilot v2/v3 repository: <https://github.com/jxxghp/MoviePilot>
- v3 search chain: <https://github.com/jxxghp/MoviePilot/blob/v3/app/chain/search.py>
- v3 torrent chain: <https://github.com/jxxghp/MoviePilot/blob/v3/app/chain/torrents.py>
- v3 TMDB matcher: <https://github.com/jxxghp/MoviePilot/blob/v3/app/modules/themoviedb/tmdbapi.py>
- TMDB search API contract: <https://developer.themoviedb.org/reference/search-movie> and <https://developer.themoviedb.org/reference/search-tv>
- TMDB movie/TV details append contract: <https://developer.themoviedb.org/reference/movie-details> and <https://developer.themoviedb.org/reference/tv-series-details>

## Related Specs

- `.trellis/spec/backend/media-classification-profiles.md` — provider-neutral parsing、内置词包、完整发行名与正反例要求。
- `.trellis/spec/backend/pt-discovery.md` — actor-bound opaque result claim 与 PT 搜索边界。
- `.trellis/spec/guides/cross-layer-thinking-guide.md` — 完整真实输入、跨层事实保真与 PT/Nyaa 双层回归。

## Caveats / Not Found

- 当前环境没有可用的 TMDB API 凭据，且未对实时 TMDB 结果做网络抓取；因此不能把某次具体搜索排序或某个 alternative-title 字段内容写成稳定事实。上述 TMDB 结论基于公开 API 字段契约、OhMyCine 的实际反序列化边界和用户截图。实现时应录制脱敏 cassette 固定真实 `zh-CN` 响应。
- MoviePilot v2/v3 的公开流程证明了副标题、站点分类、路径父级和已知身份会参与，但没有证明其原生识别器能在无上下文时稳定修复 `ULRAMAN` 错拼，也没有发现通用 franchise-prefix 注入逻辑。
