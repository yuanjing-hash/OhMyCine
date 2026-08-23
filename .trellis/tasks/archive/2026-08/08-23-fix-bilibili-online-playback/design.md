# 技术设计

## 已知证据

- 扫码登录成功。
- `site.feed` 成功。
- 在线详情接口成功。
- 同一条目随后调用 `media.playback` 返回 503。

因此问题位于插件 `resolve_playback()` 到 Server 资产注册之间，而不是 Player 媒体库发现或详情标识解析。

## 诊断与修复策略

给插件播放链路增加结构化阶段码，不记录敏感值：`identity`、`playurl`、`parse_dash`、`register_video`、`register_audio`、`gateway`。Server 保留内部错误码并对 Player 返回安全消息。

播放轨道选择从“同清晰度最高带宽”调整为“当前可解码 codec 优先，再比较带宽”，避免同一清晰度优先选中 HEVC/AV1 后被当前播放后端拒绝。DASH 视频和音频均通过独立资产引用交付，网关转发 Range 及上游要求的请求头。

实现前先以当前连接复现并确认实际失败阶段；如果是 Bilibili 业务码，则补足必要的已授权请求参数或明确映射账号/内容限制，而不是绕过站点权限。
