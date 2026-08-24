# 修正媒体库分类封面层级并实现风格3

## Goal

让 Player 中的媒体库层级与封面语义一致：Server 专区及其顶层来源使用稳定的固定入口图；进入来源后，真正的媒体分类才根据该分类中的真实海报动态生成与 Emby 分类卡片同为 16:9 的“风格 3”封面。

## Background

- 当前实现错误地给 Server 物理媒体库和插件媒体库入口生成动态拼贴，而物理媒体库内部分类反而共用固定图。
- Player 独立索引的本地、OpenList/Alist、CloudDrive2 分类已有海报候选，但当前只做最多四张的平均平铺。
- 参考实现为 `justzerock/MoviePilot-Plugins` 的 `plugins.v2/mediacovergenerator/style/style_static_3.py`：1920×1080 画布、3×3 海报、整列 -15.8° 旋转、410×610 单张、22 间距、46.1 圆角、阴影、主题色横向渐变，并采用 `315426987` 显眼位排序。

## Requirements

1. Server 专区最外层入口使用固定封面，不根据内容变化。
2. Server 物理媒体库入口（本地、115 等）继续使用各自固定封面；Bilibili 等插件在线媒体库入口使用 Manifest 声明的固定封面。
3. Server 物理媒体库进入后的分类，按 `library ID + category + media kind` 严格筛选真实媒体海报候选并生成风格 3 封面；无候选时使用既有分类兜底图。
4. 层级插件的 branch/feed/user-library 节点可获得按节点作用域生成的 Server 同源签名封面；Bilibili 必须按对应栏目获取候选，不能所有分类共用首页推荐。
5. Player 独立索引来源的分类封面改为风格 3 构图，标题由 Player 在左侧覆盖，避免依赖 Server 字体；不写入媒体目录。
6. Server 生成图统一为 1920×1080，最多九张候选，并保持现有解码尺寸/像素/字节限制、内容摘要、签名 URL 和有界缓存。
7. 直连 Emby/Jellyfin 及其他 provider 已提供的媒体库封面不进入本次动态生成逻辑，不改变其 URL、缓存和展示语义。
8. 顶层入口、分类节点和媒体项目的返回导航层级保持现状，不把封面改动扩展为新的导航模型。

## Acceptance Criteria

- [x] Server 专区卡片和最外层本地/115/Bilibili 卡片使用固定 1920×1080 图，不随库内容改变。
- [x] 进入本地或 115 媒体库后，只有实际存在内容的分类出现，且每个分类只使用该库、该分类、该媒体类型的海报候选。
- [x] 动态分类封面为 1920×1080，视觉结构与参考风格 3 一致：左侧标题区、主题色渐变、右侧三列倾斜海报、圆角和阴影，而不是平均四宫格。
- [x] Bilibili 的动画、影视、推荐、热门、番剧等节点能使用其对应栏目候选；上游无图或失败时安全回退固定图。
- [x] Player 独立本地/原始来源分类同样使用风格 3；直连 Emby/Jellyfin 封面未被重组或覆盖。
- [x] Player 不接收插件远端图片 URL、Cookie、Host asset UUID 或 Server 内部路径，只消费 Server 同源封面 URL。
- [x] Server、插件与 Player 的相关回归测试通过，Player typecheck/lint/build 与 Server `go test ./...`、`go vet ./...` 通过。
- [x] 完成后推送 Server/Player 代码至 `develop`，发布新的 Player Beta；若 Bilibili 包协议或实现改变，发布新版官方插件。

## Out of Scope

- 修改 Emby/Jellyfin 自身的媒体库封面。
- 给普通媒体项目重新生成海报或修改 TMDB 刮削结果。
- 引入用户可编辑的封面设计器或多风格选择器。
- 改变现有媒体库/插件导航信息架构。
