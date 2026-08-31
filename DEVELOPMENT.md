# OhMyCine Player 开发指南

## 环境

- Windows 11 + PowerShell
- Node.js 20+
- Rust stable（Windows 桌面默认 `x86_64-pc-windows-msvc`）
- Tauri v2 所需的 WebView2/MSVC 工具链

Windows 原生运行和打包结果是桌面版本的权威结果；Linux/WSL 检查仅作为补充。

## 安装与开发

```powershell
npm install
npm run setup:libmpv -- windows
npm run tauri:dev:windows
```

## 质量门禁

```powershell
npm run typecheck
npm run lint
npm run build
cargo test --manifest-path src-tauri/Cargo.toml --lib
cargo clippy --manifest-path src-tauri/Cargo.toml --all-targets -- -D warnings
```

根据改动范围运行 `package.json` 中对应的 `verify:*` 脚本。Android 预览使用 `npm run tauri:build:android:preview`，Windows 原生包使用 `npm run tauri:build:windows:native`。

## 代码边界

- `src/views/` 负责路由级编排；复用逻辑进入 typed services/composables。
- 所有媒体源通过 `src/services/datasource/` 的公共接口接入。
- Tauri commands 只暴露平台能力，内部使用结构化错误，传给前端的错误必须安全。
- Player 本地刮削和分类不得写回媒体源。
- 不得清理或迁移用户真实 profile 来换取测试通过。

## 分支与提交

- 从最新 `origin/develop` 创建 `feature/*`、`fix/*` 或 `codex/*` 分支。
- 验证后合入 `develop`；只有确认 Stable 时才合入 `main`。
- 提交格式为 `<type>(player): <中文描述>`。

更细的可执行规范位于 `.trellis/spec/frontend/`。
