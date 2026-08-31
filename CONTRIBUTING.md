# 贡献指南

感谢参与 OhMyCine Player。

1. 从最新 `develop` 创建功能或修复分支。
2. 保持 Player 无 Server 也能完成基本浏览和播放。
3. 为行为变更补充测试或 `verify:*` 契约脚本。
4. 提交前运行 `npm run typecheck`、`npm run lint`、`npm run build`，Rust 改动同时运行相应 Cargo 门禁。
5. 使用 Conventional Commits，例如 `fix(player): 修复远程字幕加载失败`。

Server/CLI 问题请前往 [OhMyCine-Server](https://github.com/yuanjing-hash/OhMyCine-Server)，插件/SDK/Hub 问题请前往 [OhMyCine-Plugins](https://github.com/yuanjing-hash/OhMyCine-Plugins)。

报告问题时请删除日志和截图中的访问令牌、Cookie、API Key、签名 URL、本地绝对路径和私人地址。
