# MoviePilot v3 参考研究

## 参考范围

- 后端：`app/api/endpoints/recommend.py`、`discover.py`、`search.py`，`app/chain/recommend.py`，`app/chain/douban.py`，`app/modules/douban/*`。
- 前端：`src/pages/recommend.vue`、`src/pages/discover.vue`、`src/views/discover/DoubanView.vue`、`TheMovieDbView.vue`、`src/components/dialog/SiteAddEditDialog.vue`。

## 借鉴的成熟思路

- 推荐、探索、元数据 provider 与 PT 搜索分层，推荐浏览不会为每张海报提前搜索 PT。
- 推荐栏目缓存优先、手动刷新、来源级错误隔离、旧快照兜底，并展示来源与更新时间。
- 多站点搜索使用渐进结果；单站点内部顺序分页，多站点之间有界并发，空页停止。
- 站点管理覆盖启停、优先级、超时、限速、代理/UA、连通测试和健康状态。
- 用户从资源结果创建下载后进入统一下载、整理与入库流水线。

## OhMyCine 的差异与清洁室边界

- 不复制 MoviePilot GPL 源码、选择器实现、内置移动端 API key、签名 secret 或模拟客户端凭据。
- 豆瓣 provider 只使用公开网页/公开数据入口，并可独立替换；故障时保留 TMDB 和安全缓存。
- PT 站点是 Server 内建 adapter；非 PT 内容站点仍由受限 WASM 插件扩展。
- 浏览器永不接收 PT Cookie、passkey 或真实种子 URL。搜索结果使用 Server 内部短期不透明令牌，下载时重新解析。
- 所有下载复用 OhMyCine 已有 `DownloadService`、暂存目录、媒体库排序/选择、分类 Profile、Transfer 与入库任务。

## 实现基线

- 推荐快照新鲜期 24 小时，安全旧快照保留 7 天；刷新按 provider/栏目粒度执行。
- PT 搜索按站点分组渐进返回；首版 HTTP 流采用 NDJSON/SSE 兼容事件，普通 JSON 接口保留用于自动化测试和降级。
- PTTime 首版封装 NexusPHP 兼容路径与解析器，真实站点契约差异只留在 adapter 内。
