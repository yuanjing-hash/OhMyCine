# Design: Download Rules

DownloadRule 保存用户意图和引用；ResolvedDownloadRoute 是基于当前 capability 计算的计划；DownloadRuleSnapshot 随任务不可变保存。三者分离防止规则编辑污染运行任务。

默认规则用数据库唯一约束/事务维护。规则 revision 乐观并发。UI 规则列表展示有效性、默认标记、Downloader、目标 MediaLibrary、route 摘要与最近校验时间。

## Shared Classification Pipeline

DownloadRule 不拥有第二套分类规则。它默认快照目标 MediaLibrary 当前引用的 `MediaClassificationProfile`，并只保存 Profile category ID/name 到 provider-native routing 的映射。分类引擎继续复用 `internal/classification` 的有序首个匹配胜出语义。

裸 magnet 首次只有 info hash。qBittorrent v5 adapter 先以 `stopCondition=MetadataReceived` 和稳定 OMC tag 提交元数据探测任务；metadata 到达后 qBit 自动停止。预分类服务读取 provider 返回的 torrent 名称和文件清单，执行文件名/季集解析，并按 DownloadRule/MediaLibrary 的 metadata 语言、地区、匹配策略做受限 TMDB/TVDB 匹配，构造现有 `classification.Metadata` 后调用同一 `classification.Classify`。fallback 结果只有在证据/置信度策略允许时才能自动恢复下载，不能把“缺少 genre/语言/国家字段”误当成可信分类。

高置信结果解析为 qBittorrent category；adapter 调用 `setCategory` 后恢复任务。Profile ID/revision、匹配规则 ID、category、证据摘要和置信度进入 DownloadRuleSnapshot/DownloadTask，后续规则编辑不改变该任务。下载完成后以真实文件树运行更完整的同一识别/分类/刮削管线，生成 ImportPlan 并转移到目标 MediaLibrary；预分类是刮削的早期阶段，不是独立系统。

PT/发现页若提供可信 category、标题和 TMDB/TVDB identity，可以在 provider submit 前完成相同分类；上传 `.torrent` 可以直接读取文件清单。所有路径最终都汇入同一 Profile matcher 和任务快照。

删除契约与规则基本分离：规则不能决定完成后清理 source/staging 或删除无关真实数据；唯一例外是冲突策略 `overwrite`，它明确授权 Transfer/Import 对同一目标路径直接替换。本地目标始终直接永久移除旧文件；cloud driver capability `supports_trash` 只决定旧目标是默认入云端回收站还是永久移除，不决定 overwrite 是否可用，也不新增人工确认。
