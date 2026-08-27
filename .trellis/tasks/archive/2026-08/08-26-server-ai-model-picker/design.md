# Server AI 模型选择窗口设计

## Boundaries

本任务只改 Server 的 AI Provider 响应读取边界和嵌入式 Web UI。API 路由与 DTO 形状保持不变：`POST /api/v1/settings/ai-recognition/models` 仍返回 `{ list, total }`。

## Backend Design

- 将通用 `readBounded(reader)` 改为显式接收上限，或拆分为语义明确的模型列表/结构化生成读取函数。
- 新增 `maxModelListResponseBytes = 4 << 20`；`ListModels` 使用该上限。
- Chat Completions 与 Google generateContent 继续使用 `maxStructuredResponseBytes = 256 << 10`，并继续对 `message.content`/candidate text 执行相同限制。
- 保留完整的 HTTP 状态映射、SSRF-safe client、无重定向、20 秒超时和响应解码约束。
- 服务错误映射区分 `ai_response_too_large` 在模型列表探测场景下的用户文案；不把上游响应正文写入日志或 API。

## Web UI Design

新增 `AIModelPickerDialog.vue`：

- Props：`open`、`models`、`selectedModel`；事件：`close`、`select(modelID)`。
- 使用 `Teleport to="body"`、模态遮罩、`role="dialog"`、标题关联、Esc/Tab 处理和焦点恢复，与目录选择器保持一致。
- 搜索为纯前端 `computed` 过滤，trim + lowercase 后匹配 `id` 或 `display_name`。
- 列表区域独立滚动，渲染全部过滤结果；每一行是一个按钮，点击即 emit `select` 并关闭。
- 已选模型显示选中状态和可访问标签。显示名称为空或等于 ID 时只展示 ID。
- 417 条规模无需服务端分页或虚拟列表；4 MiB 只是上游读取边界，返回 Web UI 的 DTO 仅保留 ID/显示名称。

`SettingsView.vue` 调整：

- `loadAIModels` 请求期间在获取按钮显示加载状态；成功后保存结果并打开窗口，失败保持现有 `aiModel` 和关闭状态。
- 删除 `datalist`，保留普通文本输入和手动填写提示。
- 处理选择事件时将 ID 写入 `aiModel`，关闭窗口并给出非保存型反馈；最终持久化仍由“保存 AI 设置”完成。

## Compatibility and Safety

- API 兼容，不需要数据库迁移。
- 仅放宽模型目录文档的有界读取，不放宽 LLM 输出、请求体或凭据相关限制。
- 选择模型不触发保存或生成请求，避免用户误以为已经持久化。
- 回滚时可恢复旧 UI；后端独立上限改动也可单独回滚，不影响已保存设置。

## Validation

- Go fixture 覆盖 256 KiB < models body <= 4 MiB 成功、models body > 4 MiB 失败、structured body > 256 KiB 失败。
- Web UI 覆盖弹窗接线、搜索规则、点击选择、当前选择和可访问性契约。
- 执行 Server package/full tests，以及 Web UI test/typecheck/lint/build。
