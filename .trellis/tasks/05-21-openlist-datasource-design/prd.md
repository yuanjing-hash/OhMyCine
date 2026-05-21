# brainstorm: OpenList DataSource 设计梳理

## Goal

先梳理并确认 Player 侧 OpenList/Alist DataSource 的设计边界，下一步再进入实现。目标是让 Player 在不依赖 Server 的情况下，能够连接 OpenList/Alist，浏览目录、搜索媒体文件，并把可播放文件交给现有 libmpv 播放链路。

## What I already know

* 用户认可按路线图顺序推进，当前优先开发 OpenList DataSource。
* 本轮明确不写业务代码，先阅读并对齐设计。
* Player 设计原则是独立优先，DataSource 统一暴露 `list / search / getDetail / getStreamURL`。
* `docs/architecture/03-player-design.md` 4.4 已定义 `AlistDataSource` 示例：通过 `/api/fs/list` 浏览目录，通过 `/d{path}` 生成播放 URL。
* `docs/architecture/06-roadmap.md` Phase 1.3 对 OpenList/Alist 的任务是 HTTP API 客户端、WebDAV 备选、目录浏览、搜索、播放 URL、连接测试。
* 当前代码的 DataSource 类型已包含 `alist`，但 `DataSourceManager.createDataSource()` 只实现了 `emby`。
* 当前安全设计要求敏感字段不进入普通配置文件，应通过 `credentialRef` 存储。

## Assumptions (temporary)

* MVP 优先使用 OpenList/Alist HTTP API，而不是先实现 WebDAV。
* OpenList 和 Alist 先共用 `alist` DataSource 类型与实现，文案可显示为 `OpenList/Alist`。
* 第一版只做文件级浏览和播放，不立即做 TMDB 刮削、海报墙增强或跨源搜索合并。

## Open Questions

* 已决策：OpenList/Alist MVP 只支持账号登录。用户输入 URL、用户名、密码，Player 调用 `/api/auth/login` 换取 token；不设计手填 token 模式；公开目录/路径密码后续与 WebDAV 能力一起设计。

## Requirements (evolving)

* 支持配置 OpenList/Alist base URL。
* 支持账号登录模式：URL + username + password -> token。
* 支持连接测试。
* 支持根目录和子目录浏览，并映射成统一 `MediaItem`。
* 支持按关键字搜索。
* 支持文件详情的基础信息返回。
* 支持对可播放文件生成 stream URL，并接入现有播放页。
* 敏感凭据通过凭据存储保存，普通 DataSource config 只保存引用。
* 普通 UI 不暴露手填 token、公开目录或路径密码模式。

## Acceptance Criteria (evolving)

* [~] 用户能在 Player 设置中添加 OpenList/Alist 数据源并测试连接。实现已完成，待真实服务验证。
* [~] 用户能从侧栏进入该数据源并浏览目录。实现已完成，待真实服务验证。
* [~] 视频文件能从 OpenList/Alist 进入现有播放页播放。实现已完成，待真实服务验证。
* [x] 普通配置持久化中不保存 token、用户名、密码等敏感字段。
* [x] 设计状态同步回路线图。

## Definition of Done (team quality bar)

* Tests added/updated where practical.
* `npm run typecheck` / `npm run lint` passes for Player changes.
* Docs/roadmap updated if behavior or scope changes.
* Credential and URL handling reviewed against security design.

## Out of Scope (explicit)

* 本轮不实现业务代码。
* 第一版不实现 Server 侧 OpenList driver、302 proxy 或 STRM。
* 第一版不实现完整 TMDB 刮削和本地海报缓存。
* 第一版不实现 CloudDrive2/WebDAV 通用层，除非后续确认 HTTP API 路线不可用。
* 第一版不实现手填 token 模式。
* 第一版不实现公开目录/共享路径/路径密码模式；这些能力留到 WebDAV 相关任务统一设计。

## Technical Notes

* Design entry: `docs/architecture/03-player-design.md` section 4.4.
* Roadmap entry: `docs/architecture/06-roadmap.md` Sprint 1.3.
* Security entry: `docs/architecture/07-security-design.md` sections 6.1, 6.2, 7.
* Research: `research/openlist-auth-patterns.md`.
* Existing interface: `player/src/services/datasource/types.ts`.
* Existing manager: `player/src/services/datasource/manager.ts`.
* Existing credential pattern: `player/src/services/datasource/credentialStore.ts`.

## Implementation Notes

* Added `AlistDataSource` with account-login-only HTTP API support.
* Added structured Alist credential envelope in the shared credential store.
* Settings now supports Emby and OpenList/Alist login-style add/edit flows.
* Added safe `credentialVersion` config metadata so re-login forces DataSource reinitialization without persisting secrets.
* Static verification passed via Player typecheck, lint, build, and `git diff --check`; live OpenList/Alist service verification remains pending.
