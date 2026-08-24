# 实施计划

1. 修正顶层封面层级
   - 移除物理媒体库和插件在线媒体库顶层动态装饰。
   - 补充回归测试，确认固定封面与 revision 稳定。
2. 实现 Server 物理分类风格 3
   - 扩展分类 DTO。
   - 增加分类作用域候选查询与装饰服务。
   - 将生成器升级到 1920×1080、最多九张、3×3 倾斜列、渐变、圆角和阴影。
   - 覆盖分类隔离、尺寸、缓存和签名测试。
3. 实现插件导航分类封面
   - 扩展通用导航节点和候选请求作用域。
   - Bilibili 按 route/branch 作用域提供真实候选。
   - 覆盖严格响应校验、同源输出和失败回退测试。
4. 实现 Player 独立分类风格 3
   - 将四宫格替换为风格 3 组件/样式。
   - 将候选上限提升为九张并保持稳定 revision。
   - 验证 Server 顶层固定图和 Emby/Jellyfin provider 图不受影响。
5. 文档与质量门禁
   - 更新插件在线媒体与 Player 设计契约。
   - 运行 Server `go test ./...`、`go vet ./...`。
   - 运行插件 SDK/Bilibili 测试、WASM 构建和打包校验。
   - 运行 Player artwork 回归脚本、typecheck、lint、build。
6. 交付
   - 按 Conventional Commits 提交并推送 `develop`。
   - 发布下一版 Player Beta；插件有变更时发布下一版 Bilibili 插件。

## 风险点与回滚点

- `library_artwork.go` 的像素渲染与缓存键是性能/兼容风险，先用单元测试锁定尺寸、摘要和候选上限。
- 插件导航协议必须保持旧插件兼容；新增字段均为可选，请求 scope 不改变无 scope 的旧行为。
- Player `MediaCard` 是共享组件，渲染条件必须限定到 `artworkCandidates`，避免覆盖 provider 单图。
- 发布前必须检查工作树，只提交本任务文件，保留用户现有未跟踪任务与 `server/.tmp/`。
