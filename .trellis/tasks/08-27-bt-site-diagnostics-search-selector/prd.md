# 公开 BT 诊断、可选站点搜索与 115 整理修复

## Goal

让资源发现、公开 BT 访问和 115 自动整理形成可解释、可恢复的闭环：用户在搜索前明确选择参与站点；Server 能区分站点故障、适配器漂移和 Cloudflare 挑战；115 多季剧集按逐文件季集事实入库，并在不过度保守的前提下执行必要的接口风控。

## Background and confirmed facts

- 当前聚合搜索只支持“不传 `site_id` 搜索全部”或“传一个 `site_id` 固定单站”，没有多站 `site_ids` 合同。
- AniDex 根站与 RSS 当前均为 HTTP 502；The Pirate Bay 在当前网络超时；YTS 在本机代理链路解析到 fake-IP 后 TLS EOF。这三类不能靠放宽解析器伪装成可用。
- ACG.RIP 的内建 RSS 路径已过期：旧 `/feed.xml?term=` 为 404，当前 `/.xml?term=` 返回 XML。
- EZTV 的 `size_bytes` 当前可能为 JSON 数字字符串，现有 `int64` 解码会使整包失败。
- 1337x 与 EXT.to 返回 Cloudflare “Just a moment” 403；普通 HTTP 或只换 User-Agent 无法通过。
- LimeTorrents 已从受控 `.lol` 地址跳转到 `www.limetorrents.fun`；需要更新精确官网目录并只允许显式受控 host 集合内跳转。
- MoviePilot 当前默认 `BROWSER_EMULATION=cloakbrowser`，同时支持 `flaresolverr`。获取源码时 FlareSolverr 可直接返回 solution；需要页面操作时可先取 FlareSolverr Cookie/UA，再启动 CloakBrowser。MoviePilot 会持久化 CloakBrowser 内核缓存并在启动时检查/安装。
- CloakBrowser wrapper 为 MIT，但编译浏览器二进制禁止随产品重新分发/打包；依赖声明并让最终用户从官方渠道下载是许可明确允许的方式。最新版还存在许可/并发会话约束。
- 用户附件和运行库中的实际任务 `764db4df-6565-4f55-af9f-4f190db66d5b` 证明源包含 S01–S04 共 28 个视频。`identity_snapshot.episodes` 已逐文件正确保存季集号，但任务级 `scrape_season=2`。
- `transferEpisodeFactsForManifest` 在逐文件解析后又把 `DownloadTask.ScrapeSeason` 无条件写回所有视频，因此目标计划全部变成 `Season 02/S02E..`。这是逐文件事实被任务级兜底覆盖的确定性跨层 bug。
- 当前受影响 TransferTask `5dd1cac0-aef0-4027-9877-5efeb677ef5a` 仍在执行，已持久化错误的 28 项 S02 计划。修复不能只影响新任务，还必须提供基于原始 manifest/provider item identity 的安全重新整理路径。
- MoviePilot `p115disk` 对 list、move、copy、rename、delete 等操作使用独立限流器，写操作常见为每 2 秒一次；路径查询单独每秒一次并优先命中目录 ID 缓存。健康状态下的目录创建没有再套一个“每次固定 2 秒”的全局 mutation 闸门。`p115strmhelper` 还按 endpoint 配置 cooldown，并对生活事件在 cookie/app API 间轮换，连续 405 时暂时切换接口。
- MoviePilot-Plugins 的整理接管还会按目标目录合并文件 ID，调用 115 批量 move/copy，再逐项更新缓存和任务状态；这说明“风险控制”应围绕具体 endpoint、可对账批次和真实错误，而不是把所有目录与文件操作永久串成一条慢队列。
- OhMyCine 当前让 mkdir/move/copy/rename/recycle 共用一个每 2 秒一次的 `mutationRate`，并在每个文件执行多次 Stat/List/Move/Rename，导致不同写操作互相阻塞；UI“逐个准备目录”的说明把主要耗时归因错了。
- 用户截图中的“屌丝男士”任务在 23/28 时取消，Transfer/Download Job 均已 terminal，但来源完整 manifest 有 38 项、分布在 5 个父目录。`record_and_source` 预览会对 38 项逐个执行带父级追溯的 115 Stat，而 Stat 共用每 2 秒一次的 list limiter；请求超过 HTTP 60 秒写超时后，WebUI 因无客户端超时/取消仍像永久卡住。已移动出来源根或被用户手工删除的项又被当成 boundary changed，导致等待后仍无法收敛删除。

