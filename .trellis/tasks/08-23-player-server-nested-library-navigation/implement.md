# 实施计划

## 顺序

1. 先完成并验证 `08-23-fix-bilibili-online-playback`，避免导航改动干扰播放故障定位。
2. 再完成 `08-23-plugin-nested-catalog-navigation` 的 Server 契约、标准分类接口和 Player 导航。
3. 更新官方 Bilibili 插件为层级导航，并做跨组件集成验证。

## 集成检查清单

- [ ] Server 标准媒体库分类摘要与 `category` 过滤契约完成。
- [ ] 插件 Manifest/运行时/契约同时兼容 v1 flat 与 v2 hierarchical。
- [ ] Server 为节点签发并验证绑定媒体库和深度的 token。
- [ ] Player ServerDataSource 支持标准分类节点与插件节点 ID。
- [ ] SourceLibraryView 根页、面包屑、返回、刷新和错误态符合信息架构。
- [ ] Bilibili 至少实现“番剧 → 地区”两级动态导航，推荐/热门保持叶节点。
- [ ] 首页贡献、搜索、历史、详情、播放、下载不因导航层级改变而回归。
- [ ] 日志能区分 `site.navigation`、`site.feed`、`media.playback` 及播放解析/资产注册阶段。

## 验证

```powershell
cd server
go test ./internal/plugins/... ./internal/services/... ./internal/httpserver/...
go test ./...

cd ..\plugins\official\bilibili
cargo test

cd ..\..\..\player
npm run typecheck
npm run lint
npm run build
```

真实冒烟：

- 使用当前已登录 Bilibili 连接播放截图中的视频，确认视频、音频、弹幕和清晰度切换。
- 在 Player 中依次验证 Server → 本地媒体库 → 外语电影 → 作品。
- 验证 Server → Bilibili → 番剧 → 日本 → 内容，并逐级后退。
- 安装一个仅返回 v1 单层数组的测试插件，确认仍能浏览。

## 回滚点

- 播放修复单独提交，可独立回滚。
- Server v2 协议兼容提交先落地且默认关闭；Bilibili Manifest 开启后才启用层级模式。
- Player 改动不迁移或删除本地数据源、历史和凭据。
