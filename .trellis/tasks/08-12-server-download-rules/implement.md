# Implementation Plan

1. 定义 DownloadRule、revision、default invariant、snapshot 和 route validation schema。
2. 增加迁移、permissions、审计和 CRUD/copy/set-default/validate API。
3. 接入 Downloader/MediaLibrary/Storage capability resolver。
4. 扩展 qBittorrent adapter 的 metadata-only submit、torrent 名称/文件清单读取、category 查询/指派和幂等恢复；兼容 v5 `stopCondition=MetadataReceived`，不重复添加 magnet。
5. 实现共享预分类 orchestration：快照目标 MediaLibrary Profile，解析 provider metadata，执行受限 metadata 匹配并调用现有 `classification.Classify`，保存规则 ID、证据、置信度和 provider routing。
6. 实现规则管理页面和分类结果到 provider category 的受控映射选择器。
7. 实现下载弹窗默认/切换规则与 route 摘要；展示“获取 metadata → 预分类 → 已指派分类 → 正式下载”状态。
8. 测试唯一默认、无效组合、复制、规则/Profile 变更不影响任务 snapshot、metadata 后暂停与分类恢复、缺字段不误判 fallback、Server 重启幂等对账、删除提示，以及 overwrite 在有/无回收站 capability 下都不产生 ActionRequest。
