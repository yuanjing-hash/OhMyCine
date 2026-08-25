# 技术设计

本子任务实现内建 PT adapter、PTTime NexusPHP 兼容实现、加密站点配置、健康测试、聚合搜索、SSE、短期不透明结果令牌和现有下载服务桥接。浏览器、任务普通载荷、日志和审计永不出现 Cookie、passkey 或真实种子 URL。

站点每个独立超时/限速/并发；多站并发、站内顺序翻页。候选凭据测试成功后 CAS 替换；下载时根据令牌重新解析并获取种子，再调用现有 `DownloadService.Submit`。

推荐 DTO 中的上游图片 URL 在服务返回前转换为 provider 绑定的 Base64URL 身份，浏览器只请求同源图片端点。端点重新校验 TMDB 当前图片前缀或豆瓣固定域名、HTTPS、重定向、Content-Type 与响应大小。作品详情通过 TMDB ID 重新获取完整快照；豆瓣条目先从缓存恢复标题/年份，再由 Server 映射到 TMDB，失败时仍返回有限的豆瓣快照。

CookieCloud 使用单例设置与专用 AES-GCM purpose 保存 Server URL、UUID、端到端密码和本地上传共享密钥。本地接收端仅保存 CookieCloud 原始密文；同步时在内存中解密，按各站点 Base URL 的域名选择 Cookie，调用站点 adapter 测试后以 revision CAS 更新该站点 Cookie，失败不覆盖旧值。远程访问限制 HTTP(S) scheme、响应大小、重定向和超时；本地模式要求共享认证头。

浏览器仿真是站点请求策略而非前端代填密码：启用后 PT adapter 把当前候选 Cookie 交给管理员配置的 FlareSolverr 兼容 `/v1` 接口并获取渲染后的页面，再按原站点契约解析。FlareSolverr 返回的临时 Cookie/User-Agent 不作为持久凭据真相；凭据仍由手动配置或 CookieCloud 更新。种子下载继续走直接受控请求，不把下载字节交给浏览器服务。

快速识别使用 `POST /api/v1/discovery/pt-results/recognize`，请求体只含结果 token。服务通过只读 `resolveClaim` 取得 actor/site/title，不读取 torrent identity、不 reserve/消费 claim；共享 `mediarecognition` 先生成本地标题/年份/规格，再按可用性调用 TMDB。TMDB 海报经既有 Discovery 同源图片网关投影。只有权限或 claim 失效是 HTTP 错误，元数据不可用与 no-match 都是 200 的 `unrecognized` 安全摘要。

CookieCloud 同步只统计未知观察域名和受支持候选无有效登录 Cookie 的数量。已知域名集合来自已配置站点 host 与固定内建发现候选；响应、日志和审计只记录数字，不记录域名、Cookie 名称或值。

多站支持采用 MoviePilot 已验证的分层思路，但实现为 OhMyCine 自有目录：`pkg/site/builtin` 保存稳定站点键、显示名、解析引擎、规范 HTTPS 根地址与自动发现策略；标准站点复用 NexusPHP adapter，特殊站点未来只替换对应键的 adapter。数据库 `kind` 保存目录键并取消 PTTime 单值约束。CookieCloud 对每个目录项分别做同域 Cookie 合并、候选验证和加密创建，已配置判断同时使用目录键与规范 host，不能再按一个共享解析引擎阻止后续站点。

首批目录在 PTTime 之外加入 `sewerpt`（`https://sewerpt.com`）和 `panda`（`https://pandapt.net`）。共享 NexusPHP adapter 使用目录注入的最小 profile 管理健康、普通视频搜索和种子下载路径；健康检查必须看到 `logout.php` 或欢迎标识等已登录正向证明。解析器先按 torrent ID 聚合标题链接，再从其所有祖先 `tr` 中选择 direct `td` 最完整的种子行，解决 PandaPT 的 `table.torrentname` 嵌套标题既不能重复、又不能丢失外层大小/做种/下载/完成人数字段。发布时间优先读取 `span[title]`，免费促销兼容 `pro_free*` 类名；无 `title` 的链接回退可见文本。

BT 与 PT 共享 SiteService、限速器、结果 vault、快速识别和 DownloadService 桥接，但 adapter 和凭据契约独立。`pkg/site/btrss` 使用编译期 profile 定义公开 RSS 查询路径/参数/固定下载 host，只接受同源或 profile 明确允许的 torrent URL 以及规范 `magnet:?xt=urn:btih:*`。`pkg/site/torznab` 只向管理员配置且已验证的 HTTPS API 端点发送 API Key，不跟随跨源重定向。

Adapter 可选实现 `SourceResolver`，把服务端私有结果 identity 解析为 torrent bytes 或 magnet；旧 PT adapter 继续使用 `Download` 契约。SiteService 将两者都转换成既有 `DownloadSourceInput`，禁止在公开 DTO 增加下载 URL/magnet/API Key。通用 `/discovery/torrent-search*` 路由成为管理端主契约，旧 `/discovery/pt-search*` 保留为兼容别名。