## Requirements

### A. 公开 BT 诊断与修复

- R1：为八个失败站点记录可复现的状态与归因，区分远端故障、本机 DNS/TLS/代理、Cloudflare 挑战、官网迁移和响应结构变化。
- R2：修复 ACG.RIP RSS、EZTV 数字字符串和 LimeTorrents 精确受控域名跳转，并增加真实结构回归夹具。
- R3：AniDex、YTS、The Pirate Bay 等外部链路故障必须显示安全、可行动的诊断，不绕过连接测试保存为“在线”。
- R4：PT 私有源仍不可提交到 115；公开 BT/Torznab 和未知来源的既有 fail-closed 下载路由不得被浏览器仿真绕开。

### B. 搜索前站点选择

- R5：普通标题直接搜索和海报详情资源搜索，在首次发起请求前弹出站点选择框；固定 `site_id` 单站入口保持直接搜索，不重复弹窗。
- R6：弹窗只展示当前用户可见、已启用、具备搜索能力的站点，并显示 PT/BT、健康状态和不可用原因。
- R7：支持全选、取消全选、逐项勾选；零选择时禁用确认并给出明确提示。
- R7a：站点选择首次默认全选；后续恢复当前浏览器上一次勾选结果。新增加或重新启用的站点不静默加入旧选择，用户可通过“全选”快速补齐。
- R8：新增有数量上限、去重且权限重验的 `site_ids` 合同。JSON、SSE、多语言身份搜索、失败重试、分页和会话恢复必须绑定同一站点集合，不能静默扩大为全站。
- R9：提供只含安全站点摘要的 DiscoveryRead 选项接口；普通用户不能借此读取 Cookie、passkey、Base URL 私有配置或管理字段。

### C. Cloudflare 浏览器能力

- R10：定义统一、受控的 BrowserChallengeSolver/RenderedFetch 边界，至少支持外部 FlareSolverr 和可选 CloakBrowser companion；站点 adapter 只声明是否需要渲染，不直接管理进程、Cookie 或任意 URL。
- R11：FlareSolverr 作为无需安装内核的兼容后端；CloakBrowser 作为能力更强的推荐后端，但不得把其受限二进制打进 OhMyCine 安装包或镜像。
- R12：CloakBrowser 的“内置”含义是 Server 内置配置、健康检查、缓存目录和受控 companion 生命周期；用户显式安装/接受许可后从官方渠道取得二进制。缺失、无许可或启动失败时清楚降级到 FlareSolverr/Torznab，而不是假装挑战已解决。
- R13：只允许当前已解析并匹配受控站点 profile 的 HTTPS URL；禁止任意 URL 浏览器代理、跨站 Cookie、内网地址、凭据日志和无限响应。每站隔离会话，限制并发、超时、HTML 大小并保证进程/页面清理。

### D. 115 多季季号修复和恢复

- R14：逐文件结构化季集事实优先于自动任务级 `scrape_season/scrape_episode`；任务级值仅在没有逐文件证据且语义不冲突时兜底。人工明确 override 保持最高优先级，但不得意外覆盖与其无关的多季文件。
- R15：Transfer 校验、路径规划、持久 plan summary、MediaManagedItem、catalog 和重新整理必须使用同一份逐文件事实，禁止各层重新解析后漂移。
- R16：受影响的既有错误任务可使用 Transfer 原始 manifest、identity snapshot episodes 和稳定 provider item ID 生成 S01–S04 修复预览；确认后只移动/重命名 OhMyCine 托管项，不删除源外文件，不自动破坏当前数据。
- R17：用用户这份完整真实目录/文件名作为回归夹具，覆盖四季重复 E01–E08、中文“第 N 季”和 `Sxx.Exx` 并存、路径前缀和不同大小。

