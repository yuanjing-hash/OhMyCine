# MoviePilot v3 识别方案研究

## 研究基线

- 仓库：<https://github.com/jxxghp/MoviePilot>
- 分支：`v3`
- 研究提交：`415335b21502076bcfe4c98be63a67953d35feac`（2026-08-24）
- 许可证：GPLv3。OhMyCine 只借鉴行为、分层、测试维度和公开 API 用法，不复制源码或大段正则。

## 标题解析

核心位于 `app/domain/meta/`：

- `MetaVideo` 按 token 顺序解析标题、年份、季、集、来源、分辨率、效果、流媒体平台、视频/音频编码、位深、帧率和发布组。
- 它同时保留 `cn_name`、`en_name`、`original_name` 和原始标题，避免单个破坏性清洗字符串成为唯一事实。
- `HQ` 被明确归入资源效果，因此本例在遇到 `HQ` 时会停止收集英文标题，得到 `Ming Dynasty in 1566`；即使 `BlackTV` 不在发布组表中，也不再污染查询标题。
- 四位数字先作为待定 token：在标题之后、合理年份范围内才成为年份；这类状态化解析比全局正则替换更能保护 `Ming Dynasty in 1566` 等数字标题。
- `MetaAnime` 使用动漫专用解析路径，支持括号标题、中英/罗马音、绝对集数、季集和字幕组语义。
- `WordsMatcher` 提供用户自定义替换、直接身份和集数偏移；`ReleaseGroupsMatcher` 合并内置与自定义发布组。
- `MetaInfoPath` 会在文件名质量差时利用父目录，但有条件地避免分类目录覆盖真实标题。

## TMDB 召回与验证

核心位于 `app/modules/themoviedb/tmdbapi.py` 和 `app/modules/themoviedb/__init__.py`：

- 搜索名称按中文、简体转换、英文标题顺序去重尝试。
- 类型未知且无年份时使用 multi-search；电影未命中时会回退 TV，之后再回退 multi-search。
- 电影/电视剧分别比较本地化标题与原标题。
- 第一轮未匹配后获取详情，把 alternative titles 与 translations 汇成 `names` 再匹配。
- 名称比较忽略大小写与标点。
- 年份搜索按 `year`、`year+1`、`year-1` 尝试；TV 还能用季号、季首播年份与 episode group 辅助。
- 显式 TMDB ID 在类型未知时分别查询 movie/tv，并结合标题、年份和类型提示消歧。

## 缓存与扩展

- 本地识别 cache 以解析后的 meta 为键，既缓存成功也缓存失败。
- 识别链允许插件补充其他 provider，但必须返回规范远端身份。
- v3 还具有共享识别结果上报/查询；这是召回增强，但涉及隐私、信任与错误传播，OhMyCine 首版不应依赖公共共享库。
- Python 解析器之外存在 Rust accelerator，说明解析热路径和大量规则匹配需要性能边界与一致性测试。

## 自动识别与失败纠错的交互定位

- `GET /media/recognize` 根据标题构造元数据，调用 `MediaChain.async_recognize_by_meta`，直接返回单一 `Context`；未命中则返回空结果。默认识别 API 不返回 Top-k 候选供人工确认。
- 自动整理与重新整理主链调用 `recognize_by_path` / `recognize_media` 并直接消费识别结果；正常入库没有“先展示候选、等待用户选择”的步骤。
- 前端 `CacheReidentifyDialog.vue` 的纠错输入是“媒体数据源 + 可选原生 ID”，不是候选列表。测试明确验证：不填写 ID 时两个身份字段均不提交，含义是重新运行自动识别；填写 ID 时才按显式身份重识别。
- 因此，MoviePilot 的产品模型是“默认自动决策；失败后允许再次自动识别或由高级用户显式指定 ID”，不是“低置信时让用户从 Top-k 中选择”。OhMyCine 保留显式 ID 作为灾备入口，并可在失败后的人工介入区提供关键词搜索来帮助获得 ID，但不能把候选选择塞回正常识别主链来掩盖自动识别不足。

## 值得借鉴

- 中英文/简繁多搜索名。
- 标题/原标题第一轮，别名/译名第二轮。
- 年份容差、类型 multi-search、跨类型回退。
- 状态化 token 解析，独立动漫路径，父目录与文件名融合。
- 显式 ID、用户识别词、缓存和插件边界。
- 自动识别主链与失败后显式 ID 纠错的清晰分离。

## 不应照搬

- 仅靠发布组大表会持续追赶新组名；OhMyCine 应加入位置/结构启发式和可解释候选。
- MoviePilot 的许多匹配仍以精确等价为主，不是完整的多证据排序模型。
- 公共共享识别库可能放大错误并泄露标题使用信息，不适合作为本地自托管产品的默认依赖。
- GPLv3 源码不能直接复制到未确认兼容许可策略的实现中。
- 不应虚构 MoviePilot 具有默认候选人工确认步骤，也不应据此给 OhMyCine 增加正常流程的人工作业。

## 公开参考

- [TMDB 匹配实现](https://github.com/jxxghp/MoviePilot/blob/v3/app/modules/themoviedb/tmdbapi.py)
- [TMDB 识别编排](https://github.com/jxxghp/MoviePilot/blob/v3/app/modules/themoviedb/__init__.py)
- [媒体标题入口](https://github.com/jxxghp/MoviePilot/blob/v3/app/domain/metainfo.py)
- [视频标题解析](https://github.com/jxxghp/MoviePilot/blob/v3/app/domain/meta/metavideo.py)
- [动漫标题解析](https://github.com/jxxghp/MoviePilot/blob/v3/app/domain/meta/metaanime.py)
- [识别 API](https://github.com/jxxghp/MoviePilot/blob/v3/app/api/endpoints/media.py)
- [识别链](https://github.com/jxxghp/MoviePilot/blob/v3/app/chain/_recognition.py)
- [重新识别弹窗](https://github.com/jxxghp/MoviePilot-Frontend/blob/v3/src/components/dialog/CacheReidentifyDialog.vue)
- [重新识别弹窗测试](https://github.com/jxxghp/MoviePilot-Frontend/blob/v3/src/components/dialog/__tests__/CacheReidentifyDialog.spec.ts)
