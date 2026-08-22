# 插件开发快速开始

OhMyCine 的非 PT 站点插件运行在 Server WASM 沙箱中。用户通过 Server 插件页添加 GitHub 仓库并安装插件；Player 自动把插件发布的在线媒体库作为 Server 子来源使用。

## 开发步骤

1. 从 `plugin-sdk/` 获取 Manifest、Registry、媒体 DTO、PlaybackPlan、DownloadPlan 和 Host API 类型。
2. 在独立插件仓库中创建插件源码与 `plugin.template.json`。
3. 使用固定内容 fixture 验证导航、Feed、详情、播放方案和错误隔离。
4. 构建 WASM，通过 SDK 的 Schema 与兼容测试。
5. 发布 GitHub Release，并更新根目录 `ohmycine-plugin-registry.v1.json`。

官方插件源码示例位于 `plugins/official/`。官方 Bilibili 插件与第三方插件遵守完全相同的安装、权限和运行时边界。

PT 站点不使用此插件系统；PTTime 等站点属于 Server 内建 PT 管理能力。
