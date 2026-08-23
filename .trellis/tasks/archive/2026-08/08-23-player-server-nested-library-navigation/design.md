# 技术设计

## 1. 信息架构

```text
OhMyCine Server（数据源）
├─ 本地媒体库（标准媒体库）
│  ├─ 华语电影
│  ├─ 外语电影
│  ├─ 国产剧
│  └─ 日番
├─ 115 测试盘（标准媒体库）
│  └─ 同该媒体库关联的分类规则
└─ Bilibili 连接（插件在线媒体库）
   ├─ 推荐（内容叶节点）
   ├─ 热门（内容叶节点）
   └─ 番剧（分支节点）
      ├─ 中国大陆（内容叶节点）
      ├─ 日本（内容叶节点）
      └─ 其他（内容叶节点）
```

Server 数据源根页只负责选择媒体库。进入媒体库后，`SourceLibraryView` 复用现有文件夹式导航和面包屑呈现分类树，不额外创建一个与数据源浏览并行的 Bilibili 专用页面。

## 2. 两种导航所有权

### 2.1 标准媒体库：`standard-catalog`

适用于 Server 内置本地、115 和后续标准存储媒体库。

- 分类真相来自媒体库关联的分类规则和已扫描条目的 `category_name`。
- Server 提供分类摘要接口，保持规则顺序，并补入历史条目中仍存在的未知分类。
- Player 进入数字媒体库 ID 时先取分类节点；进入分类节点后以 `category` 过滤目录作品。
- 插件协议不参与这条链路。

### 2.2 插件媒体库：`plugin-navigation`

适用于 Bilibili 及后续非标准在线站点。

- 插件 Manifest 显式声明 `navigationMode: hierarchical`；默认值为 `flat`，保证旧插件兼容。
- 现有 `site.navigation` 能力升级为可接收可选 `parentNodeKey` 的版本化请求；根请求不带父节点。
- 插件返回 `branch`、`feed`、`search`、`user-library` 四类通用节点。只有 `branch` 可继续请求子节点；其余节点进入既有 feed/search/user-library 流程。
- `nodeKey` 和 `routeKey` 对 Player 完全不透明，Player 不解析站点语义。

建议响应：

```json
{
  "version": 2,
  "mode": "hierarchical",
  "nodes": [
    {
      "id": "anime",
      "title": "番剧",
      "kind": "branch",
      "nodeKey": "anime",
      "hasChildren": true,
      "iconKey": "tv"
    },
    {
      "id": "recommended",
      "title": "推荐",
      "kind": "feed",
      "routeKey": "recommended",
      "refreshable": true
    }
  ]
}
```

子节点请求：

```json
{
  "connectionId": "...",
  "parentNodeKey": "anime",
  "depth": 1
}
```

旧版数组响应继续按 v1 单层叶节点解析。

## 3. Server API

### 标准媒体库

- 新增 `GET /api/v1/player/media-libraries/:id/categories`。
- 扩展 `GET /api/v1/player/media-libraries/:id/catalog`，增加严格校验的 `category` 参数。
- 分类摘要至少包含 `id`、`name`、`media_type`、`item_count`、`sort_order`。

### 插件媒体库

- 保留 `GET /api/v1/player/online-libraries/:id/navigation` 获取根节点。
- 新增 `GET /api/v1/player/online-libraries/:id/navigation/:nodeToken/children` 获取分支子节点。
- Server 将插件节点规范化后签发短期或稳定的 `nodeToken`，token 绑定媒体库、连接、节点类型、节点键、深度和祖先摘要；Player 不直接把任意文本作为插件父节点。
- feed 路由继续使用现有接口，但叶节点令牌必须解析为与当前媒体库绑定的 `routeKey`。

`nodeToken` 不含 Cookie、凭据或上游 URL。Server 最大深度 8、每层最多 100 个节点、节点标题 256 字符、键 256 字节，并对当前路径中的重复节点键拒绝继续展开。

## 4. Player 数据模型

扩展在线标识而不把嵌套树塞入 `MediaItem.children`：

```text
server-category | libraryId | categoryToken
online-library  | libraryId
online-node     | libraryId | nodeToken | nodeKind
online-work     | libraryId | workId
online-version  | libraryId | workId | segmentId | versionId
```

- 分类和导航节点仍映射为 `MediaItem(type: folder)`，因此当前 `DataSource.list()`、卡片、面包屑及返回逻辑可复用。
- 节点 ID 包含媒体库身份，缓存键必须使用 `sourceId + libraryId + nodeToken + cursor`。
- 面包屑名称仅用于展示，重新加载依赖节点 ID，不依赖标题或路径文本。
- Server 来源首页不再同时拉取并平铺各媒体库内容；首页聚合继续走 `getHomeSections()`，与进入 Server 来源后的层级浏览互不混淆。

## 5. Bilibili 播放故障

现有证据表明登录、Feed 和详情均成功，503 仅发生于 `media.playback`。修复时分段记录安全错误码：

1. 校验作品、分 P、版本与清晰度身份。
2. 请求 Bilibili `playurl`，记录上游业务码的安全映射，不记录 Cookie 或完整 URL。
3. 解析 DASH/渐进式资源；优先选择当前播放器可用的 AVC/AAC 组合，同清晰度再按带宽选轨，避免只选到 HEVC/AV1 导致兼容问题。
4. 分别注册视频、音频资源并验证 CDN 域名位于插件权限白名单。
5. Server 网关转发 Range、Referer、User-Agent 和必要响应头。
6. Player 获取播放计划后，分别交付 DASH 视频与音频，并保留清晰度 variants。

先增加不会泄密的阶段化诊断，再用用户当前已登录连接复现，依据实际失败阶段修正，而不是把所有故障统一映射成“来源不可用”。

## 6. 兼容、发布与回滚

- Manifest 新字段可选，缺省保持 v1 flat 行为。
- Server 先兼容 v1/v2，再发布新版 Bilibili 插件，最后发布 Player；避免新插件被旧 Server 安装后无法使用。
- Player 遇到不认识的节点类型时忽略该节点并展示来源响应无效，不导致整页白屏。
- 回滚 Player 后 v1 导航仍可用；回滚插件后 Server 继续解析旧数组；数据库只增加查询索引或无破坏字段，不改现有媒体条目身份。
