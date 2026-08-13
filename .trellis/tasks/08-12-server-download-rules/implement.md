# Implementation Plan

1. 定义 DownloadRule、revision、default invariant、snapshot 和 route validation schema。
2. 增加迁移、permissions、审计和 CRUD/copy/set-default/validate API。
3. 接入 Downloader/MediaLibrary/Storage capability resolver。
4. 实现规则管理页面和受控选择器。
5. 实现下载弹窗默认/切换规则与 route 摘要。
6. 测试唯一默认、无效组合、复制、规则变更不影响任务 snapshot、删除提示，以及 overwrite 在有/无回收站 capability 下都不产生 ActionRequest。
