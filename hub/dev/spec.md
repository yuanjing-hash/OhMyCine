# OhMyCine 插件仓库规范 v1

OhMyCine 插件只在 Server 安装和运行。用户在 Server 管理端的“插件 → 仓库设置”中添加 GitHub 仓库主页地址，例如：

```text
https://github.com/example/ohmycine-plugins
```

Server 会读取仓库根目录的 `ohmycine-plugin-registry.v1.json`，再从该仓库的 GitHub Release 下载插件包。Player 不连接 GitHub、不安装插件，也不执行插件代码。

## 仓库目录

```text
ohmycine-plugins/
├── ohmycine-plugin-registry.v1.json
├── plugins/
│   └── example-site/
│       ├── plugin.template.json
│       ├── README.md
│       └── src/
└── .github/workflows/release-plugin.yml
```

这个结构借鉴了 MoviePilot 的集中索引、插件源码目录和 GitHub Release 发布方式，但 OhMyCine 不会直接加载仓库里的 Python/Go 源码。正式安装包必须包含经过校验的 v1 Manifest 和 WASM 入口。

## Registry

Registry 的完整 JSON Schema 位于 `plugin-sdk/schema/registry-v1.schema.json`。每条插件记录至少包含：

- 稳定插件 ID、名称、说明、版本和 stable/beta 渠道；
- Manifest 与 `.omcp` 包的 GitHub Release 地址；
- 包的 SHA-256；
- Server 最低/最高兼容版本；
- 分类和版本说明。

Server 通过 GitHub API 获取默认分支和提交 SHA，再读取该提交上的 Registry。仓库地址只接受 `https://github.com/{owner}/{repo}`，不能填写 raw URL、带 Token 的 URL 或任意下载地址。

## Manifest

Manifest 的完整 JSON Schema 位于 `plugin-sdk/schema/manifest-v1.schema.json`。正式运行时固定为 `wasm`，能力和权限必须显式声明。

首期站点能力包括导航、Feed、搜索、详情、个人内容、交互、播放、清晰度、字幕、弹幕、下载计划、主页贡献和刷新。PT 站点能力不向插件开放，PT 仍是 Server 内建功能。

## 权限

权限按最小范围声明：

- `network.http`：允许访问的域名；
- `credential.use`：允许使用的插件私有凭据句柄；
- `storage.private`：插件私有存储与容量上限；
- `event.subscribe`：允许订阅的宿主事件；
- `download.plan`：只允许描述下载资产与合流关系。

插件没有文件系统、Socket、环境变量、全局数据库或全局凭据访问权。插件不能注册 Gin 路由、注入 Vue/JavaScript，也不能把任意命令交给 FFmpeg 执行。

## 发布规则

1. 使用 `@ohmycine/plugin-sdk` 类型和 Host mock 完成测试。
2. 构建 `plugin.wasm`，生成真实 Manifest 和 `.omcp` 包。
3. 计算 SHA-256；正式仓库建议额外提供 Ed25519 签名。
4. 把包和 Manifest 上传到同一 GitHub 仓库的 Release。
5. 更新 Registry，并通过 Schema、兼容性、权限域名和摘要 CI。

安装、升级、回滚和卸载均由 Server 管理端执行并写入审计日志。新增权限的升级必须再次确认。
