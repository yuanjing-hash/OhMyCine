# Server 下载规则管理

## Goal

提供独立“下载规则”页面，让用户预先配置 Downloader → 目标 MediaLibrary → staging/跨 Storage/入库策略；下载资源时默认选中默认规则，也可切换其它规则。

## Requirements

1. `DownloadRule` 独立持久化，名称唯一，可创建、编辑、复制、启用/停用和删除；至少一个有效规则可设为默认，设置新默认使用事务保证唯一。
2. 规则引用一个 Downloader 和一个目标 MediaLibrary；可选本地 staging Storage/root，并保存允许的 route preference、带宽/并发、冲突策略和完成后自动进入识别/入库开关。
3. 冲突策略允许 `ask`（未设置时的默认）、`overwrite`、`skip`、`rename`。选择 `overwrite` 必须由用户显式设置并显示可能永久替换现有媒体的高风险提示；任务执行时直接覆盖，不进入 `waiting_user_action`。本地旧目标直接永久删除，不使用 Server 隔离回收区；cloud provider 原生支持回收站时旧目标默认送入云端回收站，不支持时直接永久替换。回收站能力不是保存或执行 `overwrite` 的前提。它不等于允许删除其它不相关文件或递归目录。
4. 规则保存时根据 Downloader/Storage/MediaLibrary capability 做完整 route validation；无效组合不能保存为 enabled/default。
5. 资源下载弹窗默认选择默认规则，显示 downloader、目标库、预期 hops、临时空间与关键策略摘要；用户可切换任意有权且有效的规则。
6. 创建 DownloadTask 时深度快照规则版本和解析后的 DownloadRoute；后续编辑/停用/删除规则不改变已有任务。
7. 复制规则生成独立 ID 和不冲突名称；用户至少修改名称即可保存。
8. 被设为默认或被未完成任务引用的规则删除受控：默认规则需先选择替代；任务保留 snapshot，因此允许删除历史规则但必须提示引用统计。
9. DownloadRule 不允许配置完成后自动删除本地源、staging 或其它无关媒体/云端文件。`overwrite` 对冲突目标的移除是唯一内嵌例外；普通删除、递归删除和永久删除仍走独立反复确认流程。
10. Downloader 支持原生分类/标签/目标目录能力时，DownloadRule 保存“现有 MediaClassificationProfile 分类结果 → provider-native routing”的映射。qBittorrent 首先支持原生 category；该 category 只负责下载器内的分组、做种策略和统一暂存根下的输出分区，不替代目标 MediaLibrary、分类 Profile 或下载完成后的正式刮削结果。默认复用目标 MediaLibrary 已选择的 Profile 及其 revision，不创建第二套下载专用分类规则。
11. 裸 magnet 只有 info hash、尚无可信名称或文件清单时，不能要求用户预先选择 qBittorrent category，也不能由 OhMyCine 伪造识别结果。qBittorrent v5 首选以 `stopCondition=MetadataReceived` 提交元数据探测任务：qBit 获取 metadata 后自动停止，OhMyCine 读取真实名称/文件清单，执行“文件名/季集解析 → 受限 metadata 匹配 → 现有 `classification.Classify`”轻量刮削，调用 provider `setCategory` 后才恢复完整内容下载。已有可信站点分类、标题、TMDB/TVDB identity 或上传 `.torrent` 文件清单时，可以在首次提交前完成同一流程。
12. 自动识别得到的 qBittorrent category、分类依据、置信度、下载规则 revision 和目标 MediaLibrary 必须写入不可变任务快照；下载完成后仍以真实文件树执行正式识别/刮削、ImportPlan、转移和目标媒体库 reconciliation，预分类不能替代最终刮削。
13. provider category 被删除、改名或路径不再满足暂存边界时，任务必须阻止正式下载或进入可操作的等待/错误状态，不能静默退回未分类下载。qBittorrent metadata 探测、分类指派和恢复下载必须可重入；Server 重启后按 provider task ID/tag 对账，不能重复添加磁力。

## Acceptance Criteria

- [ ] 可创建“qBit 下载到本地媒体库”和“115 离线后转本地媒体库”等规则并设定唯一默认。
- [ ] 下载资源时默认规则已选中，可切换其它有效规则，并能看到解析后的路线摘要。
- [ ] 任务创建后修改规则不改变该任务的 downloader、目标库、staging 或 hops。
- [ ] capability 不兼容规则无法启用；复制规则是独立深拷贝。
- [ ] 未设置冲突策略时任务使用 ask；显式 overwrite/skip/rename 按规则快照执行。
- [ ] 显式 overwrite 在本地、cloud 有回收站、cloud 无回收站三种目标下均直接执行且不等待用户；本地直接永久替换，cloud 有回收站时旧目标默认进入云端回收站，否则永久替换并审计。
- [ ] 页面和 API 中不存在默认自动删除真实文件的普通开关。
- [ ] 提交裸 magnet 后，qBittorrent 先获取 metadata 并暂停；OhMyCine 根据真实名称/文件清单自动选中规则映射的 qBittorrent category，再恢复正式下载。用户不需要为每条磁力手工选择 category。
- [ ] 已带可信分类/identity 的 PT、发现页或上传 `.torrent` 任务可以提交前预分类；下载完成后统一进入正式刮削与转移流程。
- [ ] 轻量预分类与下载完成后的最终分类使用同一个 MediaClassificationProfile、同一首个匹配胜出顺序和同一 fallback 语义；任务快照记录 Profile ID/revision、匹配规则 ID、证据与置信度，不存在下载专用的重复分类配置。
- [ ] 提交后 qBittorrent 任务显示自动识别得到的 category；OhMyCine 重启、规则编辑或 provider category 漂移不会改变既有任务快照，也不会重复添加磁力。
- [ ] qBittorrent category 的下载路径必须被解析到统一暂存根的受控分区，不能通过 category 绕过 Storage/path 边界；网盘原生离线下载器通过各自 capability 暴露 provider folder/routing，而不是伪装成 qBittorrent category。

## Open Question

- 磁力 metadata 到达后仍无法达到自动分类置信度时，是保持 qBittorrent 任务暂停并进入 `waiting_user_action` 等用户选择，还是自动归入“其他/未分类”并继续下载？

## Out of Scope

- Downloader adapter 的具体实现。
- Transfer engine 的真实字节传输。
- 普通真实文件/云端对象删除执行；只定义独立确认契约。冲突 overwrite 的目标替换由 Transfer/Import 任务实现。
