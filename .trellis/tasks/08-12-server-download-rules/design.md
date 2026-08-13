# Design: Download Rules

DownloadRule 保存用户意图和引用；ResolvedDownloadRoute 是基于当前 capability 计算的计划；DownloadRuleSnapshot 随任务不可变保存。三者分离防止规则编辑污染运行任务。

默认规则用数据库唯一约束/事务维护。规则 revision 乐观并发。UI 规则列表展示有效性、默认标记、Downloader、目标 MediaLibrary、route 摘要与最近校验时间。

删除契约与规则基本分离：规则不能决定完成后清理 source/staging 或删除无关真实数据；唯一例外是冲突策略 `overwrite`，它明确授权 Transfer/Import 对同一目标路径直接替换。本地目标始终直接永久移除旧文件；cloud driver capability `supports_trash` 只决定旧目标是默认入云端回收站还是永久移除，不决定 overwrite 是否可用，也不新增人工确认。
