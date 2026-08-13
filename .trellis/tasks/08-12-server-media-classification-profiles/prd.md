# Server 媒体分类规则管理

## Goal

在 Server 管理端提供独立的“系统 → 规则管理”页面和可复用 `MediaClassificationProfile`，让用户可以查看内置默认规则、从空白创建或复制规则、通过受控表单编辑，并在后续创建媒体库时直接选择。该规则只负责媒体索引后的逻辑分类，不执行下载目标选择、移动、重命名或任何文件写入。

## Background

- Storage 和跨平台目录选择器已经完成；下一个媒体库子任务需要可引用的分类 Profile。
- Player 已实现 `ScrapeClassificationRules version: 1`，Server 必须独立实现相同数据与匹配语义，不能运行或导入 Player TypeScript。
- Server 现有 `categories.*` 权限和未来 `/categories` 领域属于下载/转移流水线 `CategoryRule`。媒体分类 Profile 必须使用独立命名，防止两种规则混用。
- 当前 Server 尚无 `MediaLibrary` 表。本任务建立 Profile 与未来引用摘要的稳定边界，但不伪造媒体库引用；实际引用、共享影响名单和待重分类状态由紧随其后的媒体库任务集成。

## Requirements

1. 新增显式数据库迁移和 `MediaClassificationProfile` 模型。字段至少包含稳定 ID、名称、规范化唯一名称、`system/custom` 类型、稳定内置 code、schema version、完整规则 JSON、revision、创建/更新时间。规则正文作为版本化结构保存，不能以未校验自由 JSON 直接落库。
2. 新增独立权限：`media_classification_profiles.read/create/update/delete`。Owner/administrator 保持全权；operator 默认获得四项；viewer 不默认获得。路由 middleware 和 service policy 双重校验，前端导航/按钮使用生成的 canonical permission constants。
3. Server 提供稳定 `/api/v1/media-classification-profiles` API：列表、详情/读取、创建、复制、更新和删除。复制使用 `POST /:id/copy`，属于 create 权限；更新携带 revision 并使用乐观并发，陈旧 revision 返回稳定冲突错误。
4. 系统 seed 至少提供一个名为“默认分类规则”、code 为 `default-v1` 的只读内置 Profile。内置 Profile 可查看、可复制，但不可改名、编辑或删除；重复迁移/启动不得生成重复 seed，也不得覆盖用户自定义 Profile。
5. 默认规则必须与 Player 当前 v1 精确一致：
   - 电影按顺序：`动画电影`（genre 16）、`华语电影`（original language zh/cn）、`外语电影`（排除 zh/cn）；fallback `未分类`。
   - 剧集按顺序：`国漫`（genre 16 + origin CN/TW/HK）、`日番`（genre 16 + origin JP）、`动漫`（genre 16）、`纪录片`（genre 99）、`儿童`（genre 10762）、`综艺`（genre 10764/10767）、`国产剧`（origin CN/TW/HK）、`欧美剧`（origin US/FR/GB/DE/ES/IT/NL/PT/RU）、`日韩剧`（origin JP/KR）；fallback `未分类`。
