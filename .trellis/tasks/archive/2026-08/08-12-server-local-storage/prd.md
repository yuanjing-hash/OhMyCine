# Server 存储、媒体库与自动入库基础

## Goal

以用户指定但不进入 Git 的 Windows 本机媒体目录为首个只读验收根，在 OhMyCine Server 中依次落地 Storage、MediaLibrary、分类 Profile、持久队列、Downloader、DownloadRule、跨存储自动入库及 STRM/signed 302 基础。物理存储、逻辑媒体库、媒体分类和下载编排保持独立，让本地媒体库先可用，并为 115/OpenList/CloudDrive2 等后续真实 cloud driver 复用同一契约。

## Background

- 当前 Server 已有认证、RBAC、审计、显式迁移和 Windows 原生启动/测试入口，但尚无 Connection、Storage Destination、Category Rule 或媒体目录业务 API。
- 本地存储不伪造成网络 Connection。它表示 Server 可访问的物理根与能力边界；未来云存储通过 OpenList/Alist、115、CloudDrive2 等 Connection 提供相同的存储能力。
- 存储与媒体库分离：存储回答“Server 能访问哪里、是否健康、容量多少”；媒体库回答“存储中的哪段目录属于一个媒体库、如何扫描、如何刮削和如何组织展示”。
- 媒体库刮削/逻辑分类规则不得和流水线 `CategoryRule` 混用。前者只影响媒体索引与元数据；后者负责未来下载完成后的存储目标、命名与转移策略。
- 媒体库不按电影/剧集强制拆分。一个媒体库可以从选中 Storage 的根或根内相对目录递归扫描混合内容，先识别 `movie` / `tv`，再分别应用电影/剧集逻辑分类组和刮削策略。
- 目标产品示例：`115 下载盘 -> <本机媒体根> -> 本地媒体库1`，以及未来 `123 下载盘 -> <另一存储根> -> 123 媒体库`。媒体库页面显示 Storage 的名称和受控相对路径，不把物理绝对路径当作媒体条目展示字段。
- 一个 Media Library 严格关联一个 Storage 和一个受控相对根。跨 Storage 聚合属于首页/聚合视图，不让单个媒体库同时持有多个物理根。
- 媒体逻辑分类使用独立可复用方案，代码/API 命名为 `MediaClassificationProfile`，界面命名为“规则管理”，避免与下载流水线 `CategoryRule` 混淆。
- 真实目录只通过管理端/API写入本机被 Git 忽略的 Server SQLite；不得硬编码进数据库迁移、seed、仓库配置或测试 fixture。

## Confirmed Local Directory Evidence

- 绝对路径由用户在本机管理端/API运行时录入，只进入被 Git 忽略的 SQLite；任务文档不记录其值。
- 目录存在且可枚举，根本身不是 symlink、junction 或其它 Reparse Point。
- 递归范围内未发现 Reparse Point。
- 当前结构：2 个一级作品目录、1 个根目录视频、合计 4 个 `.mp4`，总大小 `1,821,691,422` bytes。
- 样本覆盖电影文件、电影子目录和剧集命名：`变形金刚.mp4`、`复仇者联盟/复仇者联盟.mp4`、`斗罗大陆/斗罗大陆 - S01E1/E2`。
- 当前 D 盘容量约 2 TB，剩余约 `772,760,035,328` bytes。
- 当前用户可读；ACL 还允许已认证用户 Modify，但本任务不会以写探针验证真实目录。

## Requirements

