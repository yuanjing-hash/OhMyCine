# Server AI 模型选择窗口

## Goal

修复 OpenRouter 模型列表因响应超过通用 256 KiB 上限而无法读取的问题，并将 Server 设置页的模型 `datalist` 改为独立、可搜索的模型选择窗口，使管理员能够读取全部模型并点击整行完成选择，同时保留手动填写模型 ID 的能力。

## Background

- `https://openrouter.ai/api/v1/models` 在 2026-08-26 返回约 417 个模型、687875 bytes（约 672 KiB），超过 `server/pkg/aiprovider/types.go` 的 `maxStructuredResponseBytes = 256 << 10`。
- `server/pkg/aiprovider/provider.go` 的模型列表和结构化生成共用 `readBounded`，因此模型列表触发 `ai_response_too_large`；服务层又将该错误显示为“AI Provider 返回了无效的结构化结果”。
- `server/webui/src/views/SettingsView.vue` 当前使用空 `datalist`。只有模型列表请求成功后才填充候选，且浏览器下拉行为不明显。
- 现有 `DirectoryPickerDialog.vue` 提供了项目内可复用的弹窗行为基线：Teleport、焦点恢复、Esc 关闭、焦点约束、滚动内容和空状态。

## Requirements

1. OpenAI-compatible 和 Google AI Studio 的模型列表响应使用独立且有界的上限，首版为 4 MiB；结构化生成内容仍保持 256 KiB 上限。
2. “获取模型列表”成功后打开独立模型选择窗口，窗口展示 Provider 返回的全部规范化模型。
3. 选择窗口提供本地搜索，按模型 ID 和显示名称进行不区分大小写的包含匹配。
4. 每个模型以可点击整行呈现；点击后立即回填设置页模型字段并关闭窗口。
5. 当前填写的模型在窗口中具有明确选中状态；模型 ID 与显示名称不同时同时展示。
6. 模型字段继续允许手动填写，不再使用 `datalist`。
7. 获取失败时不打开空窗口，不覆盖当前模型，并显示能区分模型列表过大/响应无效的安全错误文案。
8. 窗口支持 Esc、遮罩关闭、关闭按钮、键盘焦点约束和关闭后的焦点恢复；获取按钮显示加载状态，窗口对无模型和无搜索结果提供明确空状态。
9. 不记录、回显或改变 API Key；现有受控 Base URL、SSRF、超时、重定向和凭据边界保持不变。

## Out of Scope

- 不增加 Provider 端分页、收藏、模型能力标签、价格或上下文长度展示。
- 不自动替换已保存模型，也不在打开窗口时自动保存设置。
- 不改变运行时结构化生成 Schema、提示词或媒体识别策略。
- 不创建浏览器外的原生 Windows 窗口；“新窗口”指页面内模态选择窗口。

## Acceptance Criteria

- [ ] AC1: 介于 256 KiB 与 4 MiB 之间的合法模型列表响应可成功解析，超过 4 MiB 的模型列表仍被拒绝。
- [ ] AC2: 超过 256 KiB 的结构化生成响应继续被拒绝，证明安全上限未被整体放宽。
- [ ] AC3: 点击“获取模型列表”成功后出现模型选择窗口，并显示 API 返回的全部模型。
- [ ] AC4: 搜索可按 ID/显示名称过滤；空搜索恢复完整列表，无匹配时显示空状态。
- [ ] AC5: 点击任意模型整行后，模型 ID 回填到设置表单且窗口关闭；仅选择不会保存设置。
- [ ] AC6: 手动输入模型 ID 仍可用，页面不再依赖 `datalist`。
- [ ] AC7: 当前模型有选中标识，窗口支持 Esc、遮罩、关闭按钮、键盘焦点约束与焦点恢复。
- [ ] AC8: 模型列表失败不会清空当前字段，错误文案不再误称为模型“结构化输出”错误。
- [ ] AC9: Server Go 测试、Web UI 测试、typecheck、lint 和 build 通过。
