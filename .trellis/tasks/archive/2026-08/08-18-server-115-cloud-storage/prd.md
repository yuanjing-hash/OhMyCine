# Server 115 网盘连接与云存储驱动

## Goal

以“连接与存储目录分离”为基础，为 Server 接入首个 115 网盘云存储驱动。用户通过手动 Cookie 创建 115 Connection，并可在该连接下浏览云端目录、选择一个或多个 Storage 根、执行只读扫描和持续增量监听；同时为后续 STRM/signed 302、云端整理写入和 115 离线下载提供稳定的 provider 接口。

## Requirements

### 1. Connection 与凭据

1. 115 登录首先支持用户手动粘贴 Cookie；MVP 只解析并接受 `UID`、`CID`、`SEID` 和可选 `KID`，拒绝把未知 Cookie 字段带入请求。
2. Cookie 属于 Connection，不属于 Storage。一个 115 Connection 可以创建多个 Storage，每个 Storage 选择不同的 115 目录作为根。
3. Cookie 使用 Server 的凭据加密能力以 AES-GCM 密文保存；API、日志、审计、WebSocket 和普通配置导出均不得返回明文 Cookie。
4. 读取 Connection 时只返回 `credential_configured`、脱敏账号摘要、最后测试时间和健康状态。更新时采用“凭据留空则保留旧值、显式操作才清除”的语义。
5. “测试连接”至少完成登录态检查、用户信息读取和根目录有限列表，并提供可理解的失败原因；不得将上游响应中的敏感字段原样回传。

### 2. 云端目录与 Storage

1. 115 Storage 使用 provider directory ID 作为根身份，另存可读的显示路径；不得把 Cookie、pickcode 或临时 URL 写入 `root_path`。
2. 云目录选择器通过 Connection 浏览目录。服务端签发短期 opaque selection token，绑定 `connection_id`、provider item ID、用途和过期时间，不能复用本地绝对路径令牌。
3. 文件和目录以 115 `file_id` 作为稳定身份；路径、文件名和 parent ID 是可变属性。`pickcode` 仅可在服务端内部换取临时直链。
4. 首期至少支持目录列表、分页、文件属性读取和只读媒体树扫描；媒体库创建后的首次扫描与 supervisor 启动沿用现有自动流程。

### 3. 限速、恢复与监听

1. 每个 115 Connection 共享 provider limiter，并按目录读取、临时直链、变更操作划分 lane；媒体库设置的刮削速率不能绕过 provider 上限。
2. 默认采用保守速率；对 429、405 和疑似风控响应执行带抖动的指数退避，连续失败进入短期断路状态，并把健康状态暴露给管理员。
3. 生活事件使用持久化 `(update_time, event_id)` 游标，先把有限、脱敏的原始事件安全持久化，再推进游标。
4. provider 事件映射为统一的 `Created`、`Moved`、`Renamed`、`Deleted` 变更，并按 file ID 幂等处理。
5. 生活事件只用于低延迟增量和离线下载完成检查唤醒；周期增量、全量文件树 reconciliation 与离线任务低频状态查询继续负责补漏。每个媒体库的 watcher/supervisor 独立并发运行，不进入全局任务队列。

### 4. 后续能力边界

1. 115 Driver 必须声明临时直链、signed proxy、change cursor、原生离线下载和云端文件操作等 capability；只有已实现并通过 probe 的能力才可启用。
2. 本任务只提供 STRM/signed 302 所需的稳定文件身份和 `DirectURL` 接口，不在本任务重复实现已有的独立 STRM/302 投影任务。
3. 临时直链按 Connection、file ID 和请求 User-Agent 隔离缓存；获取链接与最终访问使用相同 User-Agent，缓存不超过上游真实过期时间且不落数据库。
4. 115 离线下载复用 Connection Cookie 和所选 115 Storage 根，以 `pan115_offline` provider 接入统一 Downloader、DownloadTask、Job、进度与取消流程；不得复制凭据或另建任务页面。云端整理写操作未开放前，任务完成后只做文件树复核和分类，不伪报跨 Storage 入库成功。
5. 云端上传、移动、复制、重命名、删除以及回收站语义是否进入本阶段，待产品范围确认。

## Acceptance Criteria

- [ ] 用户可以手动粘贴有效 115 Cookie，测试连接后看到脱敏账号和健康状态；无效/过期 Cookie 返回安全且可理解的错误。
- [ ] 数据库、常规 API 响应、日志、审计事件和 WebSocket 中均不出现明文 Cookie、pickcode 或上游临时 URL。
- [ ] 同一 115 Connection 可创建多个 Storage，并通过云目录选择器选择不同目录；保存后目录身份不依赖可变路径。
- [ ] 115 Storage 可以分页浏览、建立文件树，并由媒体库自动完成首次扫描。
- [x] 生活事件可持久化游标并幂等映射创建、移动、重命名和删除；断线重启后可恢复未处理事件，周期 reconciliation 可修复漏事件。
- [ ] provider 限速、退避和断路按 Connection 生效，多个媒体库不能叠加请求绕过上限。
- [ ] 115 Driver 的稳定文件身份和临时直链接口可供独立 STRM/signed 302 任务消费，前端永远不直接获得上游 URL。
- [ ] 使用 fake provider 完成服务层和 handler 自动化测试；真实账号测试由 Windows 隔离数据目录中的手动 smoke 脚本完成，测试输出不记录 Cookie。

## Notes

- 参考 MoviePilot 插件、OpenList 115 驱动和 `github.com/SheltonZhu/115driver` 的协议经验，但领域模型、接口、数据库和 UI 均按 OhMyCine 的 Connection → Storage → MediaLibrary 架构独立设计。
- 当前推荐首个可交付切片为：Cookie Connection、连接测试、云目录浏览、Storage 根选择、只读扫描、生活事件增量以及 DirectURL 接口。云端写操作随后单独开放，以降低私有 API、风控和误删除风险。
