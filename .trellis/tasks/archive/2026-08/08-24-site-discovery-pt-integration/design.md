# 技术设计

## 架构边界

```text
TMDB Provider ─┐
               ├─ DiscoveryService → 推荐/探索 API → Server Web UI
Douban Provider┘          │
                          └─ WorkIdentity → SiteSearchService
                                               │
                                    PTTime Adapter / Site Registry
                                               │
                                    opaque result token vault
                                               │
                                      existing DownloadService
                                               │
                                 classify → organize → import
```

- `pkg/discovery` 定义 provider DTO 和 provider 接口；`pkg/metadata/tmdb` 扩展趋势/Discover 能力；`pkg/discovery/douban` 独立实现公开页面解析。
- `pkg/site` 定义 PT adapter；`pkg/site/pttime` 封装 PTTime/NexusPHP 认证、搜索、解析与种子获取。
- `internal/services` 负责 provider 聚合、缓存、站点配置、健康、短期结果令牌和下载桥接；HTTP handler 只校验参数和输出 DTO。
- 推荐与 PT 结果不写入媒体库事实表；持久化缓存仅存凭据无关 DTO。站点凭据独立 AES-GCM purpose/AAD 加密。

## 数据模型

- `sites`：ID、name、kind=`pttime`、base_url、enabled、priority、timeout_seconds、rate_limit、user_agent、credential_ciphertext、credential_configured、health_status、health_error_code、last_checked_at、revision、timestamps。
- `discovery_cache`：provider、section、locale、page、payload_json、fresh_until、stale_until、updated_at，唯一键绑定 provider/section/locale/page。
- 搜索令牌不持久化：进程内有界 vault 保存 actor、site、torrent identity、标题、过期时间；重启后旧令牌明确过期。

## API 契约

- `GET /api/v1/discovery/recommendations?provider=&section=&page=`：栏目及 provider 状态。
- `POST /api/v1/discovery/recommendations/refresh`：刷新目标 provider/section。
- `GET /api/v1/discovery/search?q=&media_type=&year=`：公共元数据探索。
- `GET /api/v1/discovery/pt-search?q=&media_type=&year=&site_id=`：分组 JSON 降级结果。
- `GET /api/v1/discovery/pt-search/stream?...`：SSE 渐进事件；事件仅包含安全结果 DTO。
- `POST /api/v1/discovery/downloads`：提交 result token、downloader/profile/library/priority，Server 重新取种并调用现有 `DownloadService.Submit`。
- `GET/POST/PATCH/DELETE /api/v1/sites`、`POST /api/v1/sites/:id/test`：管理员站点管理。

所有接口使用标准 envelope；SSE 除外。推荐/搜索要求 `discovery.read`，站点管理要求 `system.admin`，创建下载额外要求 `downloads.create`。

## Provider 和身份

- `WorkSummary` 包含 provider、provider_id、media_type、title、original_title、year、overview、rating、vote_count、poster/backdrop identity、tmdb_id/douban_id。
- 只有相同 TMDB ID 或显式 provider 外部映射时跨源合并；标题/年份相同仅用于 UI 提示，不自动合并。
- TMDB 图片由受控 URL 构造；豆瓣图片由 Server 图片代理/缓存或固定公开 HTTPS 域校验后输出，避免任意 URL 透传。

## 缓存与故障隔离

- 新鲜 TTL 24 小时，旧快照 7 天；用户刷新只失效目标 provider/section/page。
- provider 各自 10 秒超时、响应上限和有界并发；错误转换为稳定 code，不返回上游正文。
- 单 provider 失败返回旧快照并标记 `stale=true`；无快照则只返回该栏目错误。

## PTTime 与安全下载

- 管理员保存候选 Cookie 前先测试；测试成功后 CAS 替换密文，失败保留旧凭据。
- adapter 固定 HTTP(S) scheme、同源重定向、响应大小和路径白名单；不接受普通用户自定义搜索/下载 URL。
- 搜索页面解析畸形条目时跳过并计数；下载时以站点 ID + torrent ID 重建同源请求，校验 bencoded torrent 大小和 Content-Type/文件头。
- 搜索令牌随机 256 bit、默认 15 分钟、绑定 actor/site/result；普通日志只记录 token digest 前缀和站点 ID。

## Web UI

- 推荐页：Hero、来源切换、真实栏目横向海报、更新时间/缓存标记、来源级刷新和局部错误。
- 探索页：元数据搜索、作品详情抽屉、PT 搜索筛选、按站点分组的渐进结果、下载参数弹层。
- 站点页：卡片列表；新增/编辑在弹层；卡片展示健康、优先级、限速和最近测试，不平铺 Cookie。
- 复用全局 Toast、亮/暗主题、管理端表单与卡片 token；窄屏下表格转卡片。

## 兼容、上线与回滚

- 数据库迁移仅新增表，不重写已有下载/媒体记录。
- 未配置 TMDB 或站点时页面显示可操作空状态；现有下载管理不受影响。
- 回滚可隐藏三条导航和新路由并停止服务；新增表可保留，密文不会被旧版本读取或回显。
