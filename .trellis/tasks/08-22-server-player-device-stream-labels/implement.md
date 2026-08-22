# Implementation Plan — Player 设备管理与 Server 播放来源标识

## 1. Server management API

- [x] 在浏览器 protected group 注册 `GET /api/v1/player-devices` 与 `DELETE /api/v1/player-devices/:id`，分别要求 `connections.read` 与 `connections.update`。
- [x] 复用现有设备 handler、DTO 和 AuthService，保持当前账号范围、`no-store` 和撤销审计。
- [x] 扩展 router 测试：Cookie Session 成功、权限拒绝、CSRF 拒绝、Player Bearer 隔离、撤销后 token 失效、响应无敏感字段。

## 2. Server Web UI

- [x] 在 API types 中加入安全的 Player device summary。
- [x] 在 PlayersView 独立加载设备列表，增加刷新、加载/失败/空状态和设备卡片。
- [x] 增加确认撤销、单卡 busy、成功/失败 Toast，并确保 Emby 卡片不受设备请求失败影响。
- [x] 添加可提取纯函数或静态契约测试，覆盖设备时间/客户端标签和管理路由；避免为了测试引入重型组件挂载依赖。

## 3. Server media delivery DTO

- [x] 为 `PlayerMediaVersion` 添加有界 `delivery_kind`。
- [x] 在 local 与 pan115 playability 分支分别返回 `server_stream` 与 `server_redirect`。
- [x] 更新 service/router 测试，断言本地/115 类型正确且没有路径、artifact 或临时 URL 泄露。

## 4. Player DataSource and detail labels

- [x] 扩展 Server version runtime parser，未知/缺失交付类型安全降级。
- [x] 扩展通用 `MediaSourceOption` 的可选来源/交付展示字段。
- [x] 删除 `isStrm: version.playable`，Server 自有线路设置准确来源和交付类型。
- [x] 更新 MediaDetailView 描述顺序；准确来源存在时不再显示泛化“远程”。
- [x] 扩展 `verify-server-datasource.ts`，覆盖 local、115、旧 Server 和真实 STRM 不回归。

## 5. Documentation and consistency

- [x] 更新 Server/Player 架构或 Trellis spec 中 Player 设备管理与 delivery kind 契约，避免再次混淆 playable、STRM 与 transport。
- [x] 搜索所有 `isStrm: version.playable`、`PlayerMediaVersion` 和设备占位文案，确认没有遗漏。

## 6. Validation

- [x] `cd server; go test ./...`
- [x] `cd server; go vet ./...`
- [x] `cd server/webui; npm test`
- [x] `cd server/webui; npm run typecheck`
- [x] `cd server/webui; npm run lint`
- [x] `cd server/webui; npm run build`
- [x] `cd player; npm run verify:server-datasource`
- [x] `cd player; npm run typecheck`
- [x] `cd player; npm run lint`
- [x] `cd player; npm run build`
- [x] 检查 git diff，保留并排除已有 mobile schema 和旧未跟踪 Trellis 内容。

## 7. Commit, push, and Player Beta release

- [ ] 按 Server、Player/前端、文档/任务工件的实际边界创建中文 Conventional Commits，不纳入既有 dirty 文件。
- [ ] 推送 `develop`，重新 fetch 并确认 `HEAD == origin/develop`。
- [ ] 从最新远端 `develop` 触发 Player `v1.1.11` Beta 发布；不创建 Server tag/Release。
- [ ] 等待 workflow 完成并核实 GitHub prerelease、更新清单以及 Windows/Android 资产。

## Rollback points

- 管理端 API/UI 与媒体 delivery contract 分开组织改动，任一部分回退不改变另一部分。
- 不修改数据库 schema；失败时可删除新路由/字段并恢复 UI，不需要数据回滚。
- Player 发布失败时不移动已发布 tag；先修复 develop 并按既有发布流程处理，Server 仍保持只推送不发版。
