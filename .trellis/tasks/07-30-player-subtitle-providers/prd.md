# 扩展 Player 本地字幕提供器

## Goal

在现有播放中本地字幕搜索基础上，支持 OpenSubtitles 账号登录、射手网和迅雷字幕，并保持凭据、路径与下载地址安全边界。

## Requirements

- OpenSubtitles 继续要求应用 API Key，可选保存账号和密码并通过 `/login` 获取短期 JWT。
- OpenSubtitles 密码只保存在 Player 凭据库；JWT 只缓存在 Rust 进程内，不写入设置、数据库或日志。
- 射手网使用本地视频四段 MD5 内容哈希，通过固定 HTTPS API 搜索。
- 迅雷字幕使用本地视频三段 SHA-1 CID，通过固定查询接口搜索；由于查询接口仅提供 HTTP，默认关闭并在设置中显示风险说明。
- 射手网和迅雷只对 Player 可访问的本地文件启用。绝对路径只传入 Rust 哈希命令，不发送给外部服务。
- 外部服务只接收内容哈希、视频文件名和语言。不得发送数据源凭据、播放 URL 或本地目录。
- 字幕下载地址保存在 Rust 短期内存状态中，Vue 只持有不透明引用。
- 下载仅允许固定提供器域名、限定扩展名、大小和重定向次数，并写入共享 `cache/subtitles`。
- 本地搜索并行调用已启用且适用于当前媒体的提供器；单个提供器失败不影响其他提供器结果。
- 设置页提供明确启用开关、OpenSubtitles API Key/账号状态和保存反馈。
- 设置页打开时真实探测 Tauri 凭据命令并清理历史持久化故障标记，避免 SQLite 正常时仍显示内存降级误报。
- 更新 Player 设计、安全设计、路线图和 Trellis 规范。

## Acceptance Criteria

- OpenSubtitles API Key-only 模式继续工作。
- 配置账号密码后会先验证登录，并在搜索/下载时携带进程内 JWT。
- 旧版 OpenSubtitles API Key 凭据可以无损读取并升级。
- 本地视频搜索可返回射手网和迅雷结果，远程媒体不会向这两个哈希提供器发送请求。
- 射手网和迅雷结果下载后可立即加载到 mpv。
- HTTP/非固定域名、超大响应、未知扩展名和过期下载引用被拒绝。
- TypeScript、lint、Vite、Rust 测试、Windows GNU check 和 Windows release EXE 构建通过。

## Out Of Scope

- 为远程媒体读取 Range 内容并计算射手/迅雷哈希。
- 将字幕写回媒体目录或上传到媒体服务器。
- 网页抓取型字幕站。
