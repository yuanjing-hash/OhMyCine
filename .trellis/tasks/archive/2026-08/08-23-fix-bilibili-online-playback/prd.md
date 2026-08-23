# 修复 Bilibili 在线媒体播放不可用

## Goal

定位并修复 Player 通过 Server 播放 Bilibili 在线媒体时报在线来源不可用。

## Requirements

- 保持用户当前 Bilibili 登录状态和运行中的 Player/Server 进程。
- 为播放方案的身份校验、playurl 请求、DASH 解析、视频资产注册、音频资产注册和网关交付增加可筛选的安全日志阶段。
- 依据真实失败阶段修复 503；不能通过删除错误、伪造空播放计划或回退到不受保护的上游 URL 解决。
- 正确处理 Bilibili DASH 视频与独立音频，优先选择 Player 可解码的 AVC/AAC 轨道，并保留清晰度切换信息。
- Server 网关继续保护 Cookie、Referer、User-Agent、CDN URL token 和播放资产引用。
- 将登录失效、地区/会员限制、清晰度不可用、上游限流、响应格式错误、资产注册失败映射为稳定且不同的错误码。

## Acceptance Criteria

- [ ] 截图中的 Bilibili 视频可以在 Player 中开始播放且有声音。
- [ ] 切换到插件返回的另一可用清晰度后可继续播放。
- [ ] Server 日志能准确指出播放失败阶段，但不含 Cookie、Authorization、完整 CDN token URL 或插件私有状态。
- [ ] 上游受限内容在 Player 显示有意义的错误，而不是统一显示“在线媒体来源暂时不可用”。
- [ ] Bilibili Rust 单元测试、Server 在线媒体/资产网关测试及 Player 播放映射测试通过。

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
