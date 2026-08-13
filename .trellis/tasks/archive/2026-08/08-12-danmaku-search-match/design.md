# 技术设计：弹幕搜索与手动匹配

## 状态

已定稿，等待用户批准最终实施计划。

## Architecture

- Rust/Tauri：扩展 `commands/danmaku.rs`，修正自动匹配参数，并新增只读剧集搜索命令。
- TypeScript service：为搜索请求、响应和分组结果增加类型与严格解析。
- `useDanmaku`：统一自动识别、候选选择、手动搜索选择和评论加载状态，保持播放失败隔离。
- Player UI：设置顶部提供搜索入口；桌面/移动播放器开关使用不同状态 SVG。
- 搜索 UI：复用字幕搜索对话框的响应式交互模式，桌面端使用 modal、移动端使用全屏页面，但保持独立弹幕数据模型与组件。

## Data Flow

```text
安全媒体显示名
  -> 去路径与扩展名
  -> POST /api/v2/match
  -> 可靠匹配 -> GET /api/v2/comment/{episodeId}
  -> 失败/不理想 -> 用户打开搜索

搜索关键词 + 可选集号
  -> GET /api/v2/search/episodes?v2=true
  -> 按作品分组展示剧集
  -> 用户选择 episodeId
  -> GET /api/v2/comment/{episodeId}
  -> 更新当前匹配并显示弹幕
```

## API Contracts

- 自动匹配的 `fileName` 是不含目录和扩展名的 stem。
- 搜索关键词在前后端边界 trim，并执行最少两个字符校验。
- 搜索响应不得作为可信数据直接渲染；parser 只接受合法的作品、剧集和数值 ID。
- 评论请求继续使用 `withRelated=true`、`chConvert=1`。
- 官方 API Header 只发送到已校验官方源，不随 302 转发到评论 CDN。

## State and Persistence

- MVP 将用户手动选择保存在当前播放会话状态中；切换媒体后重新匹配。
- 暂不新增持久化数据库迁移，避免错误映射长期污染播放记录。

## Compatibility

- 不改变 libmpv 链接和打包路径。
- 新 Rust 代码使用 reqwest/serde 现有抽象，不引入平台限定依赖。
- 官方凭据继续只由编译期受控环境注入 Rust 后端，不出现在 Vue bundle。

## Error Handling

- 区分输入校验失败、无结果、远端 API 错误、响应格式错误和评论加载错误。
- 所有失败均局限于弹幕功能，视频播放状态不受影响。
- 错误文本不包含 URL query、Header、密钥或本地绝对路径。

## Trade-offs

- 不为每次搜索额外调用番剧详情；搜索结果足够选择时减少一次配额与网络延迟。
- 暂不持久化手动映射，交付范围更小且避免缺少撤销/纠错机制时保存错误匹配。
- 独立搜索窗口比内嵌设置面板多一次界面切换，但能完整展示按作品分组的剧集结果，并改善移动端可用性。
