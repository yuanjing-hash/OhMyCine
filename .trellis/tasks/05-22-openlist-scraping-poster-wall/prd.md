# brainstorm: OpenList 刮削分类海报墙设计

## Goal

在已完成并验证的 OpenList/Alist DataSource 之上，设计 Player 侧的本地刮削、分类与海报墙体验。目标是让 OpenList/Alist 不再只是文件浏览器，而能把选中根目录内的视频识别成电影/剧集/季/集，补齐海报、背景、简介、年份、评分、类型等元数据，并在 Player 中以海报墙和媒体库视图呈现。

## What I already know

* OpenList/Alist 账号登录、根目录选择、目录浏览、搜索和播放已经通过本地 live test。
* OpenList/Alist 原始 API 主要提供文件/目录信息，不提供电影级元数据。
* 现有设计文档 `docs/architecture/03-player-design.md` 第 5 章提出了网盘自动刮削系统：文件列表 -> 文件名解析 -> TMDB 查询 -> 本地缓存 -> 海报墙展示。
* 现有 Player 已有 `MediaItem` / `MediaDetail` / `MediaLibrary` / `HomeSection` DataSource 抽象，以及 `MediaGrid` / `MediaCard` / `HeroCarousel` 等展示组件。
* 现有播放历史已经使用 Tauri SQLite；凭据也使用 Tauri app-data 下的 SQLite 凭据边界。刮削元数据可以复用“本地持久化优先，不写入远端服务”的方向。
* AI 推荐最终也会依赖本地媒体元数据，但本任务先聚焦 OpenList/Alist 海报墙，不把 AI 推荐放进 MVP。

## Assumptions (temporary)

* MVP 只针对 OpenList/Alist；CloudDrive2、本地文件可复用设计但不在本任务实现。
* TMDB 是第一优先级元数据源，用户需要在 Player 设置中提供自己的 TMDB API Key 或 Read Access Token。
* 元数据、匹配结果和海报/背景缓存都保存在本机 app data；不上传到 Server。
* 第一版允许用户手动触发扫描，不先做常驻后台自动扫描。
* 第一版以“只读扫描”为原则，不移动、重命名或删除 OpenList/Alist 文件。
* OpenList/Alist 根目录可能有两类形态：像 Emby/Jellyfin 推荐库目录一样整理良好的标准目录，或者所有影片/剧集混在一起的非标准目录。
* 标准目录模式需要支持“任意用户指定根目录下的合理媒体目录结构”，不要求根目录下一定叫 `movie` / `tv`、`Movies` / `TV`。
* 标准目录示例包括但不限于 `Movies/华语电影/片名 (年份)/片名.mkv`、`TV/综艺/剧名/Season 01/S01E01.mkv`、`TV/国产剧/剧名/Season 01/S01E01.mkv`；这些只是可识别形态之一，不是强制格式。
* MP 风格分类思想可抽象为 Player 的通用本地刮削分类规则，但该规则用于“刮削后如何在海报墙/媒体库中分组”，不用于强制要求网盘目录结构。

## Open Questions

* 标准目录模式的识别阈值如何定：按目录名、层级、文件命名、电影/剧集混杂度，还是提供用户确认？

## Requirements (evolving)

* 支持从已配置的 OpenList/Alist `extra.rootPath` 开始递归扫描视频文件。
* 支持两种扫描/刮削模式：
  * 标准目录模式：目录本身已经按电影、剧集、季集或 Emby 默认刮削习惯整理良好，优先使用目录结构推断媒体类型、标题、年份、季/集。
  * 非标准目录模式：文件混放、层级混乱或分类缺失，主要依赖文件名解析与 TMDB 慢速匹配。
* 支持从用户指定的 `extra.rootPath` 自动识别目录组织方式，并在置信度不足时允许用户确认/切换模式。
* 支持通用分类规则配置，作用于 OpenList/Alist、CloudDrive2、本地文件等“原始文件源”；Emby/Jellyfin 这类已由服务端整理和刮削的数据源不默认套用该规则。
* 分类规则采用类似 MP 的条件匹配思想，但不要求顶层固定目录名；规则可以在逻辑上分为电影/剧集两组，或用规则字段声明适用媒体类型。
  * 分类名是逻辑分类名，用于海报墙、筛选、媒体库分组和未来建议整理，例如 `华语电影`、`外语电影`、`综艺`、`国产剧`。
  * 分类条件通过受控设置页配置，不让用户直接手写 YAML/JSON。
  * 分类条件支持 TMDB 详情字段：`original_language`、`production_countries`、`origin_country`、`genre_ids`、`release_year`，以及后续可扩展的一级字段。
  * 多条件同时满足；同一条件多个值用逗号；`!value` 表示排除。
  * 未匹配到显式规则时允许落入空条件兜底分类，电影和剧集默认都落入 `未分类`。
  * 类型/题材选项应来自 TMDB 官方电影/剧集 genre 枚举；电影分类只能选择 movie genre，剧集分类只能选择 tv genre，例如剧集侧可以选择综艺/纪录片/儿童/动画等 TMDB TV 类型。
