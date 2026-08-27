# 实施计划

## 1. 搜索双入口与白屏修复

- [x] 将 WebUI 标签改为“搜索 / 直接搜索”，统一指定静态按钮文案。
- [x] 将可信详情 identity provenance 与直接搜索表单状态分开，恢复手工 TMDB ID 的旧 `/torrent-search?search_by=tmdb_id` 路径。
- [x] Server 所有 `SiteSearchGroup.Items` 保证非 nil；WebUI 在 JSON/SSE/session 唯一边界归一 `items`。
- [x] 统一 TMDB 海报与推荐详情导航，确认详情 coverage、多语言资源搜索与下载入口一致。
- [x] 增加普通 JSON、SSE、session restore、空结果、部分失败、海报导航和直接 TMDB ID 路由测试。

## 2. 详情库存覆盖与订阅 UI

- [x] 在电影/电视剧详情投影 coverage 汇总和逐季逐集状态，未知事实保持保守。
- [x] 整理订阅创建/编辑布局：范围、下载路线、质量过滤、资源限制、调度和高级规则。
- [x] 显示订阅状态、最近/下次运行、缺失与 claim 摘要、blocked/needs-action 原因。
- [x] 覆盖权限、未知 coverage 不触发下载、按季订阅和完整策略序列化测试。

## 3. 下载兼容合同与 Follow 四层防护

- [x] 建立共享 SiteType/SourceKind/Downloader/Storage/Connection/MediaLibrary compatibility helper。
- [x] PT torrent 在 115 提交边界 fail closed；权威 BT Site 返回 torrent 时复用安全 bencode/infohash helper 转 magnet 后提交。
- [x] 扩充 Follow option DTO 的非敏感类型/关系事实，defaults 选择完整兼容元组。
- [x] WebUI 按“目标媒体库 → Downloader → 站点”联动，切换上游时清除失效草稿并解释原因。
- [x] 在 Follow Create/Update、Worker 搜索前、Site resolve 和 Download submit/BeforePersist 加入权威校验。
- [x] 旧不兼容订阅进入 blocked；确保不访问站点下载端点、不创建 DownloadTask。

## 4. 115 新建下载统一入口

- [x] 保留“下载管理 → 新建下载”，删除任何独立 115 转存侧栏/路由计划。
- [x] 选择 115 Downloader 后显示“离线下载 / 分享转存”，复用其下载目录和兼容目标媒体库列表。
- [x] 分享输入提交后清空且不进入 local/session storage；Server 继续加密来源。
- [x] 任务 UI 区分下载/转存完成、清单复核、识别、整理、入库和 needs-action。
- [x] 删除 MediaLibrary UI/API 对“自动摄取中转目录/绑定 115 下载器”的新配置与校验入口，保留 legacy 读取兼容。

## 5. Downloader 级“自动监听生活事件”

- [x] 为 115 Downloader 增加 `自动监听生活事件` 配置、DTO、保存校验和设置说明；不增加第二个目录字段。
- [x] 保存/启用时校验下载目录与同 Connection 其它监听目录、最终媒体库根不重叠。
- [x] 将原生离线与分享转存统一改为先幂等创建 `omc-<task-id>` 子目录，再向 provider 提交，并冻结目录 identity。
- [x] 让重试先 reconcile 原 provider task 和固定子目录，避免重复目录与重复提交。
- [x] 建立 Downloader-scoped 目录 supervisor：生活事件唤醒 + 有界周期补偿扫描。
- [x] 扫描仅处理普通直接子项，跳过 `omc-*`；对手工同名前缀记录安全告警。
- [x] 实现候选静默窗口与连续清单稳定复核，避免接管尚在转存的目录。
- [x] 使用 `Connection + Downloader + provider item ID` 摘要和数据库唯一索引原子 claim；重复事件、并发 sweep、重启幂等。
- [x] 手工接管任务引用所属 Downloader/Storage，只在同一 115 Connection 内按既有分类/Profile/目标规则入库；无法唯一分类进入 needs-action。
- [x] 保留 legacy intake DownloadTask 冻结路线；迁移不触碰真实 115 文件或任务。

## 6. 115 状态、重试和 Follow claim

