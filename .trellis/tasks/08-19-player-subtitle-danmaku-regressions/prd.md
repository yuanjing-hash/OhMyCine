# 修复字幕恢复、Emby 外挂字幕与桌面弹幕

## Goal

修复 Player 在 Windows 与 Android 上的字幕偏好恢复、Emby 外挂字幕加载，以及 Windows 弹幕开启但不可见的回归，同时保持播放启动、控制响应和敏感数据边界不退化。

## Requirements

- 按 `sourceId + mediaIdentity` 保存的字幕/音轨偏好必须在同一媒体再次播放时恢复。
- 轨道恢复优先使用语言、标题、编码、声道等稳定指纹，数字 mpv 轨道 ID 仅作兜底。
- 播放启动期间轨道元数据异步到达时不得丢失恢复机会，也不得引入高频 `mpv_track_state` 轮询。
- Emby 外挂字幕必须绑定实际选中的媒体版本和对应 MediaSource 轨道，在 Windows 与 Android 均可加载。
- Emby Token、字幕 URL 和请求 Header 只能短期存在于播放链路内存，不得进入偏好、播放历史、路由、普通日志或诊断。
- 外挂字幕下载必须限制协议、重定向、响应大小和扩展名，并在桌面与 Android 统一写入受控短路径运行缓存后交给 libmpv。
- 弹幕开启时至少保留一种可见类型；迁移已有“总开关开启但三类全关”的配置时自动恢复滚动弹幕。
- 弹幕加载失败或字幕恢复失败不得阻塞视频播放、控制栏、进度同步或队列切换。

## Acceptance Criteria

- [x] 同一媒体重播时，内嵌字幕、关闭字幕、音轨及缓存字幕偏好按稳定指纹恢复。
- [x] 轨道状态在恢复命令执行期间更新时，完成后会进行一次有界重试。
- [x] 字幕原生命令失败时前端保留错误状态且不会把失败选择保存/标记为已恢复。
- [x] Emby 播放请求携带实际 `mediaSourceId`、对应字幕轨和必要 Header；详情展示不依赖持久化敏感字幕 URL。
- [x] Windows 与 Android 的远程外挂字幕都通过相同的 Rust 受控缓存准备逻辑。
- [x] 旧桌面弹幕配置 `{ enabled: true, showScroll: false, showTop: false, showBottom: false }` 加载后至少显示滚动弹幕。
- [x] 新增行为测试覆盖偏好匹配/恢复并发、Emby 多媒体版本字幕映射、字幕 Header 边界和弹幕无效配置。
- [x] Player typecheck、lint、build、相关 verify 脚本、Cargo tests/check/clippy 通过。

## Notes

- 不修改或回滚当前工作区中的 Server 改动。
- 不读取、输出或记录 Emby 凭据和完整签名 URL。
