# 技术设计

## 边界

- 固定入口图由数据源/Manifest 决定，DTO 只携带固定 `artworkUrl` 与稳定 revision。
- 动态分类图由 Server `LibraryArtworkService` 或 Player 本地展示组件生成；候选图片与生成结果不跨越不必要的信任边界。
- 直连 provider 封面保持 DataSource 原始行为，不进入候选拼贴分支。

## Server 物理媒体库

1. `PlayerMediaLibrary` 列表不再调用 `DecorateMediaLibraries`。
2. `PlayerMediaCategory` 增加 `artworkUrl/artworkRevision/artworkSource`。
3. 分类响应后调用 `DecorateMediaCategories(ctx, libraryID, categories)`。
4. 候选查询从 `media_library_entries` 与识别快照取图，并同时约束 `library_id/category_name/media kind`。
5. 生成器升级为 1920×1080 风格 3；摘要键包含模板版本、标题和候选稳定 key。

## 插件层级分类

1. 顶层 `PlayerOnlineLibraries` 不再动态装饰，继续返回 Manifest 固定图。
2. 导航节点增加可选 `artworkUrl/artworkRevision/artworkSource`，插件原始响应不允许直接传这些字段。
3. Server 在严格规范化节点后，按安全的内部 `nodeKey/routeKey` 请求 `library.artwork_candidates`，将 Host asset 生成的同源签名图写入输出节点。
4. `library.artwork_candidates` 请求增加可选 `scopeKey`；旧插件忽略该字段仍兼容。Bilibili 用 scope 映射到具体栏目，branch 使用代表栏目。
5. 任一插件调用、图片读取或生成失败只影响当前节点并使用固定回退，不影响导航列表。

## Player 独立分类

- `MediaCard` 仅在存在 `artworkCandidates` 且来源明确为 Player 本地生成时渲染风格 3 拼贴。
- 使用 CSS 还原 3×3 倾斜列、渐变、阴影和圆角；标题在左侧独立图层展示。
- Server 返回的已生成图和 provider 图走普通单图路径，不二次拼贴。

## 兼容与安全

- 新 DTO 字段均为可选，旧 Player/插件不会失效。
- 插件候选仍只能是绑定连接和包 generation 的 Host asset；Server 不把候选引用返回 Player。
- 签名、HMAC、解码炸弹限制、有界缓存与日志脱敏保持不变。
- API 只有响应字段扩展，无数据库迁移。

## 回滚

- Server 生成失败时回退既有固定分类图。
- Player 本地拼贴不足一张候选时继续渲染 provider/兜底图。
- 若插件作用域能力不可用，节点使用库的固定封面而不是跨栏目候选。
