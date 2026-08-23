# Implementation Plan

1. 检查 Player 播放加载生命周期，改造 loopback bridge 为会话内多 token，并补充双轨、并发 Range、clear、未知 token 与跨源 Header 测试。
2. 梳理现有 Server 媒体库 DTO、扫描事件、插件在线库和图片网关，定义向后兼容的 artwork contract。
3. 实现 Server 通用候选选择、内容摘要、16:9 渲染、原子缓存与 fallback；接入本地及 115 媒体库。
4. 扩展插件 host/SDK 契约并更新 Bilibili 插件，使其提供受控候选图片；接入 plugin feed revision 刷新。
5. 实现 Player 独立媒体库封面生成与缓存，并更新 ServerDataSource 映射、卡片展示和 revision 刷新。
6. 更新相应 API/架构文档和安全契约。
7. 执行 Rust 测试与检查、Player typecheck/build、Server Go 测试、插件打包/契约测试和工作树审查。
8. 按 Conventional Commits 提交并推送 `develop`；从最新远端 `develop` 创建新的 Player Beta tag 并验证发布工作流已启动。

## Risky Areas and Rollback Points

- `player/src-tauri/src/mpv/mobile_proxy.rs`：错误回收可能中断当前播放，必须先有多 token 测试。
- 在线插件 asset gateway：不能扩大 SSRF/凭据暴露面，候选只能使用已授权 opaque ref。
- 图片生成依赖：优先复用现有已锁定依赖，避免引入难以跨平台构建的原生库。
- 大媒体库：候选查询必须分页/限量，合成应去抖并限制并发。

## Validation

```powershell
cd player
npm run typecheck
npm run build
cd src-tauri
cargo test
cargo check

cd server
go test ./...
```

还需执行仓库已有的插件校验/打包命令与目标测试，并确认 `git diff --check`、发布 tag 对应最新 `origin/develop`。