1. 新增显式版本数据库迁移和 `Storage` 模型，首版只开放 `type=local`，保存物理根、显示名称、能力与健康摘要；为未来由 cloud Connection 提供存储能力保留兼容边界。`Storage` 不等同于流水线 `StorageDestination`。
2. 提供经过 `storages.*` permission 强制保护的列表、创建、更新、删除配置与测试/探测 API；使用统一响应 envelope 和审计日志。
3. 本地根路径必须是绝对目录；Windows 支持盘符路径和 UNC 路径。拒绝空路径、相对路径、`..` 逃逸、文件路径和不可解析路径。
4. 创建或更新时规范化路径并检查目录存在、可枚举、根自身及其受控验证范围不存在 Reparse Point 逃逸风险。
5. 测试/探测返回脱敏且受控的状态摘要：是否存在、是否可读、磁盘可用空间以及明确的错误状态；不得返回目录 ACL、用户名、任意子文件绝对路径或文件内容。
6. 删除 Storage 只删除数据库配置并记录审计；绝不删除、移动、改名或覆盖真实目录及其中媒体。
7. 用户指定的本机媒体目录作为首个真实本地存储，由用户通过管理端/API显式创建；绝对路径不进入 task doc、migration、seed 或其它 Git 跟踪文件。创建存储本身不自动创建媒体库或启动扫描。
8. 自动化测试全部使用 `server/.runtime/windows/tests/` 或 Go `t.TempDir()` 隔离目录，不对真实媒体目录执行写入、删除或变更操作。
9. 管理端 `/system/connections` 从规划页替换为真实的“连接与存储”工作区，首版启用本地存储标签；网络 Connection 与流水线 Category Rule 保持明确规划状态。
10. 设计并预留独立“媒体库”领域和页面入口，但本任务不因添加存储而自动扫描媒体。媒体库未来持有：单一关联 Storage、Storage 内受控相对根（`\` 表示该 Storage 根）、递归范围、全量/增量扫描计划、所选 `MediaClassificationProfile`、刮削提供器/语言/匹配/覆盖规则、忽略规则和只读/写回策略。
11. 媒体库分类规则语义与 Player 当前 `ScrapeClassificationRules version: 1` 对齐，但由 Server 独立实现和持久化，不运行或导入 Player TypeScript：
    - 规则分 `movie` / `tv` 两组，扫描结果按识别类型进入对应组。
    - 每组有有序分类与不可缺失的 fallback；首个满足条件的分类命中。
    - 条件支持 TMDB genre、原始语言、电影 production country / 剧集 origin country、年份范围及 include/exclude。
    - 规则作为独立 Profile 由规则管理页维护；媒体库只引用一个 Profile，不内嵌一份不可管理的自由 JSON。
    - 系统提供至少一个内置默认 Profile。内置 Profile 不允许原地改名、编辑或删除，但允许一键复制为自定义 Profile。
    - 用户可以从空白创建自定义 Profile，或复制任意内置/自定义 Profile。复制操作深拷贝电影组、剧集组、fallback、条件和顺序，生成新的 ID；用户至少修改名称即可保存，之后可以继续调整具体规则。
    - 自定义 Profile 支持改名、编辑、再次复制和受控删除；名称在有效范围内唯一，复制时提供不冲突的默认副本名称。
    - 媒体库创建/编辑页通过选择控件直接选取 Profile，并显示内置/自定义标识与规则摘要，不要求在媒体库页面重复编辑规则。
    - 用户通过受控表单修改；结构化 JSON 只作为内部版本化数据，不要求普通用户编辑自由文本 config。
12. 新增独立“规则管理”页面入口，负责 `MediaClassificationProfile` 的列表、创建、复制、编辑、改名和删除；该页面属于媒体库/媒体管理领域，不放入下载流水线分类规则页面。
13. 更新架构/路线图，将 Server 首个纵向切片顺序改为本地存储优先，同时保留 OpenList/Alist、115、CloudDrive2、STRM/302 等完整计划范围。
14. 媒体库监听不进入全局持久任务队列，也不受下载/上传/刮削 worker 槽位限制。每个已启用媒体库都有独立常驻 supervisor，并可与其它媒体库同时监听：本地使用 filesystem watcher；云端由 Storage driver 自动选择事件流、change cursor 或 polling，115 优先使用“生活事件”识别新建、移动、重命名和删除。
15. 每个媒体库持久化 provider-relative 文件树快照、driver cursor 和 dirty generation。首次/周期全量重新构建文件树；事件增量直接应用变化，无法得到可靠事件时对比前后文件树生成差异。单库内合并事件并串行 reconciliation，但不建立跨媒体库的全局扫描队列。
16. 用户先选择文件源（Storage）及其相对根创建媒体库。只有来源是 cloud Storage 且用户勾选“生成 STRM”时，创建/编辑表单才显示“本地 STRM 目录”，并且该目录必填。用户通过本地目录选择器直接选择；后端把它登记为该媒体库专用、受路径边界保护的 managed output root，只写入本机 SQLite。它不是第二个文件源，也不要求用户预先再创建一个 local Storage。不能选择 cloud 路径、文件路径、Reparse Point 或不可控目录。本地文件源不显示 STRM 开关，也不需要本地投影。
17. cloud 媒体库启用 STRM 后，为视频扩展名 `mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,flv,m2ts,tp,f4v` 在所选本地 STRM 目录按相同目录结构生成 `.strm`；将同一媒体库树内的 `srt,ssa,ass,jpg` 伴随文件下载到对应相对位置。全量按当前文件树收敛，增量仅处理文件树差异。未启用 STRM 时不要求本地目录，不创建本地投影、STRM 或伴随文件，只维护 Server 索引并直接操作远端 Storage。
18. cloud Storage 的本地挂载目录与 STRM 目录是不同概念。挂载属于 Storage 访问/传输能力，主要服务于刮削产物上传、入库或其它读写流程；不得因存在挂载就把未启用 STRM 的媒体库当作本地投影，也不得自动向挂载目录生成 `.strm`。
19. DownloadRule 的冲突默认值为 `ask`。用户显式选择 `overwrite` 后，冲突任务直接覆盖且不进入人工确认：本地旧目标直接永久删除，不使用 Server 隔离回收区；cloud provider 原生支持回收站时默认先把旧目标送入云端回收站，不支持时直接永久替换。回收能力不是覆盖前提。普通删除、递归删除和永久删除仍使用独立的反复确认与审计流程。

## Acceptance Criteria

- [ ] 可在 Windows Server 管理端创建指向用户指定本机媒体根的本地存储，并在列表中看到真实在线/可读状态和磁盘空间摘要；创建后不自动扫描其中媒体。
- [ ] 重启 Server 后该目标仍存在于本机 SQLite，且仓库中没有该真实路径的 migration/seed/fixture。
- [ ] 无权限用户无法读写目标；有权限操作均经过服务端 policy，创建/更新/测试/删除产生不含子文件与敏感系统信息的审计记录。
- [ ] 相对路径、文件路径、缺失目录和 Reparse Point 逃逸验证均有回归测试。
- [ ] 删除该 Storage 配置后，本机验收媒体根及其中 4 个现有 `.mp4` 不受影响。
- [ ] Windows `server/test.ps1` 全量通过，成功测试不会留下未忽略产物。
- [ ] 两个或更多媒体库可同时监听，任何一个库的 provider 限速、重连或全量 reconciliation 不占用另一个库的监听槽位，也不占下载/上传/刮削队列槽位。
- [ ] fake cloud 文件树可验证 STRM 开启时视频投影与 `srt/ssa/ass/jpg` 同结构同步、移动/删除差异收敛；关闭 STRM 时没有本地投影产物。
- [ ] cloud 媒体库勾选 STRM 后必须通过目录选择器选择有效本地目录才能保存，不需要先创建第二个 local Storage；取消勾选后该字段不再必填。本地来源从不显示该字段或 STRM 开关。
- [ ] cloud 挂载目录只参与明确的读写/上传路线，不会让未启用 STRM 的媒体库产生任何投影文件。
- [ ] 显式 `overwrite` 在本地、cloud 有回收站、cloud 无回收站三种目标下都不中断为人工选择；本地直接永久替换，cloud 有回收站时旧目标默认入回收站，普通真实数据删除仍要求反复确认。

## Out of Scope

- 115、OpenList/Alist、CloudDrive2、WebDAV 的真实 Connection/Storage driver；本计划仅以 fake cloud capability 验证通用 STRM/302/传输契约，首个真实 driver 后补 live test。
- PT 聚合/搜索、追更订阅、插件、多用户扩展和更多网盘；保留产品范围但不在本轮八个子任务中实现。
- 流水线 `CategoryRule` / `StorageDestination` 的完整实现；本轮使用 DownloadRule 直接解析到目标 MediaLibrary，并保留后续兼容边界。
- 对本机验收媒体根中的真实样本执行 copy/move/hardlink/symlink/overwrite/delete；本轮涉及真实写操作的自动化验证只使用 `t.TempDir()` 或被忽略的 Windows 隔离目录。
