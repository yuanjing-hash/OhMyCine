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

## Acceptance Criteria

- [ ] 可创建“qBit 下载到本地媒体库”和“115 离线后转本地媒体库”等规则并设定唯一默认。
- [ ] 下载资源时默认规则已选中，可切换其它有效规则，并能看到解析后的路线摘要。
- [ ] 任务创建后修改规则不改变该任务的 downloader、目标库、staging 或 hops。
- [ ] capability 不兼容规则无法启用；复制规则是独立深拷贝。
- [ ] 未设置冲突策略时任务使用 ask；显式 overwrite/skip/rename 按规则快照执行。
- [ ] 显式 overwrite 在本地、cloud 有回收站、cloud 无回收站三种目标下均直接执行且不等待用户；本地直接永久替换，cloud 有回收站时旧目标默认进入云端回收站，否则永久替换并审计。
- [ ] 页面和 API 中不存在默认自动删除真实文件的普通开关。

## Out of Scope

- Downloader adapter 的具体实现。
- Transfer engine 的真实字节传输。
- 普通真实文件/云端对象删除执行；只定义独立确认契约。冲突 overwrite 的目标替换由 Transfer/Import 任务实现。
