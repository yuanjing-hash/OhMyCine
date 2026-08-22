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

## 本地生命周期 fixture

```powershell
npm run build:fixture
```

该命令生成一个真实、无 Host import 的最小 WASM 插件和可安装资产，用于 Server 安装、启停、升级、回滚和卸载测试。`dist/` 是本地产物，不提交 Git。

## Runtime v1 最小约束

- 必须导出 `omc_api_version() i32` 并返回 `1`。
- 可以导出 `omc_start()`。
- 当前安装生命周期不开放 WASI 或任意 Host import。
- Manifest 只能声明 SDK 已知 capability 和精确权限。
- PT 站点适配器属于 Server 内建能力，不通过此 SDK 注册。
