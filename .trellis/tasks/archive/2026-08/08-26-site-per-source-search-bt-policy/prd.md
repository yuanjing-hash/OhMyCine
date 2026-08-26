# 站点单站搜索、地址驱动 BT 与统一媒体识别

## Goal

在复用现有 PT/BT 聚合搜索、下载和整理流水线的前提下，一次性交付两组互相衔接的能力：

1. 站点管理中的单站搜索，以及 MoviePilot 式“Server 内置适配能力、用户输入官网后才创建”的 BT 接入。
2. 把下载前、下载完成后、入库前的重复识别改为一份可版本化、可人工锁定的媒体身份快照；低置信度不再阻塞，普通规则不足时可选用 Server AI 辅助，识别错误后可安全地重新整理到正确位置。

最终体验应是：有候选就稳定选择最高候选，没有 TMDB 候选也进入可见的暂定身份/待整理状态；任何阶段人工确认一次，后续下载、文件筛选、Transfer、重试和重新整理都继承该结果，不再反复要求人工介入。

## Background and Confirmed Facts

- PT/BT 聚合搜索、结果识别、下载提交和后续整理入库已经存在，本任务不创建平行流水线。
- Server 搜索合同已经支持 `site_id`，Explore 页面也已有分页、筛选/排序、快速识别、人工识别和下载入库；单站入口主要缺站点卡片导航和固定上下文状态。
- 当前 `server/pkg/site/builtin/catalog.go` 会把 Nyaa、AnimeTosho、Tokyo Toshokan、Mikan、AniDex 直接暴露为可选目录项，不符合“用户先输入官网再出现”的交互。
- MoviePilot 的法律动机不是可验证事实。本任务只落实用户明确选择的产品边界：适配器可随 Server 发布，但未由管理员显式添加的公共 BT 站不展示、不搜索、不访问。
- 用户日志 `C:\Users\VibeCoder\Downloads\runtime (1).jsonl` 显示人工 override 已返回 200，原 Job 也已重新运行；完整清单有 10 个视频，但安全清单只剩 1 个，随后 Transfer 返回 `transfer_media_unrecognized`。
- 该实例中 TMDB 103962 已由 `GetByID` 验证，直接身份的 confidence 为 1；真正失败点是 `[01][BIG5_MP4][1920X1080]` 这类动漫集号未被统一解析，旧 selector 又错误退化为只保留最大视频。
- 当前产品把“候选匹配置信度”和“文件是否能被安全转移”混成了一个总闸门，并在下载前、完成后、Transfer 前重复搜索/排名，导致人工结果和先前识别结果被后续阶段覆盖或遗忘。
- 当前统一 ranker 的默认 `MatchThreshold` 为 0.78、精确标题门槛 0.68、保守拼写门槛 0.64、候选冲突间距 0.06；`transfer.go` 还存在独立的 0.80 硬门槛。新设计必须以统一 decision/reason 触发 AI 或暂定选择，不能继续保留跨阶段散落阈值。
- `docs/architecture/02-server-design.md` 已预留 AI Provider、API Key、Model 和 Base URL 设置方向；Server 当前没有可复用的 AI 媒体识别实现。

## Requirements

### R1. 站点卡片单站点搜索

- 每个已配置、已启用且适配器支持搜索的站点卡片显示“搜索”按钮。
- 点击后进入现有搜索页，并携带不可丢失的站点 ID；首次搜索、重试、分页、识别和入库都保持该单站上下文。
- 固定单站模式不得静默回退全站聚合；目标站失败只展示该站错误。
- 停用、异常或无搜索能力的站点禁用入口并说明原因；全局聚合搜索保持不变。

### R2. BT 地址驱动发现与创建

- “添加 → BT”不展示具体公共 BT 站清单，管理员首先输入 HTTPS 官网根地址。
- Server 规范化 URL 后按内建注册表精确匹配 host，返回站点名称、稳定 kind、连接方式和能力；浏览器提交的 kind 不可信，创建时 Server 必须再次解析。
- 只有识别、连接测试和管理员保存均成功后，站点才持久化、显示并参与搜索。
- 未识别地址不套用相似解析器，提示尚未原生支持，并保留 Torznab（Jackett/Prowlarr）作为独立通用入口。
- PT 添加、CookieCloud 同步、浏览器模拟登录以及现有 Cookie/passkey 行为不回归。

