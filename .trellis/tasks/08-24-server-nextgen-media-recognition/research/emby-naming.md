# Emby 命名与识别边界研究

## 资料边界

当前 Emby Server 的完整识别实现不是公开源码。本研究使用：

- 当前官方文档：[TV Naming](https://emby.media/support/articles/TV-Naming.html)、[Movie Naming](https://emby.media/support/articles/Movie-Naming.html)
- 公开仓库：[MediaBrowser/Emby.Naming](https://github.com/MediaBrowser/Emby.Naming)
- 研究提交：`c2a57762c9cd8413cc9e57f14c3159744f4e2eba`（2019-05-16）
- Emby.Naming 许可证：MIT

公开代码年代较旧，适合验证命名解析思想，不足以推断当前 Emby Server 的远端候选排序能力。

## 当前官方命名契约

- 电影推荐 `MovieName (year)` 文件夹和同名媒体文件；文件夹是识别的重要结构信号。
- TV 推荐 `Series (year) / Season # / Series SxxExx`；每部剧应有独立目录，年份用于区分重启/同名剧。
- 支持 `[tmdbid=...]`、`[tmdb-...]`、`{tmdbid=...}`、`{tmdb-...}`，并支持 TMDB/TVDB/IMDb 身份。
- 支持大量 SxxExx、1x02、102、日期剧集、多集文件、Specials/Season 0 形式。
- BDMV 与 VIDEO_TS 目录结构是强类型事实。
- 多版本媒体要求文件名以前置文件夹名开头，` - ` 后是版本显示名；不能把所有尾部连字符文本一律视为发布组。
- extras、trailers、interviews、deleted scenes 等目录和后缀有独立语义，不能进入主作品识别。

## Emby.Naming 公开实现

- `NamingOptions` 把日期、清洗词、季集、堆叠、extra、3D、stub 等正则放在可配置对象中，而不是散落在业务流程。
- `VideoResolver` 先识别容器、stub、3D、extra，再清理名称与年份，输出结构化 `VideoFileInfo`。
- `CleanDateTimeParser` 与 `CleanStringParser` 分离；当直接年份解析失败时会先清理规格词再尝试年份，体现多阶段回退。
- `EpisodePathParser` 按有序表达式尝试命名季集、日期剧集、绝对集数和乐观规则，并用边界阻止把 `1920x1080` 当成 S1920E1080。
- `VideoListResolver` 在集合层识别多段文件、多版本与 extras，而不是只看一个“最大文件名”。

## 值得借鉴

- 把文件集合和目录结构作为一等输入。
- 显式区分强规则与乐观规则，强规则优先，弱规则只能作为辅助证据。
- 先解析结构类型/extra/stack，再清理作品标题。
- 配置化规则、顺序确定、表驱动测试。
- 支持显式 provider ID，作为用户可控的无歧义逃生通道。

## 不应照搬

- 旧 `CleanStrings` 词表过时，且“首次命中后截断全部尾部”的方式容易误删合法标题。
- 旧年份范围只覆盖到 2019，证明硬编码时间边界会自然腐化。
- Emby 强依赖用户遵循规范目录；OhMyCine 处理 115/PT 原始资源时必须对脏命名更宽容。
- Emby 的库内容类型通常由用户预先指定；OhMyCine 仍需在未知类型下做可靠的多证据判定。

## 对 OhMyCine 的直接启示

本例的 49 集文件集合本身就是强 TV 证据；包名应在 `HQ` 处截断作品标题，同时把 `-BlackTV` 作为低风险尾部发布组候选。识别器应保留两条解释：`HQ` 被资源规格规则剥离，`BlackTV` 被尾部组形态规则剥离；如果缺少这些规则，49 集结构仍应触发 TV multi-search，而不是失败后要求重下。
