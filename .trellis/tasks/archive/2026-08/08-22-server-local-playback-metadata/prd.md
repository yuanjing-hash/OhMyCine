# 修复 Server 本地播放与媒体详情元数据

## Goal

修复 Player 连接 OhMyCine Server 后，本地媒体库电影无法播放、本地剧集无季集、详情演职人员和剧照缺失的问题，同时补强 Emby 详情映射。修复完成后推送 Server 代码到 `develop`，并从最新远端 `develop` 发布下一版 Player Beta；不发布 Server 版本。

## Background

- `server/internal/services/player_media.go:153-196` 目前只把启用 signed STRM 的 115 媒体库判定为可直出，导致本地 Storage 的所有版本均为 `playable=false`。
- `server/internal/services/player_media.go:315` 的 Player stream 解析只支持 active managed STRM artifact，缺少本地文件响应路径。
- `server/internal/services/player_media.go:37-52` 仅投影标题、年份、简介和单张海报/背景；`MediaLibraryRecognition.MetadataJSON` 中已经存在的 TMDB 导演、演员、类型、评分、时长、标识等没有进入 Player DTO。
- `server/pkg/metadata/tmdb/client.go:94-122,501-594` 的快照只有单张背景图，详情请求只追加 `credits,external_ids`，没有持久化 TMDB images 列表。
- `player/src/services/datasource/server.ts:42-57,201-234,311-338` 没有解析和映射完整详情字段。
- `player/src/services/datasource/emby.ts:60-69,1702-1718,2456` 的通用图片查询限制为一张，人物类型匹配严格区分大小写。
- `player/src/views/MediaDetailView.vue:1104` 在非 Emby 来源也硬编码“Emby 暂未返回可选择的分集”。

## Requirements

### R1 — Server 本地媒体安全播放

- 本地 Storage 中有效、启用且属于可读媒体库的普通媒体文件应返回 `playable=true` 和现有受 Bearer 保护的 stream endpoint。
- `GET`、`HEAD` 和 Range 请求必须正确支持本地直出；视频数据由 Server 直接响应，不向 Player 暴露绝对路径。
- 本地文件只能从 `Storage.RootPath + MediaLibrary.RelativeRoot + entry.RelativePath` 的受控边界解析。
- 必须逐段拒绝路径穿越、symlink、Windows junction/reparse point、目录、越界路径、已停用 Storage/媒体库和不存在文件。
- 115 signed STRM → 302 的既有行为、鉴权和多设备逻辑不得回归。

### R2 — Server 详情元数据契约

- Player 媒体 DTO 应返回已有 TMDB 快照中的原始标题、评分、标语、时长、类型、导演、编剧、演员、IMDb/TMDB ID 和剧照列表。
- API 只能返回稳定的元数据与 TMDB 图片身份，不返回 Server 绝对路径、凭据、115 标识或签名/CDN URL。
- 旧版快照保持兼容：没有 images 数组时，现有 `BackdropPath` 至少作为一张剧照使用。
- 新的 TMDB 详情抓取应请求并限量、清洗、去重持久化多张背景/剧照路径；重新扫描或刷新后的媒体可获得完整图片集。

### R3 — Player ServerDataSource 映射

- ServerDataSource 应运行时校验新增字段并映射到通用 `MediaDetail`，包括评分、时长、类型、导演、演员、原始标题、外部 ID 和 `stills`。
- 本地电影应出现可播放线路；本地剧集应按版本恢复季和分集，并可以选择具体分集播放。
- Server 断开或字段缺失时仍保持 Player 独立可用，旧 Server 响应不应造成页面崩溃。

### R4 — Emby 详情补强

- Emby 详情查询应允许获取有限数量的 backdrop，而列表查询继续保持轻量。
- `People.Type` 按大小写不敏感匹配，名称清洗、去空和去重后映射导演与演员。
- Emby 电影/剧集详情能展示已有演职人员和多张剧照；缺失字段时保持空值兼容。

### R5 — 来源正确的空状态

- Server 本地剧集没有可选分集时不得显示 Emby 专属文案；空状态应根据当前来源显示准确、通用的说明。

### R6 — 提交与发布

- 所有产品改动和本任务工件使用 Conventional Commits，提交到并推送 `develop`。
- 保留并排除已有的两个 Tauri mobile schema 修改和旧未跟踪 Trellis 目录，不把它们混入本任务提交。
- Server 只提交/推送代码，不创建 Server tag 或 Release。
- Player 发布下一版 Beta（当前最新为 `v1.1.9`，预期 `v1.1.10`），发布源必须是最新 `origin/develop`；等待 GitHub Release workflow 完成并核实资产。

## Acceptance Criteria

- [ ] 本地电影详情至少有一个 `playable=true` 版本，Player 主播放操作可用并能实际打开媒体。
- [ ] 本地剧集详情返回正确季/集结构，Player 可选分集且不显示 Emby 专属错误文案。
- [ ] 本地 stream endpoint 的 GET、HEAD、单 Range 和无效 Range 行为正确，响应包含适当状态码、长度、类型及 `Content-Range`。
- [ ] 路径穿越、symlink、junction/reparse point、目录、越界、缺失文件、停用库/Storage 均被安全拒绝。
- [ ] API、错误和日志中不出现本地绝对路径；Bearer 只发往 Server origin。
- [ ] 115 STRM Player 直连仍返回原有 302，现有鉴权及 artifact 校验测试通过。
- [ ] Server DTO 与 Player 详情能展示 TMDB 类型、评分、时长、导演、演员、外部 ID 和剧照；旧快照单背景兼容。
- [ ] Emby People 类型大小写变体可正确映射，多 backdrop 可展示且请求数量有界。
- [ ] Server Go 测试、Player typecheck/lint/build 和相关 Rust/前端测试全部通过。
- [ ] `develop` 已推送；Player 新 Beta Release 成功且包含预期 Windows/Android 资产；未创建 Server Release。

## Out of Scope

- 不实现 Player 与 Server 的数据源/凭据双向同步。
- 不改变 Emby/Server 同库去重主策略或 Server canonical work identity。
- 不把本地绝对路径写入 Player 缓存或 API DTO。
- 不新增媒体重新刮削 UI；旧记录通过兼容回退显示单图，完整 images 由后续正常重扫/刷新补齐。
- 不发布 Stable，也不发布 Server 二进制版本。

## Technical Notes

- 本地直出继续复用 `/api/v1/player/media-entries/:id/stream` 和 Player device Bearer，不开匿名文件接口。
- 服务端应复用 `medialibrary.ResolveRoot` 与既有本地路径安全策略，避免出现第二套较弱的边界判断。
- handler 只负责 HTTP GET/HEAD/Range 响应；Storage/Library/Entry 解析与权限判断留在 service 层。
- Blocking open questions: none.
