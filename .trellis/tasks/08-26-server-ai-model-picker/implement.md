# Server AI 模型选择窗口实施计划

## Implementation

- [ ] 在 `server/pkg/aiprovider` 拆分模型列表与结构化生成响应上限，保持生成结果 256 KiB，模型列表设为 4 MiB。
- [ ] 补充 OpenAI-compatible/Google 模型列表和结构化生成的边界回归测试。
- [ ] 调整 AI 模型列表探测的安全错误映射，使列表过大/无效响应不再显示为“结构化输出无效”。
- [ ] 新增 `server/webui/src/components/AIModelPickerDialog.vue`，实现搜索、全量滚动列表、整行选择、当前项标记和完整模态键盘行为。
- [ ] 更新 `SettingsView.vue`：成功获取后打开窗口、点击回填、保留手输、移除 `datalist`、失败不破坏当前值。
- [ ] 增加 Web UI 测试，覆盖弹窗交互合同和设置页接线。
- [ ] 将模型列表与结构化生成的独立响应上限写入后端安全/管理端规范。

## Verification

- [ ] `cd server; go test ./pkg/aiprovider ./internal/services ./internal/httpserver`
- [ ] `cd server; go test ./...`
- [ ] `cd server/webui; npm test`
- [ ] `cd server/webui; npm run typecheck`
- [ ] `cd server/webui; npm run lint`
- [ ] `cd server/webui; npm run build`
- [ ] Windows 本机启动隔离验证：OpenRouter 模型列表可打开、搜索、选择并回填，不读取或覆盖现有用户配置之外的数据。

## Review and Rollback Points

- 检查所有 `readBounded` 调用点，防止误把 4 MiB 应用于生成响应。
- 检查错误响应和日志不包含上游正文、API Key 或 Authorization。
- 检查弹窗关闭后的焦点恢复以及选择不自动保存。
- 若 UI 回归，可回退弹窗接线并保留后端独立模型列表上限修复。