### R3. 内建 BT 首批范围

- 保留现有：Nyaa、AnimeTosho、Tokyo Toshokan、Mikan、AniDex。
- 新增：动漫花园（DMHY）、ACG.RIP、YTS、EZTV、1337x、The Pirate Bay、EXT.to、LimeTorrents。
- RSS/API 站优先用稳定 RSS/API；HTML 站使用独立适配器和 fixture，不跨站复用脆弱选择器。
- 只覆盖影视/动漫搜索和受控 magnet/种子解析，不扩展游戏、软件等非媒体库内容。
- 站点换域必须通过版本化 registry 更新；不自动发现或信任未知镜像。

### R4. BT 网络与凭据安全

- 官网只接受规范 HTTPS 根地址，无 userinfo、query、fragment；host 采用 IDNA 规范化后的精确域名/显式别名匹配。
- 测试、搜索和下载源解析统一使用受控 HTTP Client：超时、响应上限、重定向上限、允许 host、限速与并发限制。
- 浏览器只获取安全 DTO 与 actor 绑定的短期 claim；magnet、torrent URL、Cookie、passkey、API Key 和上游正文不进入浏览器状态或日志。
- 添加、更新、测试、启停、删除和单站搜索权限继续由 Server 校验并写脱敏审计。

### R5. 单一权威媒体身份快照

- DownloadTask/整理任务持久化版本化 `MediaIdentitySnapshot`，至少包含 TMDB ID、媒体类型、标题、年份、分类、来源、confidence、locked、revision、证据摘要和逐文件季集事实引用。
- 来源区分 `manual`、`direct_id`、`automatic`、`ai`、`local_provisional`；人工确认经 `GetByID` 验证后设为 `manual + locked`。
- 下载前轻量识别只负责建立初始身份；下载完成后只根据真实清单补全逐文件季集/版本事实；Transfer 只校验快照、目标和文件安全，不再进行第三次独立搜索与排名。
- 任一阶段完成人工介入后，下载、完成识别、Transfer、重试、修改目标和重新整理必须继承同一 revision；自动识别和 AI 都不能覆盖 locked 身份。
- 所有入口（全局搜索、单站搜索、手工下载、下载任务人工介入、整理历史重新识别）必须调用同一个 identity service，不能各自维护判定规则。

### R6. 置信度只排序，不阻塞

- confidence 只用于候选排序、UI 风险提示和后续纠错优先级，不再作为下载或媒体身份入库的硬阻塞条件。
- 高可信且无冲突时直接使用当前 ranker 结果；AI 已启用时，当前代码产生 `low_confidence` 或 `candidate_conflict` 会触发 AI，而不是失败或要求人工介入。
- 普通低置信度/同分冲突时使用“候选仲裁”模式：把原始发行标题、结构化解析事实和最多 5 个 TMDB 候选交给 AI，AI 只能选择输入中的 `candidate_ref`，或返回需要重写查询/无法判断，不能创造候选。
- AI 未配置或未开启时采用 MP 式宽容命中：只要 best total 与标题相似度均未低于 0.35 极低线，即使未达到 0.78、触发 `low_confidence` 或候选完全同分，也必须按统一稳定排序命中一个最高候选、标记 `provisional` 并继续，不进入人工等待。
- 极低置信度（首版默认 best total < 0.35、标题相似度 < 0.35，或 TMDB 无有效候选）使用“标题重写”模式：AI 提取标准标题、原始标题/别名、媒体类型、年份、季集和最多 5 条查询词，Server 再执行一次 TMDB 搜索与统一排名。
- 0.35 极低线进入版本化 ScoreConfig，并通过现有识别 corpus/新增真实样例校准；不允许在 handler、Transfer 或 UI 中再写独立 magic threshold。
- AI 仲裁或重搜后仍有多个/低分候选时，必须确定性选择一个最高结果：AI 已选择且 `candidate_ref` 合法时优先使用，否则按完整 rank、popularity、vote 和稳定 TMDB ID 打破同分。
- 高可信结果标记 `verified`；AI 选择或低分自动最高候选标记 `provisional` 并显示来源，但两者都可继续流水线。
- AI 未配置、调用失败或 TMDB 重搜仍无候选时建立 `local_provisional`，保留清理后的本地标题并进入未识别/待整理，而不是让任务失败、重复下载或堵塞队列。
- 身份不确定与文件结构不确定必须分离：身份可以 provisional；没有视频、路径越界、冲突待选、目标不可用、TV 多文件无法确定逐集对应等硬安全条件仍不得被 confidence 或 AI 绕过。