6. version 1 schema 必须恰好包含一组 `movie` 和一组 `tv`。每组包含有序 category 列表和不可缺失、不可为空的 fallback 名称。每个 category 有稳定、Profile 内唯一的 ID、非空且组内唯一的名称以及受控 conditions。
7. conditions 支持：TMDB genre include/exclude、original language include/exclude、电影 production country include/exclude、剧集 origin country include/exclude、release year from/to。genre 必须来自对应 movie/tv allowlist；语言与国家来自当前 Player v1 allowlist；年份为 1888–2200 且 from 不得大于 to；include/exclude 不能包含相同值；未知字段、未知枚举、重复值和错误媒体类型字段必须被拒绝并返回稳定验证错误。
8. 匹配语义与 Player v1 一致：按 category 顺序取第一个匹配；单一条件内 include 为 OR、不同条件维度为 AND、exclude 优先；字符串不区分大小写；有 include 但元数据缺失时不匹配；年份范围包含边界；全部未命中时返回 fallback。匹配器为无数据库依赖的 Go 纯函数。
9. 用户可以从空白创建自定义 Profile（movie/tv 两组、空 categories、fallback `未分类`），也可以复制任意内置或自定义 Profile。复制必须深拷贝全部 groups/categories/conditions/order/fallback，生成新的 Profile ID、category IDs 和 revision；来源和副本后续互不影响。
10. 复制可选输入名称；未输入时按“`<来源名称> 副本`、`<来源名称> 副本 2`…”生成首个不冲突名称。创建、复制、改名在 trim + Unicode case-fold/统一规范化后保持有效范围内唯一；名称长度和错误状态必须稳定。
11. 自定义 Profile 可改名、编辑、再次复制和删除。删除只删除 Profile 配置，不触碰 Storage 或文件。媒体库引用保护将在下一任务接入；本任务的 service 边界必须允许注入/扩展引用检查，不能把“永远无引用”写死为长期语义。
12. 创建、复制、更新、删除均写审计日志，只记录 actor、profile ID、动作、结果、revision 和不敏感摘要；不得把完整规则 JSON或未来媒体绝对路径写入审计/普通日志。
13. Server 管理端侧栏在“系统”分组新增“规则管理”，路由 `/system/media-rules`，与“连接与存储”并列；只有 read 权限时显示。该页不能放入“媒体整理”，避免与下载/传输 CategoryRule 混淆。
14. 规则管理页使用本任务已建立的白色默认/深色切换传统后台视觉。页面提供 Profile 列表、内置/自定义标记、摘要、创建、复制、改名、编辑和删除；无写权限时为只读视图。
15. 编辑器使用受控表单：电影/剧集分组、顺序调整、添加/删除 category、fallback 改名、genre/语言/国家的 include/exclude/不限制三态选择、年份范围。普通用户不编辑自由 JSON；保存失败保留草稿并显示字段级或稳定错误信息。
16. 内置 Profile 的复制入口始终可用（有 create 权限时）；内置编辑/删除按钮不显示或禁用并解释原因。自定义删除必须显式确认，但不使用文件删除级多次确认，因为它只删除配置。
17. 为 Server Go matcher 建立覆盖 Player v1 默认规则和边界条件的契约 fixture/tests；不得读取 Player 本地设置、修改 Player 规则或让 Player 依赖 Server。

## Acceptance Criteria

- [ ] v2 数据库升级到新迁移后存在 Profile 表和唯一一个 `default-v1`；重复 Migrate 不重复 seed，现有用户/Storage 数据保持不变。
- [ ] 默认 Profile 在 API/UI 中可读且内容、顺序、fallback 与 Player v1 当前实现一致；更新和删除均被服务端拒绝，复制成功。
- [ ] 空白创建和复制均生成独立自定义 Profile；复制后规则深度相等但 Profile/category ID 不同，修改副本不影响来源。
- [ ] 自动副本名称按冲突递增；同名创建/改名和并发唯一约束竞态返回稳定冲突错误而非 500。
- [ ] 陈旧 revision 更新返回稳定并发冲突；有效更新 revision 单调增加且完整替换经过严格校验的规则正文。
- [ ] Go matcher 契约样本覆盖默认电影/剧集分类、顺序优先、include OR、维度 AND、exclude 优先、大小写、缺失元数据、年份边界和 fallback，并得到与 Player v1 相同结果。
- [ ] owner/administrator、operator、viewer 的 read/write 权限矩阵在 middleware 和 service 层都有回归测试；无 read 权限时侧栏不显示且直接访问路由进入 403。
- [ ] “系统 → 规则管理”在白色/深色与响应式布局中可用；受控编辑器能完成 category 排序、三态条件、fallback 和保存错误恢复，内置 Profile 明确只读。
- [ ] 审计不包含完整规则 JSON；所有 API 使用标准 envelope，`server/test.ps1`、Go test/vet/build 和 Web UI test/typecheck/lint/build 全部通过。

## Out of Scope

- MediaLibrary 表、Profile 真实引用列表、共享更新后的待重分类标记和删除引用保护的端到端实现；下一媒体库子任务完成。
- 实际目录扫描、TMDB 网络请求、元数据抓取、海报/NFO、STRM、302、文件移动/重命名/删除。
- 下载/转移流水线 `CategoryRule`、`StorageDestination` 和现有 `categories.*` 的实现或改名。
- Player 规则 UI/存储迁移、Player 与 Server 规则自动同步、自由 JSON/YAML 编辑和 schema v2。
