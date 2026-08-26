# Implementation Plan

1. [x] 增加 credential key loader/AES-GCM envelope、Downloader/DownloadTask 模型、显式迁移、RBAC catalog 和数据库升级测试。
2. [x] 定义 `pkg/downloader` adapter/capability/telemetry，完成 fake adapter 与 qBittorrent 受控 HTTP client 测试。
3. [x] 实现 DownloaderService：CRUD、provider 配置约束、加密凭据、健康测试、审计脱敏和活跃引用保护。
4. [x] 实现 DownloadService/worker：加密提交源、原子创建事实与 Job、provider submit/reconcile、telemetry persistence、重启恢复及 provider 控制。
5. [x] 增加薄 handler、受保护 routes、owner/all 过滤和 HTTP 集成测试；仓库当前不存在 OpenAPI 文件。
6. [x] 实现下载管理 Web UI、真实导航与权限生成，覆盖 CRUD、测试、磁力/URL/种子提交、状态/未知 telemetry 和白色/深色主题语义样式。
7. [x] 更新 Server 架构/roadmap 与 backend spec，运行 Go/Web UI/embedded/Windows `server/test.ps1` 全量验证。
8. [x] 增加统一下载暂存设置的 migration/model/service/API，复用目录选择 token 与 Storage 边界校验，并让下载 worker 只从该设置解析 save path。
9. [x] 将系统设置页接入统一暂存目录；移除下载器表单的 Storage 选择，下载器列表改为实时状态卡片与按需编辑表单。
10. [x] 增加 App 级自动消失悬浮通知，细化 qBittorrent 连接测试安全错误文案，补齐 Go/Web 测试与 Windows 全量验证。
11. [x] 修复 qBittorrent 新旧 add/login 响应兼容与提交幂等性，增加 metadata/file/category adapter contract 和 v4/v5 回归测试。
12. [x] 增加加密 TMDB 设置、受控 client、连接测试、文件名/季集解析复用、Profile snapshot 与轻量/完成后刮削服务。
13. [x] 扩展 DownloadTask migration/DTO/worker 状态机：metadata probe、preclassify、provider category、自动未识别 fallback、resume、重启对账和完成复核。
14. [x] 更新系统设置与下载页面：TMDB 凭据、Profile 选择、分类/刮削阶段与安全结果展示；补齐 API/RBAC/Web UI 测试。
15. [x] 更新 downloader backend spec、Server 架构/roadmap，运行 Go/Web UI/embedded/Windows 全量验证；真实 qBittorrent v5.2.3 live smoke 留给 owner 手工验收，自动测试不操作现有任务。
16. [x] 将非运行态 qBittorrent 暂停/取消改为可重启恢复的 queued provider-control intent；Scheduler claim 后先执行 provider 控制，成功才确认状态，失败清除 intent 并继续任务，同时关闭遗留 ActionRequest。
17. [x] 对齐 Player TMDB 通道：增加构建/部署/用户凭据优先级与安全状态，默认短域名网络故障回退，自定义 API/图片 route 独立测试后保存，更新 Windows/Linux 构建脚本、Server CI、设置 UI、迁移、测试和安全文档。
18. [x] 对齐 Player TMDB 双认证：显式支持 v4 Read Access Token/Bearer 与 v3 API Key/query，增加 credential kind、v11 兼容迁移、deployment/build 双变量、设置 UI 与泄露回归测试。
19. [x] 收口破坏性取消与终态清理：取消使用 `deleteData=true`，provider 成功/task-not-found 后自动删除本地 DownloadTask/Job；failed/cancelled 增加受 RBAC、确认和审计保护的删除 API/UI，provider 失败时保留记录。
20. [x] 增加 v13 兼容迁移，自动关闭旧版下载分类确认并重新入队；下载页不再把遗留状态呈现为需要人工确认。
21. [x] 修正下载暂存目录可见性：受保护设置 API 和页面显示当前绝对路径，目录选择器从 Server 已保存路径重新打开；写入仍只接受短期目录令牌，运行日志、审计与任务 DTO 继续隐藏路径。
22. [x] 将 115 原生离线下载器的根目录下拉框替换为共享 Storage 范围目录树；新增稳定 provider directory ID/path、v22 迁移、创建/编辑/回显和提交绑定，并覆盖越界令牌与根目录兼容回填测试。

## Rollback Points

- Migration 仅新增表/索引；回滚代码时保留未知表，不执行自动 drop。
- downloader 删除不级联 DownloadTask；实现发现不安全时可禁用 worker/页面而不删除持久事实。
- provider 控制失败不将任务伪装成 paused/cancelled，也不删除 provider 数据。
