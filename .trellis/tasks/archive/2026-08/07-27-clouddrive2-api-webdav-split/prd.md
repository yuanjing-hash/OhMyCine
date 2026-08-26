# 拆分 CloudDrive2 原生 API 与通用 WebDAV 数据源

## Goal

修正 Player 数据源协议边界：CloudDrive2 使用官方 gRPC API 与用户创建的 API Token；现有 WebDAV `PROPFIND` + Basic Auth 能力拆分为独立通用 WebDAV 数据源。OpenList/Alist 继续使用自身 HTTP JSON API，不受本任务影响。

## Requirements

- `clouddrive2` 仅接受 CloudDrive2 服务地址、API Token 和用户选择的根目录。
- CloudDrive2 API 请求通过 Tauri Rust 原生 gRPC 客户端执行，使用 `Authorization: Bearer <api-token>` 元数据。
- CloudDrive2 使用 `GetSubFiles` 浏览目录、`GetSearchResults` 搜索、`GetDownloadUrlPath` 获取播放直链及临时请求头。
- API Token 只进入 Player 凭据边界和瞬时 Tauri IPC，不进入普通配置、localStorage、日志、扫描缓存或播放历史。
- CloudDrive2 设置页不显示账号和密码字段，并提示用户创建具有只读文件访问权限的 API Token。
- 新增独立 `webdav` DataSource，使用 WebDAV URL、用户名、密码和用户选择的根目录。
- WebDAV 保留只读 `PROPFIND` 浏览、有限递归搜索、Basic Auth 播放 header 与根目录约束。
- `clouddrive2` 与 `webdav` 都复用 raw source 扫描、分类、海报墙、Home 聚合和全量/增量扫描调度。
- 不实现远端上传、删除、移动、重命名、创建目录或其他写操作。
- 更新 README、Player 设计、安全设计和路线图，明确 CloudDrive2、OpenList/Alist、WebDAV 三者协议边界。
- 完成 TypeScript、lint、Vite、Rust 与 Windows GNU release 构建验证，并生成最新 `ohmycine-player.exe`。

## Security Boundaries

- CloudDrive2 API Token 按敏感凭据处理，普通配置仅保存 `credentialRef`、`credentialVersion`、`rootPath` 等非敏感字段。
- WebDAV 账号密码按敏感凭据处理，禁止嵌入 URL。
- CloudDrive2 gRPC endpoint 与 WebDAV URL 仅允许 `http` / `https`，拒绝 userinfo、query 和 fragment。
- Provider 返回的路径必须规范化并限制在所选根目录内。
- 播放直链和附加 header 只传给 mpv，不显示、不记录、不持久化。

## Acceptance Criteria

- 设置页同时出现独立的 CloudDrive2 和 WebDAV 类型卡片。
- CloudDrive2 表单只要求服务地址和 API Token，可浏览并选择 API 允许范围内的根目录。
- WebDAV 表单要求 WebDAV 地址、账号和密码，可浏览并选择根目录。
- 两类数据源均可进入媒体库、扫描、搜索和生成播放请求。
- CloudDrive2 不再发送 `PROPFIND` 或 Basic Auth。
- OpenList/Alist 仍通过 `/api/auth/login`、`/api/fs/*` 和 `/d/...` 工作。
- 所有自动化检查与 Windows GNU release 构建通过。

## Out Of Scope

- CloudDrive2 文件变更 PushMessage 长连接监听；本轮继续使用现有短间隔增量 polling/diff。
- WebDAV Digest Auth、OAuth、客户端证书或写操作。
- CloudDrive2 账号密码换 JWT 登录模式。
- Server 侧 CloudDrive2/WebDAV driver、STRM 与 302 代理。
- GitHub push、tag 或发布。