### R7. 通用动漫/发行命名解析与包级季集事实

- 预处理继续使用项目内置的完整 TV/anime 识别词规则，不得为修复单个作品删除、缩减或改成作品特判。
- 统一解析覆盖中英日标题、拼音/罗马音、字幕组前缀、别名、季/集、版本和发行规格；至少支持 `S01E01`、`1x01`、`EP01`、`第01集`、`- 01`、`[01]`、`[01v2]`。
- 方括号数字必须结合 TV 身份、相同命名骨架、多文件序列及 BIG5/语言/编码/分辨率等发布证据；不得把 `[1080p]`、`[10bit]`、`[1920X1080]`、年份或数字电影名误判为集号。
- 选择和 Transfer 消费同一份逐文件事实。TV 多视频包全部无集号时禁止最大文件 fallback，也不得只入一集；完整来源保持不动并进入“集号待整理”。
- 人工确认身份后如果季集事实可由包级规则确定，应自动创建一个包含全部有效剧集及关联字幕/伴随文件的 TransferTask，不再要求逐集手填。

### R8. Server AI 辅助识别

- 设置页新增“AI 媒体识别辅助”，首版 Provider 类型只支持 `OpenAI-compatible` 与 `Google AI Studio (Gemini native)`；不实现 Anthropic 等其它原生协议。
- OpenAI-compatible 支持管理员填写 Base URL、API Key、模型列表/手动模型名；Google AI Studio 使用固定官方 Generative Language API、API Key 和可调用 `generateContent` 的模型列表，不允许把 Google 类型改成任意 Base URL。
- 模型列表获取失败时仍允许管理员手填模型；AI 总开关默认关闭，只有 `enabled=true` 才允许识别 worker 自动调用 AI。
- 开关关闭时，下载前、下载完成、Transfer、重试、扫描和后台任务不得产生任何 AI 网络请求：低分/冲突直接按稳定最高候选建立 provisional，无候选则建立 `local_provisional`。管理员在设置页显式点击“测试连接/获取模型”属于配置动作，可在未启用时单独请求，不得触发媒体识别或改变任务。
- 每个 identity revision 最多执行一次候选仲裁和一次标题重写，禁止递归调用或无限重搜；下载完成和 Transfer 继承结果，不重复产生 AI 费用。
- 候选仲裁和标题重写使用两套独立系统提示词与 JSON Schema。输入标题/文件名被明确标记为不可信数据，模型不得把其中内容当作指令。
- OpenAI-compatible 优先使用 `response_format: json_schema`，仅在兼容端明确不支持时降级为 `json_object` 并执行同一服务端 Schema 校验；Google 使用 `responseMimeType: application/json` 与 `responseSchema`。
- AI 结果必须先通过长度、枚举、candidate_ref、季集范围和 JSON Schema 校验；模型自报 confidence 仅作诊断，不参与硬安全授权。
- AI 不直接执行下载、移动、删除、覆盖或配置修改，也不能把未经验证的 TMDB ID 锁定为权威身份。
- AI API Key 使用现有 AES-GCM 凭据存储、SecretInput/reveal 权限和审计；日志必须脱敏。
- 发送给 AI 的内容不得包含绝对路径、Cookie、token、provider item ID、magnet/torrent URL 或下载器信息；自定义 Base URL 使用受控 HTTP Client并执行 SSRF、重定向、超时和响应大小限制。

### R9. 识别错误后的重新整理

- 下载历史、媒体整理历史和媒体详情均可发起“修正识别并重新整理”；用户选择正确 TMDB 后先展示旧位置 → 新位置、重命名、字幕/伴随文件和冲突预览。
- 确认后创建幂等 reorganization Job，更新 locked identity revision，按当前媒体库规则移动/重命名托管媒体与字幕，重建 NFO/JPG/STRM，并通知 Player/Emby/Jellyfin 刷新。
- 重新整理只操作数据库/manifest 明确归 OhMyCine 管理的产物；不扫描猜测、删除非托管文件或绕过现有冲突策略。
- 仅在旧目录由系统创建、内容已成功迁移且目录为空时清理旧目录；本地、115、copy/move/symlink 分别复用现有驱动和安全边界。
- 失败可重试且不能丢失旧 identity/manifest；任务页面显示准备、预览、执行、部分失败和完成状态。