* 标准目录模式应从路径结构本身推断媒体类型、标题、年份、季集与可能的分类目录；不能因为根目录不是 `movie` / `tv` 就判为非标准。
* 非标准目录模式默认认为目录信息不可依赖，先按散文件处理；在 TMDB 匹配后使用同一套分类规则计算逻辑分类，用于海报墙筛选、媒体库分组和未来建议整理，但不写回网盘。
* 支持视频文件过滤：mkv、mp4、avi、mov、wmv、flv、webm、m4v、ts、rmvb 等。
* 支持文件名解析，提取标题、年份、分辨率、来源、视频编码、音频编码、制作组、季集号。
* 支持 TMDB 查询与匹配，并将结果保存到本地元数据库。
* 支持本地海报/背景图缓存，避免每次打开都重新请求远端图片。
* 支持扫描状态：未扫描、扫描中、已完成、部分失败、需要重新扫描。
* 支持本地扫描日志：记录扫描开始/结束、模式判定、文件数量、命中/未识别数量、TMDB 请求失败、跳过原因和错误摘要。
* 支持在 OpenList/Alist 数据源页从“文件夹视图”切换到“媒体库/海报墙视图”。
* 支持基础分类：电影、剧集、未识别，并按通用分类规则展示逻辑分类。
* 支持用户对错误匹配进行后续修正；MVP 可以先设计入口，不一定第一版实现完整手工匹配工作台。
* 不向 TMDB 或 AI provider 发送 OpenList/Alist 账号、token、完整内网 URL 或敏感播放 URL。
* 不对 OpenList/Alist 网盘做任何上传、写入、重命名、移动或删除操作；所有日志、缓存、匹配结果和图片均只保存在 Player 本地。

## Proposed Design (evolving)

### Product shape

OpenList/Alist 数据源保留两种入口：

* 文件夹视图：当前已经可用的原始目录浏览与播放，永远保留为兜底。
* 媒体库视图：基于本地扫描、刮削、分类和海报缓存生成的海报墙体验。

用户第一次进入媒体库视图时，如果该数据源尚未扫描，页面显示“开始扫描”主操作、扫描范围摘要（当前 `rootPath`）和 TMDB 配置状态。扫描完成后，默认进入海报墙；未识别文件可以从“未识别”或文件夹视图继续访问和播放。

### Scraping pipeline

```text
OpenList/Alist selected root
  -> recursive provider listing
  -> video-file filter
  -> path structure sampling
  -> auto detect standard / non-standard mode
  -> parse media candidates
  -> TMDB search + detail enrichment
  -> local metadata/artwork/log cache
  -> logical classification rules
  -> poster wall / media detail / playback
```

扫描只读 OpenList/Alist。所有可变状态都在 Player 本地：扫描任务状态、路径解析结果、TMDB 匹配结果、用户修正、海报/背景图缓存、分类结果和日志。

### Standard mode detection

标准模式不是固定目录名，而是对用户选中根目录下面的结构做评分。可用信号包括：

* 目录层级：`分类/片名 (年份)/视频文件`、`剧名/Season 01/S01E01`、`分类/剧名/Season 01/S01E01`。
* 文件命名：标题年份、`S01E01`、`第01集`、`Season 01`、多集连续编号。
* 聚合关系：同一剧名目录下有多季/多集，单个电影目录下通常只有 1 个主视频和少量字幕/花絮。
* 分类提示：`华语电影`、`外语电影`、`综艺`、`国产剧`、`动漫` 等目录名只能作为提示，不能作为强制条件。
* 混杂度：同一目录同时出现大量互不相关标题时降低标准模式置信度。

检测结果建议输出：

* `mode`: `standard` / `nonStandard`
* `confidence`: 0-1
* `reasons`: 用于本地日志和用户确认文案
* `samples`: 参与判断的少量路径样例

当置信度低时，扫描不失败；先按推荐模式执行，并在日志/设置里允许用户切换后重新扫描。

### Non-standard mode

非标准模式默认目录信息不可靠，把视频文件作为散文件候选处理：

