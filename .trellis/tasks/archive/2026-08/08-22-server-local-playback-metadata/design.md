# Design — Server 本地播放与媒体详情元数据

## Architecture and boundaries

### Stream endpoint

保留现有 `GET/HEAD /api/v1/player/media-entries/:id/stream`，由认证后的 Player token 访问。service 返回一个有判别字段的播放解析结果：

- `local_file`：已验证的本地普通文件句柄/内部路径、显示文件名、大小与修改时间；handler 使用 `http.ServeContent` 响应 GET/HEAD/Range。
- `redirect`：115 active managed STRM artifact 的既有 `ProxyRedirect`，handler 继续返回 302。

service 在一次查询中绑定 entry → enabled library → enabled storage，并按 Storage 类型分流。绝对路径只存在于本次请求的 Server 内存中，不进入 JSON DTO、审计事件或客户端错误。

### Local path safety

1. 使用 `medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)` 得到受控媒体库根。
2. 将数据库中的 provider-relative `entry.RelativePath` 规范化并约束到媒体库根。
3. 从根到目标逐段 `Lstat`，拒绝 symlink、Windows reparse point/junction、目录终点与任何边界偏移。
4. 打开文件后使用句柄的 `Stat` 再确认普通文件，减少检查后替换风险；响应期间持有句柄。
5. 所有客户端错误使用稳定安全码/通用消息，不拼接绝对路径。

### Playability projection

媒体版本的可播放性由 Storage 类型计算：

- `local`：库/Storage 启用且 entry 可解析到安全普通文件。
- `pan115`：保持 `STRMEnabled && SignedProxyEnabled` 且存在 active managed completed STRM artifact。
- 其它类型：暂不直出。

媒体库 `direct_stream` 同步反映本地可直接读取或 115 signed STRM 可用，不再把本地库整体标成不可播放。

## Metadata contract

`PlayerMediaItem` 新增可选字段，保持旧客户端兼容：

- `original_title`, `rating`, `tagline`, `runtime_minutes`
- `genres`, `directors`, `writers`, `cast`
- `tmdb_id`, `imdb_id`
- `still_paths`

人物数组优先输出简化名称列表，避免把 TMDB 原始人员 payload 直接暴露给客户端。图片字段仍是 TMDB file path，Player 通过已有 TMDB artwork origin 生成展示 URL。

TMDB `Snapshot` 增加有界 `BackdropPaths`（或语义等价字段）。详情请求的 `append_to_response` 增加 `images`，对 file path 进行 `cleanImagePath`、去重、限制条数。首图与旧 `BackdropPath` 保持一致。旧 JSON 反序列化天然兼容；投影时在数组为空时回退单个 `BackdropPath`。

## Player mapping

`ServerDataSource` 扩展 runtime-safe record shape，并把新增字段映射到现有 `MediaItem/MediaDetail`。版本仍通过同一 `DataSource` 接口生成，不让视图直接调用 Server API。

Emby 列表查询保留 `ImageTypeLimit=1`；详情 payload 使用单独的有界图片参数，例如 `ImageTypeLimit=8`。人物类型比较使用规范化小写，名称 trim 后按大小写不敏感去重。剧照 URL 从详情返回的 Backdrop tags 构造，并限制数量。

`MediaDetailView` 的无分集文案由来源决定；Server/通用来源显示中性说明，只有真实 Emby 来源才显示 Emby 文案（如仍需要）。

## Compatibility

- 新 Server + 旧 Player：新增 JSON 字段被忽略；本地版本变为可播放。
- 旧 Server + 新 Player：新增字段均为可选，详情继续以旧字段显示。
- 旧 TMDB snapshot：以单张 `BackdropPath` 回退，不要求数据库迁移。
- 115：沿用现有 artifact 解析、302、ticket/鉴权及多设备路径。

## Operational and rollback

- 本地直出没有新公开端口和匿名路由，仍使用 Server 主端口及 Player Bearer。
- 大文件由 `ServeContent` 流式读取，不读入内存；客户端断开由请求上下文/HTTP server 结束传输。
- 若本地直出出现回归，可回滚 Storage 类型分流而不影响 115 proxy；新增 JSON 字段和 Snapshot 字段可安全保留。
- 发布只打 Player Beta tag；Server 代码随同一 `develop` 提交可部署测试，但不创建 Server Release。

## Risks

- Windows reparse point 检测必须复用仓库现有实现，不能只检查 `os.ModeSymlink`。
- `http.ServeContent` 需要已打开的 seekable file，并正确处理 HEAD/Range；handler 不应先写 JSON envelope。
- 详情图片数量增加会提升 Emby/TMDB 请求与渲染成本，因此服务端和客户端均限量。