### R10. 状态、日志与兼容

- 人工确认后显示“正在重新识别并入库”；已确认身份但集号不足时显示“身份已确认，集号待整理”；低分自动结果显示“自动暂定”，不再显示泛化的“置信度未命中”。
- 日志记录 task/job/identity revision、source、候选数、selected rank、candidate_videos、episode_matched、selected_files 和稳定 reason code；不记录绝对路径、原文件名、凭据或 provider 私有身份。
- 旧任务在缺少 identity snapshot 时允许一次性从已有 recognition/override 字段回填；已有人工作用的旧任务迁移为 locked，不能被新自动流程覆盖。
- 现有 PT、CookieCloud、Torznab、BT stable kind、全局搜索、qBittorrent/115 完成清单和媒体库路由保持兼容。

### R11. 媒体整理四档删除范围

- 终态媒体整理记录提供四个明确选项：`仅删除转移记录`、`删除转移记录和源文件`、`删除转移记录和媒体库文件`、`删除转移记录、源文件和媒体库文件`。
- 旧 `DELETE /api/v1/transfers/{id}` 保持兼容并继续等价于“仅删除转移记录”；真实文件删除使用独立的预览/确认接口，不允许通过布尔参数静默扩大范围。
- 预览必须列出安全计数、来源/媒体库类型、缺失项、做种或活动重整阻断状态和将执行的动作，不向浏览器返回绝对路径、115 item ID、provider task ID 或凭据。
- 确认令牌绑定 actor、Transfer、Download、媒体库、删除 scope、managed manifest 摘要、identity revision、任务 revision 和短期过期时间；数据库仅保存令牌摘要。确认时重新验证全部绑定事实，变化后要求重新预览。
- 源文件只按 DownloadTask 的不可变完整/安全 manifest、暂存根、provider task 和稳定 115 item ID 删除；媒体库文件只按 `media_managed_items` 中 active + managed + 当前 Transfer 所有权删除，禁止扫描目录、猜测同名文件或删除 unmanaged sibling。
- 本地删除逐项重验 Storage/staging canonical root、普通文件和 symlink/junction/Reparse Point；115 删除逐项重验 Connection、root ancestry、provider item ID 与当前父级，并使用该驱动的回收/删除语义。
- 选择删除源文件时，若仍有活跃做种或运行中的下载/重整任务，预览必须阻断或确认路径先安全终止 provider/做种事实；不得留下会重新下载、继续做种或指向已删除来源的活动任务。
- 文件动作成功后再事务清理 Transfer/Download/Seeding/Reorganization 相关历史和 ownership；媒体库删除推进 dirty/content revision，触发 catalog、NFO/JPG/STRM、Player 与 Emby/Jellyfin 对账。部分失败保留记录和未完成 ownership，允许安全重试，不得伪装成删除成功。
- UI 使用服务端返回的四个 scope 能力和明确危险文案；包含文件的三档需要再次确认，按钮颜色不能成为唯一的风险提示。

## Acceptance Criteria

