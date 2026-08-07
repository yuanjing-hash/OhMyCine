# Research: Server 首个纵向切片选择

- **Query**: OhMyCine Server 首先应以本地文件、OpenList/Alist、115 或 CloudDrive2 中哪类连接验证架构，如何形成非空骨架的首个可演示闭环，并为下载/转移/PT/追剧保留扩展。
- **Scope**: mixed
- **Date**: 2026-08-07

## Findings

### 结论

首个**生产级连接**推荐选择 **OpenList/Alist**，首个里程碑交付“OpenList/Alist 现有媒体 → 受控扫描 → 生成签名 STRM → Server 302 到上游直链 → 通知 Emby 刷新 → 实际播放”的纵向闭环。

本地文件适合作为驱动契约的参考实现、测试夹具和路径安全测试，但不应单独充当首个产品演示：它无法验证远程凭据、临时播放 URL、302 缓存和云端失败语义。115 和 CloudDrive2 应在该闭环稳定后接入；115 的登录/API 不确定性最高，CloudDrive2 的 gRPC/Token 边界比 HTTP 驱动更重，且 Player 侧尚未完成真实服务实机验证。

### 首驱动比较

| 候选 | 能验证的关键边界 | 首切片风险 | 建议 |
|---|---|---|---|
| OpenList/Alist | 凭据、连接测试、根目录约束、远程列举、文件详情、临时/签名直链、302、STRM | HTTP API 相对稳定；仓库 Player 已有 live-tested 实现可对照 | **首个生产驱动** |
| 本地文件 | 统一文件模型、递归扫描、增量判定、路径/符号链接安全、STRM 文件写入 | 不能证明云盘播放主链路；本地媒体本身通常无需 STRM/302 | 测试/参考驱动，与首切片并行但不作为演示主角 |
| CloudDrive2 | gRPC 适配、API Token、带 Header 的播放请求 | 协议桥和部署依赖更重；真实服务仍待实机验证 | 第二或第三个远程驱动 |
| 115 | Cookie/登录态、专有 API、临时直链 | 当前方案仍提到社区代理 API；Player 仅有占位，接口稳定性最不适合验证自家架构 | OpenList 闭环后再做专项集成 |

### Files Found

| File Path | Description |
|---|---|
| `docs/architecture/02-server-design.md:125` | 定义 `Connections → Storage Destinations → Category Rules` 三层模型及完整媒体闭环。 |
| `docs/architecture/02-server-design.md:241` | 当前云驱动接口一次性要求 List/Get/Upload/DownloadURL/Search/Delete/Quota，首切片若全部实现会造成空能力或伪实现。 |
| `docs/architecture/02-server-design.md:316` | Storage Destination 已承载远程路径和 STRM 输出配置。 |
| `docs/architecture/02-server-design.md:386` | STRM 同步设计含全量、增量、清理和生成代理 URL。 |
| `docs/architecture/02-server-design.md:1084` | 302 引擎通过驱动解析真实下载 URL 并按上游过期时间缓存。 |
| `docs/architecture/06-roadmap.md:334` | Server MVP 目标包含四类存储、三层架构、STRM、302 和配置同步，但当前 Sprint 按横向模块铺开。 |
| `docs/architecture/06-roadmap.md:430` | 同一 Sprint 同时安排多网盘、两个下载器、302、媒体服务器和配置同步，容易延迟首个用户闭环。 |
| `docs/architecture/06-roadmap.md:501` | STRM 与真正的下载→转移→入库闭环被排在更后，说明当前顺序会先形成大量不可演示骨架。 |
| `docs/architecture/07-security-design.md:267` | 302 默认不得公开裸奔，推荐 signed-url；签名覆盖规范化路径和过期时间。 |
| `docs/architecture/07-security-design.md:323` | 本地文件和 STRM 输出必须限定根目录、阻止 traversal/symlink escape。 |
| `docs/architecture/07-security-design.md:548` | Server MVP 的安全底线：登录、凭据加密、代理非匿名、签名/内网白名单、脱敏、根目录约束。 |
| `player/src/services/datasource/alist.ts:122` | Player 已有 OpenList/Alist 初始化、连接测试和 credentialRef/rootPath 处理。 |
| `player/src/services/datasource/alist.ts:258` | 已实际使用 `/api/fs/list`、`/api/fs/get`、`/api/fs/search`。 |
| `player/src/services/datasource/alist.ts:441` | 已实现根目录检查并构建带 sign 的 `/d{path}` 播放地址。 |
| `player/src/services/datasource/clouddrive2.ts:34` | CloudDrive2 需要独立 native bridge：list/search/getStream。 |
| `docs/architecture/06-roadmap.md:250` | OpenList/Alist 已通过本地服务 live test；CloudDrive2 仍待真实服务实机验证；115 仍为占位。 |

### Code Patterns

1. **用一个真实用例反推最小接口，而不是先实现“大而全 Driver”**

   首切片的驱动必需能力只应覆盖：

   - `TestConnection`
   - `ListPage` 或可取消的目录列举
   - `Stat/Get`
   - `ResolvePlayback`（URL、必要 Header、ExpiresAt）
   - 能力声明（例如 `CanUpload`、`CanSearch`、`CanDelete`、`CanServerSideMove`）

   Upload/Search/Delete/Quota 不应成为所有驱动的首期强制方法。否则 Local、OpenList、115、CloudDrive2 会被迫返回 `not implemented`，而服务层无法可靠判断能力。Rclone 的模式是小的基础 `Fs` 接口加 `Features` 中的可选操作，适合 OhMyCine 后续多驱动扩张。

