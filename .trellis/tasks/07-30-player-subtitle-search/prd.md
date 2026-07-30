# 实现播放中字幕搜索与加载

## Goal

在播放控制条的字幕菜单中加入可用的在线字幕搜索，让 Emby 媒体可选择使用 Emby 服务端字幕提供器或 Player 本地字幕提供器，其他数据源直接使用 Player 本地字幕提供器，并把下载结果立即加载到当前 mpv 播放会话。

## Requirements

- 字幕菜单底部增加“搜索字幕”操作，保持字幕开关和已有轨道选择不变。
- Emby 媒体点击搜索后先显示“Emby 搜索 / 本地搜索”来源选择，不默认混合请求两个来源。
- 非 Emby 媒体点击搜索后直接进入本地搜索，不显示无意义的来源选择。
- 搜索面板允许选择常用字幕语言，默认简体中文，并展示提供器、格式、评分、下载量、哈希匹配、听障和强制字幕等可用信息。
- Emby 搜索调用服务端远程字幕 API；选择结果后让 Emby 下载字幕、刷新媒体详情并加载新出现的外部字幕轨道。
- 本地搜索通过 Player 字幕提供器抽象执行，首个提供器为 OpenSubtitles REST API。
- OpenSubtitles API Key 使用现有 Tauri 安全凭据边界保存；普通设置不得保存、回填或记录密钥。
- 本地搜索优先使用媒体 IMDb/TMDB 标识；缺少标识时使用标题、年份、季和集信息查询。
- OpenSubtitles 下载链接由 Rust 原生 HTTP 命令获取和下载到 Player cache 字幕目录，限制响应体大小、文件扩展名和目标路径。
- 下载完成后通过 `mpv_add_subtitle` 立即加载，并刷新当前字幕轨道状态。
- 搜索、下载和加载失败必须显示可理解提示，不得泄漏 Token、API Key 或带签名下载 URL。
- 更新 Player 设计、路线图和 Trellis 可执行规范。

## Acceptance Criteria

- Emby 播放中点击“搜索字幕”先出现来源选择，选择 Emby 后可搜索服务端字幕。
- Emby 搜索结果下载后出现在当前字幕轨道中并被选中，无需退出播放页。
- Emby 播放选择本地搜索或其他数据源播放点击搜索时，可使用已配置的 OpenSubtitles 提供器。
- 未配置 OpenSubtitles API Key 时显示前往设置的明确提示，不发起匿名或不受控网站抓取。
- 本地字幕下载只写入 Player cache 字幕目录，文件名和路径不能由远端任意控制。
- TypeScript、lint、Vite、Rust 测试、Windows GNU check 和 release EXE 构建通过。

## Out Of Scope

- 抓取无公开稳定 API 的字幕网站。
- 自动上传字幕、自动给媒体目录写入字幕文件或修改远端媒体文件。
- Jellyfin 原生字幕提供器支持。
- 字幕自动翻译、OCR 或时间轴校准。
- GitHub push、tag 或发布。