- [ ] AC1: 每个可搜索站点卡片提供“搜索”入口，搜索、重试和翻页始终只访问该站；全局搜索无回归。
- [ ] AC2: “添加 → BT”默认不显示具体公共站点；输入受支持官网、通过 Server 识别和测试后才出现站点卡片。
- [ ] AC3: 未知/相似 host、带 userinfo/query 的 URL、跨允许 host 重定向和伪造 kind 均被拒绝且不保存。
- [ ] AC4: 现有 5 个及新增 DMHY、ACG.RIP、YTS、EZTV、1337x、The Pirate Bay、EXT.to、LimeTorrents 都有官网识别、健康检查、搜索/分页和安全下载源 fixture；未配置站点不发网络请求。
- [ ] AC5: PT 添加、CookieCloud、浏览器模拟登录、Torznab和旧 BT 记录无需破坏性迁移即可继续工作。
- [ ] AC6: 同一下载从搜索到 Transfer 只产生并推进同一 identity revision；人工确认一次后，所有后续阶段和重试均显示并使用该 locked 身份。
- [ ] AC7: AI 已启用且 ranker 返回 `low_confidence` 或 `candidate_conflict` 时触发一次 AI 候选仲裁；合法 `candidate_ref` 被采用，非法/虚构引用被拒绝，仍保证确定性选中一个候选并继续。AI 关闭时同一输入产生零 AI 请求并直接稳定选择最高候选。
- [ ] AC8: AI 已启用且 best total/标题相似度低于极低线或 TMDB 无候选时触发一次 AI 标题重写，再以标准标题/别名重新搜索 TMDB；重搜仍为空则产生 `local_provisional`。AI 关闭时直接产生 `local_provisional`，两条路径都不阻塞队列。
- [ ] AC9: 真实 10 集 `[01][BIG5_MP4][1920X1080]` fixture 在人工确认 TMDB 103962 后不重提下载器，创建且只创建一个含 10 集的 TransferTask。
- [ ] AC10: `[01]`、`[01v2]` 及相关发布证据能生成逐集事实；`1080p`、`10bit`、分辨率、年份和数字片名反例不误判。
- [ ] AC11: TV 多视频包完全无法解析集号时不再只选最大文件，完整来源保持不动，身份快照仍保留，并显示专用“集号待整理”状态。
- [ ] AC12: 在下载前、下载完成或整理阶段任一入口人工改成正确 TMDB 后，后续自动继承且不再次人工介入。
- [ ] AC13: 对一个已错误入库的本地或 115 托管媒体执行重新整理，可预览并安全移动到新目录、更新命名/元数据/STRM、刷新下游；重复执行幂等，非托管文件不受影响。
- [ ] AC14: AI 总开关默认关闭且关闭时所有运行时流程产生零 AI 请求；OpenAI-compatible 与 Google AI Studio 均可显式测试连接、获取/手填模型并返回通过同一领域 Schema 的结果，API Key 加密保存，恶意 Base URL、越权 reveal、超大/非 JSON 响应均被拒绝。
- [ ] AC15: Server/Web UI 测试覆盖 identity migration、AI 候选仲裁、AI 标题重写、极低阈值边界、两种协议、调用次数上限、失败回退、人工锁、防覆盖、重新整理回滚以及站点网络安全。
- [ ] AC16: 媒体整理终态记录可按四档范围预览并确认删除；record-only 不触碰文件，source/library/both 只删除绑定的托管项，本地与 115 均覆盖越界、身份变化、做种/活动任务、重复确认、部分失败和审计回归测试。

## Out of Scope

- 用 AI 直接决定并执行下载、文件删除、覆盖或媒体库配置修改；本任务新增的删除只能由用户显式选择 scope 并完成服务端预览/确认。
- 为每一部识别失败作品添加硬编码别名；作品级修正应来自人工 identity、规则管理或经过验证的元数据候选。
- 本阶段提供通用多文件逐集手工映射编辑器；自动包级规则无法确定的任务先进入“集号待整理”，后续可单独扩展批量映射 UI。
- 自动添加/启用/探测公共 BT 站，自动信任镜像，或在运行时接受任意用户 CSS/HTML 解析器。
- 重做下载器并发、媒体库排序、目标选择和已有冲突策略。
- 把 Server AI 设置同步到 Player，或让第三方站点/插件默认获得 AI 凭据。

## Key Product Decisions

- BT 采用“核心内置适配器、用户输入官网后显式创建”；未配置站点完全静默。
- 单站搜索复用现有 Explore 和下载流水线，不建设第二套结果页。
- 三次识别改成一次身份建立、一次文件事实补全、一次安全校验；没有三个互相竞争的识别结果。
- 人工身份优先级最高且锁定；AI 和自动识别永不覆盖人工结果。
- 置信度从硬闸门降为分流/提示信息；AI 关闭时普通低分与同分直接命中最高，AI 开启时交给 AI 仲裁，只有极低分/无候选进入标题重写或本地待整理。
- “媒体是谁”与“每个文件是哪一集/是否安全可移动”分开处理；前者可暂定，后者不能靠猜测跨过文件安全边界。
- AI 是低置信度/候选冲突的可选仲裁器，也是极低置信度/无候选时的标题重写器；默认关闭，输出必须通过严格 Schema 和 Server 再验证。
- 识别错不要求用户手动清目录；由可预览、可重试、仅操作托管产物的重新整理 Job 修正。
