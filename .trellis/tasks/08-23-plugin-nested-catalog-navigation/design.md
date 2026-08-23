# 技术设计

采用父任务 `design.md` 中的 `standard-catalog` / `plugin-navigation` 双模式。

Server 新增标准分类摘要和分类过滤；插件契约以 v2 对象响应表达 `branch/feed/search/user-library` 节点，并继续接受 v1 数组响应。Server 为规范化节点签发绑定媒体库、节点类型、深度与祖先摘要的 token。Player 将分类/插件节点映射为通用 folder item，通过 `DataSource.list()` 递归加载，现有 `SourceLibraryView` 负责卡片、面包屑和返回。

首页聚合继续走 `home.contribution`，不会为了展示首页而遍历插件导航树。
