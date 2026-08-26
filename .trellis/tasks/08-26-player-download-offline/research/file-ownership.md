# Player 下载任务文件所有权

用户要求本任务与其它 Player 工作避免交叉修改。实施按可独立验证的切片领取文件，不以大范围重构一次占用所有共享入口。

## 当前切片：Rust 下载核心

本任务当前独占：

- `player/src-tauri/src/commands/downloads.rs`
- `player/src-tauri/src/downloads/**`
- 下载核心专项 Rust 测试与 fixture
- 仅为编译/命令注册所需的 `player/src-tauri/src/lib.rs`、`player/src-tauri/src/commands/mod.rs` 最小 hunk

当前明确避让：

- `player/src/App.vue`
- `player/src/views/SettingsView.vue`
- `player/src/views/PlayerView.vue`
- `player/src/components/layout/FloatingControls.vue`
- `player/src/components/media/MediaCard.vue`
- `player/src/services/datasource/**`
- `player/src/stores/**`
- Server 源码

## 协作规则

- 每个后续切片开始前重新检查 `git status` 和活动任务；发现目标文件已有非本任务改动时停止领取该文件。
- 新功能优先放入下载/离线专用模块，通过小型兼容层接入共享入口。
- 不还原、覆盖、整体格式化或夹带其它任务改动。
- 共享 UI、DataSource 与播放入口只有在对应切片开始且确认无冲突后才领取。
- 所有测试使用临时数据库/目录，不读取、重置或迁移真实 Player profile。

## 2026-08-26 23:03 写入冲突暂停

`player/src-tauri/src/commands/downloads.rs` 正被另一个不可见的并行会话持续写入。本任务的多分段切片已停止对该文件的任何修改、格式化和自动修复，直到文件写入稳定且重新完成归属审计。

- 其他任务或会话如果看到本说明，请不要再修改 `downloads.rs`。
- 当前可继续领取的工作必须使用新的离线专用文件，且不修改 `downloads.rs`、Server 源码或另一个 Trellis 任务目录。
