# 技术设计

## 边界与数据流

```text
内置媒体库 / 插件清单
  → Server 服务层生成安全 artwork URL
  → Player Device API 返回 posterUrl/backdropUrl
  → ServerDataSource 通用映射
  → SourceLibraryView 卡片展示
```

分类导航保持现有虚拟节点模型。Server 服务层先根据真实目录项统计内容，再过滤零计数分类并附加静态分类封面；Player 不自行猜测分类是否为空。

返回行为由 `SourceLibraryView` 暴露当前数据源内部层级状态，并通过一个可取消的应用级返回钩子交给 `BackButton`。内部层级存在时先切换节点；只有位于数据源根节点时才调用 Router 后退。

## 插件静态资源契约

- Manifest 增加可选媒体库 artwork 相对路径字段。
- `.omcp` 归档携带 Manifest 声明的静态图片；打包和 Server 校验使用同一扩展名、路径和大小限制。
- 安装目录保留静态图片；运行时仅按已启用插件及声明路径读取。
- 封面使用包 SHA-256 寻址的公开同源只读路由，返回明确 MIME、不可变缓存与 `nosniff` 头。封面不含用户数据；公开摘要 URL 避免把 Player Bearer 放进 `<img>` URL，读取时仍要求摘要属于当前启用安装并重验托管目录。
- 不允许插件提供 `file://`、绝对路径、远程 URL、SVG 或 HTML；资源路由不得暴露安装目录。

## 兼容性

- Manifest artwork 字段可选，旧插件无需重新打包即可继续安装。
- Player 的 artwork 字段已是可选字段，旧 Server 响应继续使用占位图。
- 分类标识、内容查询参数和已有媒体详情协议保持不变。

## 发布与回滚

- Bilibili 插件协议变更后从 0.3.2 升至 0.3.3，并更新 Registry 的版本、包 URL 与 SHA256。
- Player 发布 v1.1.15 Beta；标签只能创建在已推送的 `origin/develop` 最新提交上。
- 若插件发布失败，不回滚 Server/Player 的兼容字段；旧插件可继续无封面运行。
