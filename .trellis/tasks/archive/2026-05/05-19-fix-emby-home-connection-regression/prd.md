# 修复 Emby 首页连接回归

## Goal

修复最新版本打开主页后 Emby 内容无法连接/加载的回归，优先恢复基础 Emby 浏览能力，再保留 token-safe provider sync 诊断。

## Requirements

- 首页 Emby 数据源必须能正常加载 home sections / continue watching / latest library 内容。
- 不得为了播放进度上报兼容而破坏普通 Emby API 请求。
- 移除或收敛可能触发 Emby/CORS/网关拒绝的新增认证头。
- Provider sync 诊断仍保留，但不能影响数据源连接。

## Acceptance Criteria

- [ ] Emby request headers 回到稳定可连接形态。
- [ ] Player typecheck/lint/build 通过。
- [ ] Windows Tauri build 通过。

## Technical Notes

- 最新回归最可疑点：为 playstate 兼容新增 `X-MediaBrowser-*` headers 后，所有 Emby API 请求都带上这些额外 header，可能导致服务端/跨域预检/代理拒绝，从而首页直接连不上。
- 重点文件：`player/src/services/datasource/emby.ts`、`player/src/services/datasource/errors.ts`。
