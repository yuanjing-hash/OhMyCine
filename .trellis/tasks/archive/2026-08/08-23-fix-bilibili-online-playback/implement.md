# 实施计划

1. 为 `resolve_playback()` 及 Host 资产注册补充脱敏阶段错误和测试。
2. 使用当前已登录连接复现，定位 503 的实际阶段与 Bilibili 安全业务码。
3. 修正 playurl 参数、DASH 轨道选择或 Host 域名/资产注册中的实际缺陷。
4. 验证 Server 网关 Range、视频/音频双资产、过期和错误映射。
5. 运行 Bilibili、Server 和 Player 相关测试，并做真实播放与清晰度切换冒烟。
