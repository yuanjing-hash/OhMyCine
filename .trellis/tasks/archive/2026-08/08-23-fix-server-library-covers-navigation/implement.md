# 实施计划

1. 审计 Player 的 Server 浏览节点和全局返回按钮，补齐可复用的内部层级返回机制及回归检查。
2. 修改 Server 媒体库 DTO/服务：过滤零内容分类，并为媒体库与分类提供同源静态封面。
3. 扩展 Plugin SDK Manifest、打包与校验契约，使 `.omcp` 可安全携带声明的 PNG/JPEG/WebP artwork。
4. 增加 Server 插件静态资源读取与 Player Device 鉴权路由，覆盖路径穿越、MIME、大小和旧插件兼容测试。
5. 为官方 Bilibili 插件制作原创静态封面，更新 Manifest 与版本并构建插件包和 Registry。
6. 更新 Player 通用 Server 数据源映射和分类/媒体库卡片展示，修正逐层返回。
7. 更新跨层 Trellis 代码规范和必要的 API/架构文档。
8. 运行 Server、Player、Plugin SDK、Bilibili 插件全量检查和发布前校验。
9. 提交并推送 `develop`，发布 Bilibili 插件 0.3.3 和 Player v1.1.15 Beta，核验远端资产。

## 重点验证

- `go test ./...`、`go vet ./...` 和 Server Web UI 构建。
- Player `verify:server-datasource`、typecheck、lint、build 及相关静态回归脚本。
- Plugin SDK typecheck、测试、fixture 打包与非法资源验证。
- Bilibili `cargo fmt --check`、`cargo test`、`cargo clippy -- -D warnings`、WASM 构建。
- Git 标签 SHA 等于最新 `origin/develop`，Player Windows/Android Beta 资产与插件 Registry 均可下载。

## 风险与回滚点

- 静态资产归档变更可能破坏插件更新路径，因此初始化安装和热更新必须比较输出结构。
- 返回钩子必须在页面卸载时注销，避免其他页面被陈旧回调拦截。
- 图片资产不得直接复制参考项目，需使用 OhMyCine 自有设计与许可安全的构图。
