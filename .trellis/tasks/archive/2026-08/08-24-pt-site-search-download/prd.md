# PT / BT 站点连接与搜索下载

## Goal

以 PTTime 为首个内建适配器，让管理员安全配置 PT 站点，让获权用户聚合搜索真实资源并复用现有下载管线创建任务。

## Confirmed Facts

- PT 站点属于 Server 内建能力，不由插件安装。
- Server 已有下载器、种子/URL/磁力输入、加密下载源、目标媒体库选择、持久任务队列、任务历史、整理入库和安全取消逻辑。
- “站点管理”和“探索”路由已预留，但还没有站点模型、适配器或业务 API。

## Requirements

- 建立版本化 PT 站点适配器接口，至少覆盖连接测试、搜索、资源详情、种子获取、站点健康与认证失效分类。
- PTTime 首版支持真实登录态校验、分页搜索、分类/关键词筛选、排序和种子获取；具体字段与认证方式以真实站点契约研究为准。
- 站点连接与用户凭据分离于下载任务；搜索结果只持有短期安全 token，不把敏感下载 URL 发给浏览器。
- 搜索结果统一为站点无关 DTO，包含稳定结果 token、标题、副标题、媒体类型候选、大小、发布时间、做种/下载人数、促销、质量/分辨率、字幕/地区、站点和过期时间等可用字段。
- 搜索并发有全局上限和每站点上限；结果按站点隔离错误、去重并可分页。
- 发起下载时 Server 使用结果 token 重新校验/解析种子，并调用现有 `DownloadService`；不能信任浏览器提交的种子 URL、passkey、分类或媒体库物理路径。
- 站点凭据使用专用 purpose/AAD 加密；更新采用候选测试成功后原子替换，失败保留旧凭据。
- 新增细分站点权限；首版管理入口仍要求管理员，搜索沿用 `discovery.read`，下载沿用既有创建权限。
- 推荐作品点击后先进入作品详情，而不是立即执行 PT 搜索；详情页展示服务端重新获取的元数据，并提供“按标题搜索”“按 TMDB ID 搜索”和清晰标记为后续能力的“订阅”入口。
- 推荐图片必须通过同源、受控、大小受限的 Server 图片网关加载，不能依赖浏览器直连 TMDB/豆瓣图片域名，也不能形成任意 URL 代理。
- 站点管理新增 CookieCloud 设置，支持自建/公共远程 CookieCloud 与 Server 本地 CookieCloud 接收端；同步后的 Cookie 只在 Server 解密、按站点域名筛选、候选测试成功后原子替换。
- CookieCloud 在站点列表为空时也会从受支持域名发现站点；只有通过内建适配器验证的 PTTime Cookie 才能自动创建加密站点，未知域名只跳过。
- 新增站点使用“选择连接类型 → 填写类型配置”两步向导。首版只开放 PT，按钮统一命名为“添加”，为后续非 PT 连接类型保留扩展点。
- PT 站点支持可选浏览器仿真请求模式；首版使用管理员配置的 FlareSolverr 兼容服务完成受控页面渲染/Cloudflare 会话更新，不在 Web UI 捕获或回显站点 Cookie。
- 每条 PT 搜索结果提供不触发下载的“识别”操作。Server 只从 actor 绑定的不透明结果令牌读取服务端标题 claim，复用共享名称识别器与 TMDB，返回中文规范标题、年份、媒体类型、同源海报和分辨率/来源/视频编码/音频编码/HDR 等发行规格。TMDB 未配置、不可用或无匹配时仍返回可读的本地解析摘要。
- CookieCloud 同步摘要必须用计数解释静默跳过：分别报告 CookieCloud 中当前不是受支持站点的其他域名，以及受支持候选缺少有效登录 Cookie 的数量；不得返回原始域名或 Cookie 名称。
- 站点支持必须采用“内建站点目录 → 通用解析引擎 → 特殊站点覆盖”的结构。标准 NexusPHP 站点共享请求与解析实现，CookieCloud 按目录域名一次发现多个站点；未收录的标准 NexusPHP 站点允许管理员手动接入，非 NexusPHP 站点不能假装兼容。
- 首批真实多站回归必须包含 PTTime、下水道（SewerPT，`https://sewerpt.com`）和熊猫高清（PandaPT，`https://pandapt.net`）。三者共享受控 NexusPHP 引擎，但保持独立稳定键、目录身份和 CookieCloud 发现候选；PandaPT 首轮覆盖普通视频 `torrents.php`，不把尚未实现的 `special.php` 音频专区伪装成已支持。
- 站点管理同时支持 PT 与公开 BT 索引，首批 BT 目录包含 Nyaa、AnimeTosho、Tokyo Toshokan、Mikan 与 AniDex；另提供通用 Torznab 入口以连接 Jackett/Prowlarr。
- BT 站点不伪造 PT Cookie/passkey 语义：公开 RSS 站点默认无凭据，Torznab API Key 使用同一站点 AES-GCM envelope 但单独命名。CookieCloud 只发现和更新声明支持 Cookie 的 PT 站点。
- BT RSS/Torznab 结果可在 Server 内解析为受控 `.torrent` 或规范化 magnet，但浏览器仍只收到 actor 绑定的短期结果令牌；确认后复用现有 DownloadService 和后续识别/整理/入库链路。
- 搜索 API 新增通用 torrent 路由并保留旧 PT 路由兼容；管理端使用通用路由、站点类型标签与 PT/BT 分步添加向导。