### E. 115 风控与进度文案

- R18：保留 115 风控保护，但把单一全局 mutation limiter 拆为独立 endpoint/operation 节流；目录查询/创建、move、copy、rename、delete/offline 不因无关操作永久串行。健康状态下 mkdir 不设置固定 2 秒等待，只受有界并发、幂等去重和真实风险控制约束。
- R19：先一次性构建并持久化目录 DAG，复用父目录 listing/ID 和冲突快照；同一任务/重试不为每个文件重复验证或创建同一目录。只有 405、429 或 provider 明确返回“频繁/风控”时，才触发带 jitter 的共享指数退避和熔断；普通耗时和排队不得标成风控。
- R20：优先评估 MoviePilot-Plugins 已采用的“按目标目录批量 move/copy”模式；只有当前 SDK/115 API 能返回或可查询出逐项结果、且部分成功可安全对账时才启用。否则保持单项幂等执行，不为追求速度牺牲恢复边界。
- R21：UI 正常阶段显示“检查目标目录”“检查冲突”“移动文件”“重命名”“结果对账”等真实动作；仅实际进入退避时显示“115 风控退避，预计 N 秒后重试”，并持续显示已完成/总文件数。删除“115 正按风控限速逐个准备目录并入库”这类笼统文案。

### F. 删除预览收敛与可恢复性

- R22：删除媒体整理记录必须采用收敛语义。用户已在 115/下载器手工删除 provider 任务、输出根目录或部分源文件时，这些对象按“已不存在/已删除”计数并允许继续清理 OhMyCine 记录，不能把外部缺失当作边界损坏。
- R23：`record_only` 预览不得访问 115、下载器或逐项核对文件；只校验本地记录权限和是否存在真正仍运行的本地 worker，须快速返回。
- R24：包含源文件或媒体库文件的预览应按根目录/父目录批量读取并对账 provider item ID，禁止对 20–30 个同目录文件串行执行受限 Stat。预览必须有 Server 端总超时、客户端可取消/超时和明确错误；不能无限显示“正在核对”。
- R25：只有仍可能写入或持有清单的本地 job/worker 才阻止删除。外部 provider 状态缺失、历史任务状态陈旧或文件已不存在时，应先安全对账/收敛本地状态，再允许用户完成所选范围；不得删除所选范围之外的文件。
- R26：115 `record_and_source` 只能删除仍可证明位于该任务不可变 `provider_output_id` 来源根内的残留项。已移动到媒体库或离开来源根的 provider item 记为 detached 并保留；禁止仅凭历史 item ID 跨边界回收。来源根整体不存在时视为来源已清理完成。

### G. Server-only Beta 发布

- R27：本次完成后发布的是 Server Beta，不得为满足旧工作流前置条件而构建、上传或重新发布 Player。
- R28：Server Beta 使用不会触发 Player `v*.*.*` tag workflow 的独立 `server-vMAJOR.MINOR.PATCH` tag 和 prerelease；发布源必须是最新远端 `develop`，tag 必须精确指向该提交。
- R29：Server Beta 必须由 CI 构建内嵌 WebUI 且注入官方只读 TMDB 凭据的 Windows x64 ZIP、Linux x64 tar.gz 和 SHA-256 清单；不得上传不含 WebUI 的普通 `go build` 产物。
- R30：既有共享 Player prerelease 上的 Server 资产保持可下载，不重写历史 tag/release；新的 Server-only 工作流需保持手动触发、版本校验、远端 develop tip 校验、Secret 校验和幂等资产上传。

