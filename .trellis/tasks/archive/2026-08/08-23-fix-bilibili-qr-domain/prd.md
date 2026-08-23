# 修复 Bilibili 扫码域名权限并发布 0.3.1

## Goal

补齐 Bilibili 新二维码域名的精确网络权限，加入回归测试并发布官方插件 0.3.1。

## Requirements

- Bilibili 扫码登录必须接受当前官方接口返回的 `account.bilibili.com` HTTPS 二维码地址。
- 插件网络权限必须保持最小授权，只增加扫码流程实际需要的精确域名，不能放宽为通配的 `*.bilibili.com`。
- 必须加入覆盖真实新二维码域名的回归测试，避免测试夹具继续掩盖上游域名迁移。
- 插件版本发布为 0.3.1，并更新官方 Registry、Manifest、安装包与发布说明。
- 本次纯插件更新不得要求用户重启 Server，也不得泄露二维码会话键、Cookie 或其他登录凭据。

## Acceptance Criteria

- [x] 0.3.1 Manifest 明确声明 `account.bilibili.com`，原有 Bilibili API/CDN 域名权限保持不变。
- [x] 二维码生成夹具使用当前 `account.bilibili.com` 地址，解析与权限测试通过。
- [x] Rust 单元测试、Clippy、WASM 构建及插件 SDK 校验通过。
- [x] 官方插件仓库 Registry 指向 0.3.1，公网下载的 Manifest/OMCP SHA-256 与 Registry 完全一致。
- [x] 用户刷新官方仓库并升级插件后，可直接重新扫码，无需重启 Server。

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