- [x] 修正 115driver `0/1/2/-1` 映射及未知状态的可重试处理。
- [x] 更新 adapter 错误断言，增加 queued→running→completed Worker 回归。
- [x] 验证显式重试复用仍在运行或已完成的 provider task/output。
- [x] 下载失败、取消、完成和最终入库状态正确同步 Follow episode claim。
- [x] 全阶段提供文件保留型“取消”：provider-first `Cancel(..., false)` 删除下载器任务、保留文件，再停止 OhMyCine 后续流水线并进入 cancelled 历史；默认删除同样保留文件，显式完全删除才传 `true`。
- [x] 补齐 Submit/Cancel 竞态：迟到 provider ID 持久化后立即 `Cancel(..., false)`，失败保留安全诊断与重试事实。

## 7. 文档与长期合同

- [x] 更新 `.trellis/spec/backend/download-route-selection.md`，固化 PT/115 兼容矩阵、统一目录所有权和生活事件幂等规则。
- [x] 更新 `docs/architecture/02-server-design.md` 中搜索、Follow 与 115 Downloader/生活事件描述。
- [x] 若 OpenAPI 合同文件存在，更新新字段、选项 DTO、错误码与 deprecated intake 字段（仓库当前不存在该合同文件）。

## 8. 自动化验证

- [x] `cd server; go test ./internal/services ./pkg/cloud/pan115 ./pkg/downloader/pan115offline`
- [x] `cd server; go test ./...`
- [x] `cd server; go vet ./...`
- [x] `cd server/webui; npm run typecheck`
- [x] `cd server/webui; npm run test -- --run`
- [x] `cd server/webui; npm run build`
- [x] `git diff --check`

重点测试矩阵：

- [x] 搜索：poster/detail provenance、direct TMDB ID、JSON/SSE/session `items:null`、单站失败。
- [x] Follow：defaults/Create/Update/Worker 四层合法与伪造组合、运行时配置漂移。
- [x] 路线：PT torrent→qBit 允许、PT/torrent→115 拒绝、BT torrent→magnet→同账号 115 允许、BT magnet→同账号 115 允许、跨账号/local 拒绝。
- [x] 取消：provider 已完成且识别/Transfer/Import 失败仍可取消；provider 任务删除但文件保留、Follow claim 释放、provider 失败不伪取消、cancelled 历史与默认/完全删除幂等。
- [x] 115 所有权：OMC 离线/分享目录被监听跳过；手工直接子项被接管；同一 item 并发扫描只建一单。
- [x] 稳定性：生活事件早到、重复、漏报、分页、重启恢复、转存未完成、保留前缀、目录重叠。
- [x] 安全：分享密钥/路径/provider identity 不进入 DTO、事件、日志、审计或浏览器持久化。
- [x] 兼容：legacy intake 配置/任务可读可完成，不自动迁移 provider 工作。

## 9. 浏览器验收（只读或隔离数据）

- [ ] 搜索不白屏，标签和按钮文案正确，两种搜索路由可观察地区分。
- [ ] 海报详情显示库存覆盖、多语言资源结果与订阅入口。
- [ ] Follow 下拉按兼容路线联动，PT 不再与 115 同时可选。
- [ ] 新建下载选 115 后显示“离线下载 / 分享转存”，没有独立侧栏或第二个目录。
- [ ] MediaLibrary 无“自动摄取中转目录”，Downloader 显示“自动监听生活事件”。
- [ ] status 1 显示下载中；验证过程不重试、删除或操作用户真实任务。

## 风险与回滚点

- Follow DTO、WebUI 联动和 Server validation 必须同批发布，防止短暂接受非法组合。
- 普通离线改到 `omc-*` 子目录会改变新任务 provider 保存位置；必须验证 115 API 对子目录的稳定支持和完成清单边界。
- 生活事件监听与 Download Worker 共享 Connection 限速，需合并唤醒并避免事件风暴放大 provider 请求。
- legacy 字段只退出现行配置，不在本任务物理删除列，避免破坏在途任务。
- 任何失败回滚均保留最终 PT→115、未知 torrent→115 和跨 Connection 拒绝规则，同时不得误伤权威 BT torrent 的安全 magnet bridge。
