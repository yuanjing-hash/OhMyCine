# 插件 SDK 与 Host API

公共契约位于仓库的 `plugin-sdk/`，它是插件作者和 Server 运行时之间唯一稳定边界。官方 Bilibili 插件也必须使用这套 SDK，不能导入 Server `internal/` 包。

## 调用模型

插件通过版本化 JSON ABI 接收标准操作：

- `site.navigation`
- `site.feed`
- `site.search`
- `site.detail`
- `media.playback`
- `media.download_plan`

返回值必须使用 SDK 的 `MediaWork → MediaSegment → MediaVersion → StreamVariant` 层级。清晰度只属于当前媒体版本，不能被当成选集或媒体版本。

## Host 能力

Host API 仅提供受控 HTTP、结构化日志、插件私有存储和受控时间。HTTP 请求只能访问已授权域名，具有超时、重定向、响应体和并发限制；凭据通过句柄附加，不把 Cookie/Token 明文返回插件或 Player。

`PlaybackPlan` 和 `DownloadPlan` 中的 URL 使用短时不透明引用。插件不能选择本地绝对路径、提供任意命令或直接写入媒体库；真实下载由 Server 下载到任务暂存区，DASH 合流后进入现有识别、整理和入库流水线。

## 错误与日志

插件返回稳定错误码，例如 `not-authenticated`、`rate-limited`、`upstream-unavailable`、`timeout` 和 `invalid-response`。Server 对错误和返回 DTO 进行二次校验，不把插件栈、路径、Cookie、Token 或上游签名 URL 发给 Player。

日志的 `plugin_id`、`connection_id` 和 operation 由宿主绑定，插件不能覆盖，并统一经过 Server 脱敏和轮转。
