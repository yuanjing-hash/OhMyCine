# Implementation Plan — Server 本地播放与媒体详情元数据

## 1. Server local playback

- [ ] 为 Player entry stream 引入本地文件/302 的判别解析结果，保持 handler 薄层。
- [ ] 复用媒体库根解析和 Windows reparse-point 策略，实现逐段安全本地 entry 打开。
- [ ] 更新 library/version playability 投影：本地安全文件可播，115 artifact 逻辑不变。
- [ ] 在 handler 中用已打开文件的 `http.ServeContent` 支持 GET/HEAD/Range，并保持 302 分支。
- [ ] 添加本地电影、剧集、GET/HEAD/Range、越界、symlink/reparse、目录、停用库/Storage 和路径泄露回归测试。

## 2. Server metadata projection

- [ ] 扩展 TMDB snapshot images 响应、清洗、去重和数量上限；保持旧 Snapshot 兼容。
- [ ] 扩展 Player DTO，并从 Recognition MetadataJSON 投影完整详情及 still paths。
- [ ] 添加 snapshot 解析、DTO 字段、旧单背景回退和不泄露敏感路径的测试。

## 3. Player data sources and UI

- [ ] 扩展 ServerDataSource 可选字段类型、运行时解析和通用详情映射。
- [ ] 补齐本地电影播放线路、本地剧集季集映射的前端测试。
- [ ] 为 Emby 详情使用独立有界多图查询；人物类型大小写不敏感并去重。
- [ ] 添加 Emby People、多 backdrop 和旧响应兼容测试。
- [ ] 修正媒体详情空分集文案，不再对 Server 来源硬编码 Emby。

## 4. Documentation and contract consistency

- [ ] 若 API schema 已覆盖该端点，更新 OpenAPI DTO/响应说明。
- [ ] 更新 Server/Player 架构文档，明确本地 Bearer Range 直出与元数据详情契约。
- [ ] 按调试结论更新相关 Trellis spec，记录本地 playability 不得与 115 artifact 条件耦合。

## 5. Validation

- [ ] `cd server; go test ./...`
- [ ] `cd server; golangci-lint run`（环境可用时）
- [ ] `cd player; npm run typecheck`
- [ ] `cd player; npm run lint`
- [ ] `cd player; npm run build`
- [ ] `cd player/src-tauri; cargo test`
- [ ] `cd player/src-tauri; cargo check`
- [ ] 检查 `git diff`，确认两个既有 mobile schema 与旧未跟踪 Trellis 目录未进入本任务提交。

## 6. Commit and release

- [ ] 在 `develop` 上按 Server/Player/文档边界创建中文 Conventional Commits，必要时使用多个提交。
- [ ] 推送 `develop`，fetch 后确认 `HEAD == origin/develop`。
- [ ] 创建并推送下一 Player Beta tag（预期 `v1.1.10`），不得创建 Server tag。
- [ ] 等待 Player Beta workflow 完成，核实 GitHub prerelease、更新清单及 Windows/Android 资产。

## Rollback points

- 本地 stream 分支与 115 redirect 分支分离提交/逻辑，若路径安全或 Range 回归可单独撤销本地分支。
- DTO/Snapshot 均为向后兼容的可选字段，不执行破坏性数据库迁移。
- 发布前必须完成远端 develop tip 校验；workflow 失败时不移动或重用已发布 tag。
