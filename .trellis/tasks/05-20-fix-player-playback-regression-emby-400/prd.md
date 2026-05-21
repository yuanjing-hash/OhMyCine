# 修复播放回归和 Emby 400 上报

## Goal

修复最新构建中部分视频无法播放、Emby provider sync started 请求 400、以及返回主页后本机历史记录消失的问题。优先恢复播放器基础播放与本机历史稳定性，再让 Emby 上报按真实诊断收敛。

## Requirements

- 不能让 provider sync 或诊断逻辑影响 mpv 播放启动、停止、返回和本机 SQLite 历史。
- 部分 Emby 视频无法播放必须定位到流地址、mpv load/resume、或最近改动引入的副作用，并修复。
- Emby `started` 上报当前 400：`/Items/{Id}/PlaybackInfo` 返回 `MediaSourceId` 但没有 `PlaySessionId`；`/Sessions/Playing` 和 legacy user-item started 都 400。需要避免发送会被 Emby 拒绝的 started payload，并继续保留 token-safe 诊断。
- 本机历史记录不能因为播放失败、返回、0 秒 stopped/paused 或 provider sync 失败被删除/覆盖。
- tokenized URL、api_key、Authorization 不得展示或持久化。

## Acceptance Criteria

- [ ] 有些视频不能播放的回归被修复，正常视频播放不受影响。
- [ ] 播放失败/返回时不会清掉已有继续观看历史。
- [ ] Emby sync 在缺少 PlaySessionId 时不再用明显会 400 的 session started payload 破坏诊断；诊断显示具体跳过/失败原因。
- [ ] Provider sync 失败不影响本机历史保存和播放。
- [ ] typecheck、lint、build、Windows Tauri build 通过。

## Technical Notes

- 用户给出的诊断显示：`PlaySessionId` 缺失，`/Sessions/Playing` 400，`/Users/{UserId}/PlayingItems/{Id}` 400，query 中 `PositionTicks=9729720000` 但 UI 行 `pos=0`，说明 started 使用了 resume startPosition 上报但诊断 position 显示当前 0。
- 需要检查 `player/src/services/datasource/emby.ts`、`player/src/views/PlayerView.vue`、`player/src/composables/useMpv.ts`、`player/src/services/playbackHistory.ts`。
- 尝试读取 Windows 日志：`C:\Users\VibeCoder\AppData\Local\com.ohmycine.player\logs\render-diagnostics.log`，WSL 下可能为 `/mnt/c/Users/VibeCoder/AppData/Local/com.ohmycine.player/logs/render-diagnostics.log`。
