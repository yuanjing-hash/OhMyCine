# 技术设计

本子任务实现内建 PT adapter、PTTime NexusPHP 兼容实现、加密站点配置、健康测试、聚合搜索、SSE、短期不透明结果令牌和现有下载服务桥接。浏览器、任务普通载荷、日志和审计永不出现 Cookie、passkey 或真实种子 URL。

站点每个独立超时/限速/并发；多站并发、站内顺序翻页。候选凭据测试成功后 CAS 替换；下载时根据令牌重新解析并获取种子，再调用现有 `DownloadService.Submit`。

推荐 DTO 中的上游图片 URL 在服务返回前转换为 provider 绑定的 Base64URL 身份，浏览器只请求同源图片端点。端点重新校验 TMDB 当前图片前缀或豆瓣固定域名、HTTPS、重定向、Content-Type 与响应大小。作品详情通过 TMDB ID 重新获取完整快照；豆瓣条目先从缓存恢复标题/年份，再由 Server 映射到 TMDB，失败时仍返回有限的豆瓣快照。

CookieCloud 使用单例设置与专用 AES-GCM purpose 保存 Server URL、UUID、端到端密码和本地上传共享密钥。本地接收端仅保存 CookieCloud 原始密文；同步时在内存中解密，按各站点 Base URL 的域名选择 Cookie，调用站点 adapter 测试后以 revision CAS 更新该站点 Cookie，失败不覆盖旧值。远程访问限制 HTTP(S) scheme、响应大小、重定向和超时；本地模式要求共享认证头。

浏览器仿真是站点请求策略而非前端代填密码：启用后 PT adapter 把当前候选 Cookie 交给管理员配置的 FlareSolverr 兼容 `/v1` 接口并获取渲染后的页面，再按原站点契约解析。FlareSolverr 返回的临时 Cookie/User-Agent 不作为持久凭据真相；凭据仍由手动配置或 CookieCloud 更新。种子下载继续走直接受控请求，不把下载字节交给浏览器服务。
