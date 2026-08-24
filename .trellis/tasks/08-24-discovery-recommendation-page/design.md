# 技术设计

本子任务实现 `DiscoveryProvider`、TMDB Discover/Trending、豆瓣公开 provider、24 小时新鲜缓存与 7 天旧快照兜底，并交付推荐/探索管理页。统一 DTO 与父任务 `design.md` 一致；推荐只负责作品目录和身份，不提前搜索 PT。

豆瓣实现不包含第三方移动端 key/secret，结构变化只让豆瓣栏目进入 stale/error。跨源仅在可靠外部 ID 映射时合并。
