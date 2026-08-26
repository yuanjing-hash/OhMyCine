# 扫描计划设置即时保存反馈

## Goal

修复设置页扫描计划修改后缺少保存成功反馈的问题。对于没有显式“保存”按钮的即时设置，用户修改全量/增量扫描开关或间隔后，界面必须主动告诉用户配置已保存；失败时继续显示安全错误。

## What I Already Know

* 用户明确指出：没有按钮的设置项，软件应在用户修改后主动提示保存结果；有按钮时才是点保存后提示。
* 当前扫描计划 UI 在设置页数据源管理卡片内，开关使用 `@change` 即时保存，间隔输入也使用 `@change` 即时保存。
* 当前 `updateRawScanSchedule()` 成功后只清空 `feedback`，失败才提示 `扫描计划保存失败。`
* 这属于 Player 设置页交互修复，不改变扫描调度、缓存、凭据或数据源逻辑。

## Requirements

* 修改全量/增量扫描启用状态后，保存成功时显示明确成功反馈。
* 修改全量/增量扫描间隔后，保存成功时显示明确成功反馈，反馈内容应包含源名称、扫描类型和新间隔。
* 输入非法间隔时不能静默无响应，应给出用户可理解的提示，且不写入配置。
* 失败反馈继续使用 `toSafeErrorMessage()`，不泄露敏感信息。
* 反馈复用现有 SettingsView 的 `feedback` 浮层，不引入新的全局 toast 系统。

## Acceptance Criteria

* [x] 在数据源管理页修改 raw source 的全量/增量开关后，顶部浮层显示“已保存”类提示。
* [x] 在数据源管理页修改 raw source 的全量/增量分钟间隔后，顶部浮层显示“已保存”类提示。
* [x] 输入 `0`、负数、非数字或空值时显示错误/提示，并且不会调用保存。
* [x] Emby/Jellyfin 的“不适用”说明保持不变。
* [x] `npm run typecheck`、`npm run lint`、`npm run build` 通过。
* [x] Windows GNU exe 刷新。
* [x] 本地 git commit 完成，不 push GitHub。

## Definition of Done

* Trellis task 上下文完成并进入 `in_progress`。
* 设置页即时反馈行为完成。
* 相关验证命令通过。
* `player/src-tauri/target/x86_64-pc-windows-gnu/release/ohmycine-player.exe` 刷新。
* 本地提交完成。

## Technical Approach

* 在 `SettingsView.vue` 中为扫描类型和源名称增加小型格式化 helper。
* 扩展 `updateRawScanSchedule()` 的参数，让调用方传入成功反馈文案。
* `updateRawScanScheduleEnabled()` 和 `updateRawScanScheduleInterval()` 成功后设置 `feedback.value = { type: 'success', ... }`。
* 对非法分钟输入设置 `feedback.value = { type: 'error', ... }` 并提前返回。

## Out of Scope

* 不新增全局 toast/通知系统。
* 不改变扫描调度、watcher、缓存、DataSource 或凭据存储。
* 不调整扫描间隔默认值。
* 不执行 `git push`。

## Technical Notes

* Main file: `player/src/views/SettingsView.vue`
* Existing feedback UI: `feedback && mode === 'manage'` fixed top-right panel.
* Existing save path: `updateRawScanScheduleEnabled()` / `updateRawScanScheduleInterval()` → `updateRawScanSchedule()`.