* 从文件名提取标题、年份、季、集、分辨率、片源、编码、音频、字幕/制作组等。
* 对剧集文件，按解析出的标题 + season/episode 进行本地聚合。
* 对无法解析标题的文件，保留为 `unresolved`，仍可播放。
* 后续用户修正匹配时，修正结果写入本地 override 表，不写回 OpenList/Alist。

### Classification rules

分类规则是刮削后的逻辑分组，不是物理目录约束。它影响：

* 海报墙中的分类分区和筛选。
* 媒体库侧栏/标签。
* 聚合首页的分组来源。
* 后续 AI 推荐可以使用的本地分类上下文。
* 未来 Server/整理功能的“建议目标”，但本任务不执行整理。

第一版不提供自由手写 YAML/JSON，也不做容易误配的自由式可视化规则编辑器。分类配置做成一个专门的“刮削与分类”设置页，用受控控件生成规则：

* 页面分为“电影分类”和“剧集分类”两组。
* 每组标题右侧有 `+`，点击后添加一个分类。
* 新分类需要填写分类名，例如 `华语电影`、`动画电影`、`综艺`、`国产剧`。
* 分类条件通过控件选择：
  * 类型/题材：多选，来自 TMDB 官方 `movie` 或 `tv` genre 列表；电影分类不可选择剧集 genre，剧集分类不可选择电影 genre。
  * 原始语种：多选，来自 TMDB/ISO 语种列表。
  * 国家/地区：多选，电影使用 `production_countries`，剧集使用 `origin_country`。
  * 年份范围：起止年份输入或范围控件。
  * 排除条件：每个多选条件支持“包含/排除”切换，内部仍可表达 `!value`，但不把这个语法暴露给普通用户。
* 分类顺序可调整；匹配时从上到下命中第一个满足条件的分类。
* 每组保留一个兜底分类，电影和剧集默认都为 `未分类`，兜底分类不能删除但可以改名。
* 高级导入/导出可以后置；MVP 内部可保存为结构化 JSON，而不是保存用户手写文本。

默认规则仍参考用户给出的 MP 思路，但在 UI 中呈现为受控选项：例如“动画电影”选择电影 genre=Animation，“外语电影”只作为电影侧显式可编辑默认分类，“综艺”选择 TV genre=Reality/Talk 等官方剧集类型，“国产剧”选择 origin country=CN/TW/HK。

默认兜底分类与空/历史配置兜底都统一为 `未分类`；电影和剧集各自的兜底都不能落到 `外语电影`。

这里的“电影分类 / 剧集分类”只是规则适用的媒体类型域，不是要求网盘目录顶层必须这么命名。

内部规则结构建议：

```json
{
  "version": 1,
  "groups": [
    {
      "mediaType": "movie",
      "categories": [
        {
          "name": "动画电影",
          "conditions": {
            "genreIds": { "include": [16], "exclude": [] },
            "originalLanguages": { "include": [], "exclude": [] },
            "productionCountries": { "include": [], "exclude": [] },
            "releaseYear": null
          }
        }
      ],
      "fallbackCategoryName": "未分类"
    },
    {
      "mediaType": "tv",
      "categories": [
        {
          "name": "综艺",
          "conditions": {
            "genreIds": { "include": [10764, 10767], "exclude": [] },
            "originCountries": { "include": [], "exclude": [] },
            "releaseYear": null
          }
        }
      ],
      "fallbackCategoryName": "未分类"
    }
  ]
}
```

### Local data model

第一版可以用本地 SQLite 表表达这些概念：

* `scrape_sources`: sourceId、rootPath、ruleSetVersion、lastScanStatus、lastScanAt、detectedMode、confidence。
* `scrape_items`: sourceId、providerPath、fileName、fileSize、modifiedAt、mediaType、seriesKey、seasonNumber、episodeNumber、parseStatus、matchStatus。
* `scrape_metadata`: item/series identity、tmdbId、title、originalTitle、year、overview、rating、genreIds、languages、countries、runtime、posterPath、backdropPath、scrapedAt。
* `scrape_categories`: item/series identity、categoryName、matchedRuleName、ruleSetVersion。
* `scrape_overrides`: user correction, forced TMDB id, forced media type, ignored file, custom title/year。
* `scrape_logs`: sourceId、scanId、level、stage、message、safePath/sample、createdAt。

存储时只保存 provider path 和必要的 sourceId，不保存账号、密码、token、签名播放 URL 或完整敏感 provider URL。海报和背景图保存在 app data 的 source-scoped cache 目录。

### UI flow

OpenList/Alist 数据源页：

