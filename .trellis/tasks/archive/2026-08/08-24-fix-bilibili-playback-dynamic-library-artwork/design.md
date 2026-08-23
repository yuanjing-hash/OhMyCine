# Technical Design

## Boundaries

动态封面是“媒体库索引所有者”的派生展示资产，而不是某个数据源的固定图片字段。

- Server：负责 Server 管理的本地、115、在线插件媒体库候选收集、合成、缓存和同源分发。
- Player：负责独立本地/直连数据源的候选收集与本地合成；ServerDataSource 只消费 Server 结果。
- Plugin：声明候选图片的 opaque asset reference，不直接向 Player 暴露第三方 URL，也不实现独立渲染器。

## Playback Bridge

将单一 `Option<ProxyTarget>` 改为按播放 session 管理的 token map。一次 `mpv_load` 先创建/替换 session，再为视频轨、音频轨分别注册 target。路由按 token 精确查找；clear 清除整个 session。采用有界容量和会话级回收，避免成为长期开放代理。

## Artwork Contract

媒体库 DTO 暴露稳定字段：

```text
artworkUrl
artworkRevision
artworkSource: generated | provider | custom | fallback
```

候选内部契约至少包含媒体稳定身份、图片稳定身份/etag、受控图片引用、图片用途、排序时间。插件候选必须绑定插件、连接和在线媒体库身份。

## Generation Pipeline

```text
collect candidates
→ validate and deduplicate media/image identity
→ deterministic selection
→ normalized crop/layout/title overlay
→ encode 16:9 asset
→ atomic cache replace
→ publish content revision
```

生成键包含模板版本、库显示名、候选稳定身份和图片 revision。相同生成键直接复用缓存。失败保持上一个成功结果；从未成功时使用静态 fallback。

## Refresh and Compatibility

- 扫描/生活事件/plugin feed 只标记 dirty，由去抖 worker 生成。
- 手动刷新可立即排队但仍执行同库互斥与内容键复用。
- 现有静态 `libraryArtwork` 继续兼容，映射为 fallback。
- 老 Player 忽略新增字段仍可使用 `artworkUrl`；新 Player 使用 revision 做缓存失效。

## Security

- loopback 仅绑定 `127.0.0.1`，使用高熵 token、会话回收和有限 target 数。
- Server 插件图片读取继续经过 manifest 权限、域名/IP/重定向复验、响应大小和超时限制。
- Player DTO、日志和缓存键不包含上游签名 URL、Cookie、Authorization 或 Server 绝对路径。
- Player 本地生成结果仅写入受控 app cache，不写用户媒体目录。

## Rollback

- 播放代理修改可独立回退到上一实现，但发布前必须由双轨回归测试保护。
- 封面字段保持向后兼容；生成服务失败时静态 fallback 可保证 UI 不空白。
- 缓存使用独立版本目录，必要时可停止生成并继续返回旧成功封面或 fallback。