2. **首个闭环必须跨越持久化、外部系统和实际副作用**

   建议演示顺序：

   1. 首次启动创建管理员并登录。
   2. 创建 OpenList/Alist Connection；敏感字段 AES-GCM 加密，读取 API 永不回显秘密。
   3. 测试连接并浏览目录，选择一个受 `rootPath` 限制的 Storage Destination。
   4. 对该目标执行一次手动全量扫描，识别视频文件并保存同步状态/文件指纹。
   5. 在受控 STRM 根目录原子写入 `.strm`；内容为带 `exp`/`sig` 的 Server URL。
   6. Emby Connection 触发库刷新。
   7. 从 Emby 打开该 STRM；Server 验签、规范化路径、调用 OpenList 获取当下直链并返回 302。
   8. 可观察到一次有 run ID 的结果：发现数、生成数、跳过数、失败数、刷新结果；日志不含 token/CDN URL。

   这条链路至少真实验证 Connection、Destination、driver adapter、service orchestration、数据库、文件写入、安全签名、外部刷新和播放，而不是 CRUD 页面或接口占位。

3. **Category Rule 在首切片中落库但只用最小语义**

   现有媒体扫描并不需要下载分类；首切片可只支持“该 Destination 的扫描包含哪些扩展名/相对路径映射”。保留 `destination_id`、priority、match predicate、naming/transfer policy 字段或版本化配置入口，但不要为了展示三层模型先实现完整 TMDB、命名模板和 move/hardlink/copy/symlink。

4. **以持久化 Job/Run 作为后续流水线的扩展缝**

   把首期 `STRMSyncRun` 视为通用 Job 的第一个类型，状态至少包含 queued/running/succeeded/partial/failed/cancelled、input、progress、result、error summary 和幂等键。后续能力接入同一编排边界：

   ```text
   PT/追剧 → 创建 DownloadIntent
           → DownloaderJob
           → TransferJob（应用 CategoryRule + Destination）
           → ImportJob（本地文件或云驱动）
           → STRMSyncJob（若目标需要）
           → RefreshJob / WebSocket event
   ```

   这样首切片不是未来会被删除的特例，而是从 `Import → Notify` 两段切入完整 `Discover → Download → Transfer → Import → Notify` 管线。

### 首个可演示闭环的范围

**必须包含**：Server 启动与迁移、单管理员认证、Connection/Destination 最小 API、OpenList/Alist adapter、Emby adapter、加密凭据、根目录约束、视频扫描、一次手动全量 STRM 同步、幂等/原子写、签名代理、302 上游解析、刷新通知、run 状态与脱敏日志、单元/集成测试及一个可重复的本地演示脚本或说明。

**明确不包含**：115、CloudDrive2、上传、搜索、删除、配额、qBittorrent/Transmission、TMDB/NFO/海报、增量定时任务、无效 STRM 自动删除、Player 配置同步、WebSocket、多用户、PT、追剧、插件。它们保留在产品范围，但不阻塞首个可播放闭环。

### 如何避免空骨架

- 每个 PR 都必须让同一条 use case 更接近可播放结果，不以“目录建完/接口定义完/CRUD 完”为独立里程碑。
- 先用 OpenList adapter 的契约测试驱动接口；再加 Local/Fake adapter 验证同一套服务，不提前创建 115/CD2 空包。
- API 只暴露首切片实际调用的端点；未来端点留在文档/issue，不返回虚假的成功或永久 `501`。
- 数据迁移采用递增版本和显式表约束；不要把 provider 特有字段平铺到 Connection 表，使用 provider type + 加密 credential envelope + 经校验的非敏感 config。
- 同步服务以单文件失败不终止整批、可取消、可重跑且幂等为验收条件；输出临时文件后 rename，避免生成半截 STRM。

### External References

- [MoviePilot](https://github.com/jxxghp/MoviePilot) — 项目自述强调“聚焦自动化核心需求、简化功能和设置”，说明媒体自动化产品应先交付核心业务链而非堆叠所有集成。
- [MediaLinker](https://github.com/Lindasama/MediaLinker) — 以“扫描 → 审核 → 执行”形成可见工作流，并通过单个外部 `process` API 接收下载完成事件；它展示了先完成一个可触发、可返回逐文件结果的垂直闭环，再接下载器的做法。
- [OpenList API docs](https://fox.oplist.org) / [OpenList `fsread.go`](https://github.com/OpenListTeam/OpenList/blob/main/server/handles/fsread.go) — `list/get` 已覆盖列举、详情、`raw_url`/sign 生成，是首个远程只读+播放闭环所需的最小 HTTP 面。
- [Rclone filesystem interfaces](https://github.com/rclone/rclone/blob/master/fs/types.go) / [optional features](https://github.com/rclone/rclone/blob/master/fs/features.go) — 基础接口与可选能力分离；Copy/Move/ListR/About/PublicLink 等以 feature 暴露，而非强迫每个 backend 全实现。

### Related Specs

- `.trellis/spec/backend/directory-structure.md` — handler/service/model 与可复用 provider package 的边界。
- `.trellis/spec/backend/api-guidelines.md` — `/api/v1/` 与统一响应信封。
- `.trellis/spec/backend/security-guidelines.md` — 鉴权、凭据、路径和日志安全要求。
- `docs/architecture/07-security-design.md` — 首切片必须遵守 signed proxy、受控根目录与秘密脱敏。

## Caveats / Not Found

- 仓库当前没有 `server/` 可运行实现，因此结论基于架构文档、已发布 Player 的可复用集成经验和外部项目模式，尚无 Server 代码债务需要兼容。
- OpenList API 仍应通过版本兼容测试固定实际响应结构；不能仅依赖文档示例。
- “首个生产驱动选 OpenList/Alist”不表示降低 115 的产品优先级；它只是先用最可控的远程协议证明 OhMyCine 自身架构，再将 115 作为紧随其后的高价值驱动。
