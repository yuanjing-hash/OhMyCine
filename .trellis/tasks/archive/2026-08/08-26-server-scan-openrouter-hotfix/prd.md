# Server 媒体库扫描与 OpenRouter 热修

## Goal

修复 Server 中两个阻断性缺陷：媒体文件已成功枚举和识别后，扫描结果能够原子地写入 SQLite；OpenAI-compatible AI Provider 能接受 OpenRouter 的标准 `https://openrouter.ai/api/v1` Base URL。修复完成后发布新的 Server Beta，供用户直接升级验证。

## Background

- 用户提供的运行日志显示：Transfer 已成功移动 10 个文件，媒体库扫描发现 10 个文件，形成 1 个识别单元且 `matched=1`，随后在结果持久化阶段返回 HTTP 500。失败后 generation 未推进，重复扫描继续失败。
- 当前持久化事务位于 `server/internal/services/media_library.go:1300`，事务失败日志位于 `server/internal/services/media_library.go:1502`。现有日志只输出通用 `media_library_scan_failed`，没有安全的失败阶段或 SQLite 错误分类，无法从运行日志确认具体失败语句。
- 当前 `server/pkg/aiprovider/network.go:16` 只允许空路径或 `/v1`，因此 OpenRouter 的标准 `/api/v1` 在发起网络请求前就被拒绝。
- 当前 endpoint 拼接逻辑位于 `server/pkg/aiprovider/network.go:82`，扩展 Base URL 支持时必须避免重复拼出 `/api/v1/v1/...`。

## Requirements

### R1 — 修复扫描结果持久化失败

- 使用用户日志所代表的真实链路建立回归场景：Transfer 后一个 10 集 TV 包进入本地媒体库，事件扫描或手动扫描形成一个识别单元并写入 10 个媒体条目。
- 定位并修复事务回滚的真实原因，不得只改变日志或吞掉数据库错误。
- 成功提交后，scan run 必须为成功状态，10 个条目与一个识别记录必须存在，条目必须关联正确的 recognition，library generation 必须推进，媒体变更 outbox 必须符合现有语义。
- 事务仍保持原子性；任一阶段失败时不得留下部分条目、部分 generation 或错误的 ready change。
- 保持现有“识别及外部请求在事务外，短事务内二次校验后提交”的架构。
- 覆盖当前数据库升级链兼容性，尤其是 v25 recognition、v39 change outbox、v48–v51 后的 SQLite schema。

### R2 — 提升扫描事务可观测性

- 为事务内关键阶段增加稳定、安全的 stage，例如配置复核、加载现有条目、写 source assets、写 recognition、写 entries、清理失效数据、推进 generation、写 scan run、写 media change。
- 将底层数据库错误映射为有限的安全分类，例如 configuration_changed、constraint、foreign_key、unique、busy 和 unknown。
- 失败日志只允许包含稳定 stage、错误分类、library ID、scan run ID、generation、scan kind 和耗时。
- 日志、HTTP 响应和 WebSocket 事件不得包含 SQL、绝对路径、文件名、provider ID、metadata JSON 或任何凭据。
- UI/API 继续得到稳定的扫描失败错误，同时错误文案应足够区分“结果提交失败”与识别失败。

### R3 — 兼容 OpenRouter Base URL

- OpenAI-compatible Provider 必须接受并规范化 `https://openrouter.ai/api/v1` 及尾斜杠形式。
- 方案不得只硬编码 OpenRouter hostname；应安全支持规范的 OpenAI-compatible API 路径前缀，并确保 models 与 chat completions endpoint 各拼接一次版本路径。
- 必须继续拒绝非 HTTPS、userinfo、query/force-query、fragment、非 443 端口、私网/环回/链路本地地址以及危险或歧义路径。
- 必须保持 DNS/IP 复核、禁用环境代理、禁止重定向、超时和响应大小限制。
- 测试连接、模型列表和结构化生成都必须使用正确的 `/api/v1/...` 地址。
- Base URL 修复后，模型是否存在、Key 是否有效由 OpenRouter 的真实响应决定，不得把远端认证/模型错误误报成本地 URL 配置错误。

### R4 — 验证与发布

- 新增针对两个缺陷的单元/集成回归测试，并运行 Server Go、Web UI、lint/typecheck/build 和 Windows Server 门禁。
- 验证结束后不得遗留 Server 测试进程。
- 使用 Conventional Commit（中文描述）提交到 `develop`，推送最新远端 `develop`。
- 从最新 `origin/develop` 发布下一个 Server Beta（预计 `v1.1.28`），确认 Windows/Linux Server 产物及 SHA256 资产成功上传。

## Acceptance Criteria

- [ ] 10 集 TV 包在 Transfer 成功后触发扫描，数据库最终存在 10 个 media library entries 和 1 个匹配 recognition，scan run 为 success，generation 正确推进。
- [ ] 同一场景通过手动扫描和事件扫描均可完成，重复扫描保持幂等，不再循环生成失败历史。
- [ ] 人为注入每个关键持久化阶段的失败时，事务完整回滚；日志能显示稳定 stage 与安全错误分类，且不泄露路径、文件名、SQL、provider identity、metadata 或凭据。
- [ ] `https://openrouter.ai/api/v1` 与尾斜杠形式均通过校验，models 请求为 `/api/v1/models`，结构化生成请求为 `/api/v1/chat/completions`，没有重复 `/v1`。
- [ ] 现有 `/v1` 和空路径 OpenAI-compatible Base URL 继续工作；全部既有 SSRF/URL 拒绝测试继续通过。
- [ ] Server 全量质量门禁通过，任务提交并推送到 `develop`，新的 Server Beta 发布资产完整。

## Out of Scope

- Player 离线下载管理、右键/长按下载、离线剧集页面/字幕/弹幕缓存及下载限速设置；Server Beta 发布后另建 Player 任务处理。
- 修改媒体识别算法、TMDB 匹配策略或 AI 识别提示词。
- 测试或复述用户曾公开粘贴的 OpenRouter API Key；该 Key 应由用户撤销并重新创建。
- 放宽到 HTTP、私网 AI endpoint、任意端口或自动跟随重定向。

## Technical Notes

- SQLite 是权威目标，修复不能引入 CGO 或数据库方言特有行为。
- 页面中显示的“新增 10”是事务内预提交计数；事务失败后必须明确视为未提交，而不是部分成功。
- 当前没有阻塞实施的产品决策；具体失败语句由回归测试和阶段化错误包装在实现阶段确定。