## Acceptance Criteria

- [ ] AC1: PTTime 正确凭据可通过测试并加密保存；错误凭据返回稳定错误码且旧配置不丢失。
- [ ] AC2: PTTime 搜索可返回真实分页结果，字段经过边界清洗，畸形条目被跳过并计数。
- [ ] AC3: 多站点并发搜索时单站失败只影响对应分组，并且用户可以只重试该站。
- [ ] AC4: 敏感种子地址只在 Server 内部短期解析；浏览器、数据库普通字段、日志和任务 payload 看不到 passkey。
- [ ] AC5: 从搜索结果创建的任务与手动上传种子创建的任务进入同一下载、完成复核、整理和入库流程。
- [ ] AC6: 搜索缓存、限速、超时、重试和认证失效状态均有测试与可筛选安全日志。
- [ ] AC7: 站点管理页面支持新增、编辑、测试、启停、删除前确认和状态展示，亮/暗主题与移动窄屏可用。
- [ ] AC8: 推荐海报、背景和详情图片均从同源受控端点加载，外部图片不可用时保留可读占位，不影响其它栏目。
- [ ] AC9: 推荐作品进入详情页后，用户可明确选择按标题或 TMDB ID 搜索；订阅入口不会误创建任务，并说明后续能力。
- [ ] AC10: CookieCloud 远程/本地模式可保存、测试和立即同步，失败保留站点旧 Cookie；任何 API、日志或页面状态均不含 CookieCloud 密码或同步 Cookie。
- [ ] AC11: 添加站点先选择 PT 类型再进入配置，浏览器仿真开关只在配置了受控仿真服务时生效。
- [ ] AC12: 首次 CookieCloud 同步可自动创建并显示通过验证的 PTTime 站点，同步摘要分别报告新增、更新、跳过和失败数量。
- [ ] AC13: 用户可对任意仍有效的 PT 搜索结果执行快速识别；操作不获取种子、不消费下载令牌、不创建任务，且越权/过期令牌统一失败。
- [ ] AC14: 快速识别在 TMDB 正常时返回规范中文元数据与同源海报，在 TMDB 缺失或无匹配时仍以 HTTP 200 返回本地标题/年份/发行规格和稳定错误码。
- [ ] AC15: CookieCloud 同步 UI 能以不含域名和 Cookie 的计数说明为何只发现部分站点，并明确其它域名不代表已支持的 PT 站点。
- [ ] AC16: 内建站点目录至少覆盖首批常见 NexusPHP 站点，目录键、适配器和 CookieCloud 发现保持一致；一次同步含多个已支持域名时分别验证并创建多个站点。
- [ ] AC17: 站点管理新增目录选择与通用 NexusPHP 入口；NexusPHP 嵌套表格不会把同一种子重复解析为多条结果。
- [ ] AC18: CookieCloud 同批包含 PTTime、SewerPT 和 PandaPT Cookie 时会分别验证并创建三个站点；PandaPT 嵌套标题表格仍从外层种子行取得大小和做种统计，且标题链接缺少 `title` 属性时回退到可见文本。
- [ ] AC19: 站点向导可分别选择 PT 和 BT；目录/API/卡片显示稳定 `site_type`，且旧 PT 记录无需数据迁移即可正确派生类型。
- [ ] AC20: Nyaa 的 RSS fixture 可解析标题、大小、时间、做种/下载/完成数与同源 torrent；AnimeTosho、Tokyo Toshokan、Mikan、AniDex 具有独立目录 profile 和受控查询合同。
- [ ] AC21: Torznab 使用加密 API Key 执行 caps/search，能从 namespaced attr/enclosure 解析 torrent 或 magnet；API Key、magnet 和 torrent URL 不进入 REST/SSE/日志/审计。
- [ ] AC22: 通用 torrent 搜索端点同时渐进返回 PT/BT 分组，旧 `pt-search` 兼容端点仍可用；BT 确认下载复用现有下载器、媒体库和整理队列。

## Out of Scope

- 自动订阅、RSS、刷流、签到、魔力值、站点消息、邀请和用户数据统计。
- PT 插件市场和任意脚本型站点适配器。

## Delivery Constraint

- 首版按 PTTime 当前 NexusPHP 兼容页面契约实现 Cookie 登录、分页搜索和种子获取，同时把页面路径与解析器封装在 PTTime adapter 内。自动化测试使用脱敏固定 fixture；真实账户联调属于上线验收，不允许为了绕过未知页面结构而把 Cookie 或真实种子 URL 暴露给浏览器。
