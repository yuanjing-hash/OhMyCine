# OhMyCine Plugin SDK

`@ohmycine/plugin-sdk` 定义 Server 非 PT 站点插件的 Registry、Manifest、媒体 DTO 和运行时契约。插件在 Server 的 WASM 沙箱中运行；Player 不安装插件包，也不执行插件代码。

## 构建可安装包

准备符合 `schema/manifest-v1.schema.json` 的 Manifest 模板和已编译 WASM，然后执行：

```powershell
npm install
npm run pack -- --manifest ..\plugins\official\example\plugin.template.json --wasm .\build\plugin.wasm --out .\dist\example
```

输出是两个独立的 GitHub Release 资产：

- `<plugin-id>-<version>.manifest.json`：包含工具计算后的 `packageSha256`。
- `<plugin-id>-<version>.omcp`：内容确定的 ZIP 包，只包含 WASM 入口和将来允许的受管资源。

Manifest 不能放进 `.omcp`，否则 Manifest 中的包摘要会与包含 Manifest 自身的压缩包形成循环。不要手工修改生成后 Manifest 的身份、版本、来源或摘要。

Server 安装时还会为安全解包后的完整目录树计算独立摘要，并在确认、启用、回滚和重启恢复前重新计算。即使 `.omcp` 摘要和内容寻址目录名不变，安装后被修改或增加文件的包也不会执行。

## 生成 Registry 条目

仓库根目录发布 `ohmycine-plugin-registry.v1.json`。每个条目的 `manifestUrl` 和 `packageUrl` 必须指向同一 GitHub 仓库的 Release 资产，`packageSha256` 必须与生成 Manifest 和 `.omcp` 完全一致。Server 会先固定 Registry 所在提交 SHA，再受控下载 Release 资产。

模板见 [`../plugins/ohmycine-plugin-registry.v1.template.json`](../plugins/ohmycine-plugin-registry.v1.template.json)。发布前执行：

```powershell
npm run verify
npm run typecheck
```

`fixtures/online-media.v1.json` 是 Go Host 与 TypeScript SDK 共读的安全契约夹具，固定验证 Work → Segment → Version → Variant、声明式动作、DASH 双轨、字幕/弹幕、UUID 资产引用和短时选择令牌。修改这些字段时必须同时通过 SDK verify 与 Server 共读测试。

## 本地生命周期 fixture

```powershell
npm run build:fixture
```

该命令生成一个真实、无 Host import 的最小 WASM 插件和可安装资产，用于 Server 安装、启停、升级、回滚和卸载测试。`dist/` 是本地产物，不提交 Git。

## Runtime v1 最小约束

- 必须导出 `omc_api_version() i32` 并返回 `1`。
- 必须导出线性内存、`omc_alloc(size) i32` 与 `omc_invoke(operation, pointer, length) i64`；可以导出 `omc_start()`。
- 唯一允许的 import 是 `ohmycine.host_call`。WASM 不开放 WASI、Socket、文件系统、环境变量、系统命令或数据库。
- 插件操作使用 [`src/runtime.ts`](src/runtime.ts) 中冻结的 operation code 和 JSON DTO；站点交互、扫码登录、播放进度、PlaybackPlan 与 DownloadPlan 均不得另建私有路由。
- Host HTTP 只允许 Manifest 授权的 HTTPS 域名，并限制 DNS、重定向、超时和载荷。Cookie/Token 只能由凭据引用附加，不能返回 WASM。
- 扫码登录使用两阶段 Cookie 捕获：HTTP 返回一次性 `credentialCaptureRef`，插件确认提供方业务状态成功后才调用 `credentialCommit`。引用短时、单次且绑定插件、运行代次、连接、scope 与 origin。
- 远端播放/下载地址先注册成短时 `urlRef`；普通 DTO、日志和 Player 持久状态不能出现最终 CDN URL 或敏感 Header。
- Manifest 只能声明 SDK 已知 capability 和精确权限。
- PT 站点适配器属于 Server 内建能力，不通过此 SDK 注册。