## Acceptance Criteria

- [ ] AC1：八个失败站点均有诊断结果；ACG.RIP、EZTV、LimeTorrents 修复有回归测试。
- [ ] AC2：普通直接搜索和海报详情资源搜索均先选择站点；固定单站入口不重复弹窗。
- [ ] AC3：全选、取消全选、零选择禁用、不可用站点说明和键盘/移动端交互可测试。
- [ ] AC3a：首次打开默认全选；刷新或再次搜索时恢复当前浏览器上一次选择；站点新增后旧选择保持不变，“全选”能立即包含全部当前可选站点。
- [ ] AC4：只选部分站点时，Server 的 JSON/SSE/多语言搜索、重试和分页均不访问或返回未选站点。
- [ ] AC5：1337x/EXT.to 在配置了可用 solver 时通过受控页面获取；solver 缺失或失败时返回站点级错误且不影响其它站点。
- [ ] AC6：FlareSolverr URL、代理凭据、站点 Cookie 和 CloakBrowser profile 不进入普通 DTO、SSE、日志、审计详情或浏览器存储。
- [ ] AC7：四季真实 fixture 生成 28 个唯一目标：S01 6 集、S02 6 集、S03 8 集、S04 8 集；不存在跨季覆盖或重复目标。
- [ ] AC8：任务级自动 `scrape_season=2` 不再覆盖逐文件 S01/S03/S04；单集/单季包和人工 override 既有行为不回归。
- [ ] AC9：已有错误任务能生成安全修复预览并重新整理为四季；provider identity/边界变化时 fail closed。
- [ ] AC10：115 目录被去重准备；同一路径只查询/创建一次，健康响应下 mkdir 不固定等待 2 秒，且不再由一个全局 mutation limiter 把 mkdir、move、rename、delete 串行。独立操作节流、真实 405/429 风险退避和恢复测试通过。
- [ ] AC11：同一目标目录的多个文件在可对账时批量 move/copy；任意部分成功、超时或重试都不会重复移动、覆盖或丢失任务状态。
- [ ] AC12：页面显示真实阶段和进度；正常操作不显示“风控中”，只有实际 backoff 才显示风险原因和预计重试时间。
- [ ] AC13：相关 Go 单测、WebUI 测试、typecheck、lint、build 和 `git diff --check` 通过。
- [ ] AC14：已手工删除 115 离线任务、输出根目录、全部源文件或部分源文件时，删除预览在有界时间内返回，并把缺失项显示为“已不存在”；确认后可清理记录且不会触碰媒体库文件（除非用户明确选择媒体库范围）。
- [ ] AC15：`record_only` 不产生任何 provider API 调用；同一 115 父目录的多文件删除预览使用有界批量对账而非逐文件受限 Stat，并有超时、重试/返回和错误展示测试。
- [ ] AC16：23/28 取消、38 项来源清单、5 个父目录且无完整 MediaManagedItem 的真实形态可在 60 秒以内完成预览；移出来源根的项显示 detached 且不删除，来源根内残留才进入删除清单。
- [ ] AC17：从最新远端 `develop` 触发 Server Release 能创建/复用 `server-vX.Y.Z` prerelease 并上传 Windows/Linux/校验文件；不会触发 Player Release，也不会生成或上传任何 Player 资产。

## Out of Scope

- 绕过 PT 登录、验证码或站点授权。
- 接受模糊镜像域名、未知跨源 torrent 地址或把任意 URL 暴露给浏览器 solver。
- 在 OhMyCine 发布物中重新分发 CloakBrowser 专有二进制，或替用户绕过其许可/并发限制。
- 为 Server-only Beta 发布或更新 Player 安装包、Updater 清单、Player prerelease/tag。
- 为追求速度关闭所有 115 节流、重试边界或托管项验证。
- 未经预览确认自动移动、覆盖或删除受影响的既有媒体库文件。
