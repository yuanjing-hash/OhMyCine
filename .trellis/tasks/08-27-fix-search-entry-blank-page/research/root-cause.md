# 根因研究

## 搜索白屏

- `server/internal/services/site_identity_search.go:148-153` 为减小最终聚合响应把复制分组的 `Items` 设为 `nil`，JSON/SSE 因而输出 `"items":null`。
- `server/webui/src/views/ExploreView.vue:74` 与 `server/webui/src/sites.ts:250-305` 假定 `items` 永远是数组，收到首批 `null` 分组后调用 `.map()`，Vue 主视图崩溃。
- 浏览器复现错误为 `TypeError: Cannot read properties of null (reading 'map')`。
- 修复边界必须双层防守：Server 保证 `[]`，WebUI wire/session 边界也归一为 `[]`。

## TMDB ID 直接搜索不可达

- UI 和 Server 原有 `/torrent-search?search_by=tmdb_id` 合同仍在。
- `server/webui/src/views/ExploreView.vue` 的 `trustedIdentity` 只检查 `mediaType + tmdbID`，把用户手工填写/选择的 TMDB ID 也当作详情页可信身份，转发到 `/discovery/media/{type}/{id}/torrent-search`。
- 可信身份必须来自海报/详情导航上下文；直接搜索表单输入的 TMDB ID 必须保留旧路径。

## 追更错误提交 PT 到 115

- `server/internal/services/follow.go:212-240` 查询全部启用 Site、Downloader、MediaLibrary，把所有 Site ID 默认全选，再分别选第一个 Downloader 和第一个 MediaLibrary。
- `server/internal/services/follow.go:570-598` 的快照校验仅验证对象存在且启用，没有验证三者的类型、来源格式和 115 connection identity。
- `server/internal/services/follow_worker.go:245` 按快照遍历全部站点；`server/internal/services/follow_worker.go:197` 将选中结果直接交给 Site download。
- `server/internal/services/site.go:1099-1155` 对私有 PT 适配器获取 `.torrent` 后直接构造 `SourceTorrent`，未在取种或提交前拒绝 115 原生离线。
- `server/internal/services/download.go:606-680` 已有下载器与媒体库的部分目标校验，同账号 115 限制已经存在，但没有表达 SiteType/PT→SourceKind→Downloader 的完整合同，也没有在 Follow 默认值/UI 中复用。

## 115 运行中任务被误判失败

- 固定依赖 `github.com/SheltonZhu/115driver v1.3.5` 的权威语义为 `0=todo`、`1=running`、`2=done`、`-1=failed`。
- `server/pkg/cloud/pan115/client.go:729-735` 把 `1` 和 `-1` 都映射为失败。
- `server/pkg/cloud/pan115/client_offline_test.go:164-183` 也错误断言 status 1 为 failed，固化了缺陷。
- 真实两个自动追更任务均已获得 provider task ID，约 20 秒首次轮询后被标记失败，与默认轮询间隔一致。
- 状态映射错误和 PT→115 不兼容是两个独立缺陷：前者解释“为什么显示失败”，后者解释“为什么根本不该提交”。

## 既有能力与约束

- `server/pkg/site/builtin/catalog.go:24-25` 已定义稳定的 `SiteTypePT="pt"` 和 `SiteTypeBT="bt"`。
- `server/webui/src/downloads.ts` 的 `compatibleDownloadLibraries` 已能限制 115 下载器只能匹配同账号 115 媒体库，但 Follow API 选项缺少类型/账号信息且编辑器未复用。
- 真实任务不可由修复脚本自动重试、删除或迁移；实现与浏览器验证均保持只读，除非用户主动操作。

## 115 Downloader 与目录监听的真实语义

- `server/webui/src/views/MediaLibrariesView.vue:70` 只列出与当前媒体库 Storage 同 `connection_id` 的已启用 `pan115_offline` 下载器。
- `server/internal/services/media_library.go:530-590` 保存时再次验证同账号、类型、启用状态以及中转根不与最终/其它中转根重叠。
- `server/internal/services/download.go:523-557` 在中转目录发现 provider item 时，用 `MediaLibrary.IngestDownloaderID` 创建普通内部 DownloadTask，复用识别和 Transfer 流水线。
- `server/pkg/downloader/pan115offline/client.go:48-53,134-151` 对 share/provider-item 使用 `SubmitRequest.ProviderDirectoryID` 覆盖 Downloader 自己的离线目录；provider item 只验证已存在条目并立即返回 completed，不监测离线任务。
- `server/internal/services/downloader.go:322-350` 说明 115 Client 的账号/driver 与下载目录本来就从 Downloader 绑定的 Storage/Connection 派生。
- 用户最终确认 115 Downloader、所属 115 Storage/媒体库和下载目录是一体路线：不再配置媒体库级“自动摄取中转目录”，而是在 Downloader 上开启“自动监听生活事件”，复用其下载目录。
- `server/internal/services/media_library.go:1003-1060` 的现有 sweep 会枚举 intake root 的直接子项；`server/pkg/downloader/pan115offline/client.go:44-73` 的普通离线任务也直接把 provider 输出提交到 Downloader 目录。如果原样合并，目录 sweep 可能在 Download Worker 完成前抢先为同一输出创建第二条接管任务。
- `server/pkg/downloader/pan115offline/client.go:80-126` 已让分享任务使用稳定 `omc-<task-id>` 子目录；`server/internal/services/media_library.go:1054` 已跳过 `omc-*`。正确收口是让普通离线也进入 `omc-*` 保留子目录，由原 Download Worker 独占；生活事件监听只接管下载目录下的普通直接子项。
- `server/internal/services/download.go:539-557` 已有基于 provider item 的持久去重与数据库唯一索引基础，但新合同需要把 key 扩为 Connection + Downloader + provider item，并在 claim 前增加静默窗口/连续清单稳定复核。

## 115 分享入口与最终产品决定

- `server/webui/src/views/DownloadsView.vue:448-449` 已在“下载管理 → 新建下载”按 115 Downloader 能力显示分享来源，`server/webui/src/views/DownloadsView.vue:211-229` 也要求显式 Downloader。
- 用户否决独立“115 转存”侧栏/页面。最终交互是在“新建下载”选择 115 Downloader 后显示“离线下载 / 分享转存”，两者共用 Downloader 下载目录并继续选择同账号兼容 MediaLibrary。
- 后端已经具备 share receive、加密来源和统一识别/Transfer 基础；缺口是把来源方式表达清楚、移除独立 intake root 依赖、让状态覆盖从转存到最终入库的完整阶段。
