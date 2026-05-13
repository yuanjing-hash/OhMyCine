# Player 播放历史与继续观看

## Goal

用 Tauri app data 下的 SQLite 保存播放历史和继续观看进度，让 Player 能跨重启恢复播放位置，并为首页/媒体库继续观看区域提供本地数据基础。

## Requirements

- 新增播放历史 SQLite 存储，复用项目已有 `rusqlite` 技术栈。
- 记录媒体 identity、sourceId、libraryId、title、path/stream identity、position、duration、updatedAt、completed 等字段。
- 播放中定期保存进度，暂停/切换/关闭时保存最终状态。
- 再次播放同一媒体时可提示或自动恢复到上次位置。
- 本地历史不应泄露 tokenized URL；显示和日志需要 redaction。
- 与 Emby 自带 continue-watching 不盲目重复；本地数据聚合需有清晰来源标识。

## Acceptance Criteria

- [ ] Tauri SQLite schema 和 command 存在。
- [ ] 播放进度会按节流策略保存。
- [ ] 重新打开同一媒体可恢复或提示恢复进度。
- [ ] 已接近结尾的媒体可标记 completed，避免继续观看噪声。
- [ ] 本地 continue-watching 数据可被前端读取。
- [ ] `npm run typecheck` / `npm run lint` / `npm run build` 通过。
- [ ] `cargo check` 通过。

## Out of Scope

- Server 同步。
- 多用户历史隔离。
- 云端媒体服务反向同步 watched state。

## Technical Notes

- Parent task: `05-12-player-playback-controls`.
- Existing reference: `player/src-tauri/src/commands/credential.rs` uses Tauri app data + `rusqlite`.