* 顶部提供“媒体库 / 文件夹”切换。
* 媒体库视图支持扫描状态、重新扫描、查看日志、分类筛选、电影/剧集/未识别筛选。
* 文件夹视图继续走当前 DataSource `list()`，不依赖刮削成功。
* 海报卡点击进入详情页；电影直接播放主文件，剧集进入季/集列表后播放具体集。

设置页：

* 保持“管理数据源”作为设置子页。
* 增加“刮削与分类”设置项，管理 TMDB 凭据、默认语言地区、默认扫描模式、分类规则。
* 分类规则在专门设置页中通过受控控件编辑：电影分类与剧集分类分组展示，每组支持 `+` 添加分类、选择 TMDB 官方类型/题材、语种、国家/地区、年份范围和排除条件。
* 不让用户直接编辑 YAML/JSON；结构化导入/导出可以后置。

### Implementation slice proposal

建议后续实现分成四个小步：

1. 本地刮削基础设施：SQLite schema、扫描任务状态、日志、视频文件递归扫描、只读安全边界。
2. 结构识别与解析：标准/非标准检测、文件名解析、电影/剧集候选聚合、未识别兜底。
3. TMDB 与分类：TMDB 配置、搜索/详情、海报缓存、默认分类规则和规则执行。
4. UI 接入：OpenList/Alist 媒体库视图、海报墙、分类筛选、详情页剧集列表、扫描日志入口。

## Acceptance Criteria (evolving)

* [ ] 用户能在 OpenList/Alist 数据源上手动触发扫描。
* [ ] 扫描后能看到电影海报墙，卡片优先展示海报、标题、年份、评分。
* [ ] 对整理良好的标准目录，扫描能使用目录结构快速识别电影/剧集，而不是把所有文件都当作散文件慢刮。
* [ ] 对混乱的非标准目录，扫描能退回文件名解析 + TMDB 匹配，并保留未识别结果。
* [ ] 用户能在设置里通过受控分类设置页查看/编辑通用刮削分类规则；规则只影响原始文件源，不影响 Emby/Jellyfin。
* [ ] 电影分类添加时只展示 TMDB 官方电影 genre，剧集分类添加时只展示 TMDB 官方剧集 genre；普通用户不需要手写 YAML/JSON。
* [ ] 标准目录 `Movies/华语电影/片名 (年份)/片名.mkv`、`TV/综艺/剧名/Season 01/S01E01.mkv` 以及没有固定 `Movies/TV` 顶层名但层级合理的目录，都能被识别为对应媒体类型和逻辑分类。
* [ ] 未识别文件不会丢失，能在“未识别/文件夹视图”继续浏览和播放。
* [ ] 点击海报能进入媒体详情页并播放原 OpenList/Alist 文件。
* [ ] 本地配置/数据库不保存 OpenList/Alist token、账号密码或 tokenized stream URL。
* [ ] 扫描日志和刮削缓存只存在本地，不对 OpenList/Alist 做任何写操作。
* [ ] 扫描失败或 TMDB 不可用时，Player 仍能保持文件浏览和播放可用。

## Definition of Done (team quality bar)

* PRD 收敛到可实现 MVP。
* 需要实现时补齐 `implement.jsonl` / `check.jsonl`。
* 前端变更通过 typecheck、lint、build。
* Player UI 变更后跑 Windows 打包。
* 设计状态同步到 `docs/architecture/06-roadmap.md`。

## Out of Scope (explicit)

* 不做 Server 侧刮削或 Server 侧媒体库。
* 不做文件重命名、转移、删除或目录整理。
* 不把分类规则执行结果写回 OpenList/Alist 目录结构；规则只影响 Player 本地识别、分组、海报墙和未来建议整理。
* 不做 PT 下载、STRM、302 代理。
* 不把 AI 推荐作为本任务 MVP。
* 不要求第一版支持所有网盘，只先服务 OpenList/Alist。

## Technical Notes

* Design source: `docs/architecture/03-player-design.md` section 5.
* Roadmap hooks: `docs/architecture/06-roadmap.md` Phase 1.3 DataSourceManager 完善、Phase 3 Cinema OS UI 中库内海报墙相关项。
* Relevant Player files: `player/src/services/datasource/alist.ts`, `player/src/services/datasource/types.ts`, `player/src/views/SourceLibraryView.vue`, `player/src/views/MediaDetailView.vue`, `player/src/components/media/*`.
* Existing persistence examples: playback history SQLite commands and credential SQLite boundary.
* Classification rule reference: user-provided MP-style condition matching, but expose it through controlled settings UI instead of direct YAML/JSON editing; avoid requiring physical top-level `movie` / `tv` directories.
