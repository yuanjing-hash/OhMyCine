# Bug Analysis: Emby 收藏写入成功但 Player 仍显示未收藏

## 1. Root Cause Category

- **Category**: B/D/E — 跨层协议错误、集成测试缺口、隐式假设
- **Specific Cause**: 收藏写入使用了正确的 `/Users/{UserId}/FavoriteItems/{Id}`，但读回状态错误地请求 `Fields=UserData`。Emby 官方 OpenAPI 中 `UserData` 不是合法的 item field；收藏成员关系应通过 `Filters=IsFavorite`，单条判断再加 `Ids={Id}`。收藏页还把提供方请求失败吞成空数组，掩盖了协议错误。静态测试只检查方法名存在，没有执行真实 HTTP 写入→读回→列表链路。

## 2. Why Previous Fix Failed

1. 只补了 `getFavoriteState()` 和 `listFavorites()` 的接口形状，没有用 Emby 兼容服务执行真实请求。
2. 菜单查询失败时回退到卡片快照；快照中的 `favorite=false` 实际表示“响应没有返回状态”，却被当成权威未收藏。
3. 收藏页通过 `.catch(() => [])` 把错误显示成空收藏，导致运行时没有可诊断反馈。

## 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Architecture | 收藏写入后保留短期状态覆盖，抵抗 Emby 索引延迟 | DONE |
| P0 | Protocol | 使用 `Filters=IsFavorite`，单条判断同时使用 `Ids` | DONE |
| P0 | Test | 新增真实 HTTP 写入、读回、列表和短暂旧列表集成回归 | DONE |
| P1 | UI | 收藏页明确显示提供方加载错误与重试，不再伪装为空 | DONE |
| P1 | Spec | 将 Emby 收藏读写契约写入前端规范 | DONE |

## 4. Systematic Expansion

- **Similar Issues**: Emby 已播放状态、继续观看和其他 UserData 消费方也必须区分“明确 false”和“字段未返回”。
- **Design Improvement**: 提供方拥有的布尔状态需要独立成员查询，不能仅依赖任意列表卡片的可选快照。
- **Process Improvement**: 新增提供方写操作时，必须有一个本地 HTTP 兼容服务覆盖写入→权威读回→聚合页面的完整闭环。

## 5. Knowledge Capture

- [x] 更新 `.trellis/spec/frontend/component-guidelines.md`
- [x] 新增 `verify:emby-favorites` 集成回归
- [x] 收藏页保留可诊断错误
- [x] 仓库不存在 `src/templates/markdown/spec/` 镜像目录，无需同步模板
