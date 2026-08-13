# 实施计划：弹幕搜索与手动匹配

## Preconditions

- 用户已确认搜索结果采用桌面 modal、移动端全屏的独立响应式窗口。
- 展示最终规划摘要，并在后续消息获得明确实施批准。
- 执行 `task.py start`，读取 `trellis-before-dev` 与 Player/安全规格。

## Checklist

1. 修正 Rust 自动匹配请求的文件 stem 规范化与参数构造，补充边界测试。
2. 在 Rust 命令层实现 `/api/v2/search/episodes` 只读搜索，复用鉴权、Base URL、超时、大小限制与日志脱敏。
3. 扩展 TypeScript 类型、严格 parser 和 client 调用，并覆盖畸形/空结果测试。
4. 重构 `useDanmaku` 的自动匹配选择逻辑，加入手动搜索、选择和当前匹配同步。
5. 增加弹幕搜索选择 UI，并接入设置顶部按钮及桌面/移动响应式布局。
6. 将桌面端和移动端“弹”文字按钮替换为开启/关闭形态不同的 SVG 图标，验证无障碍属性。
7. 更新 `verify-danmaku.ts` 与弹幕规格，覆盖 API 契约、安全边界和只读约束。
8. 运行格式化、类型检查、Lint、前端 build、Rust tests/check 以及 Tauri Windows 构建。
9. 使用现有 `develop` WSL 环境或等价 Linux 检查验证 Linux 编译路径，并审查 GitHub Actions diff，确保云端构建不受影响。

## Validation Commands

```powershell
cd player
npm run typecheck
npm run lint
npm run build
npm run verify:danmaku
cargo test --manifest-path src-tauri/Cargo.toml
cargo check --manifest-path src-tauri/Cargo.toml
```

实际脚本名称以 `player/package.json` 为准；Tauri Windows 完整构建使用仓库现有 Windows 命令并采用隔离测试 profile。

## Risk and Rollback Points

- 官方搜索返回结构可能随 `v2` 版本含可选字段，parser 应允许无害扩展但拒绝缺失核心 ID。
- 自动匹配选择策略改变可能暴露以前被静默选错的媒体；回退点是保留候选但不自动加载低可信结果。
- 搜索 UI 只连接弹幕 composable，不修改播放核心或 libmpv 模块，便于独立回退。
