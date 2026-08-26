# 统一媒体库扫描识别与目录对账实施计划

## 1. Built-in recognition word packs

- [x] 按固定 commit 引入 TV/anime 原文、MIT LICENSE、sources manifest 和 SHA-256 校验。
- [x] 实现四种 MoviePilot custom words 语法、捕获替换、EP 安全算术、直接 TMDB hint 与受限兼容正则执行器。
- [x] 全量解析/编译 322 条有效内置规则，覆盖 lookaround、backreference、offset、direct ID、超时和执行顺序。
- [x] Profile 增加规范 pack code 配置、默认启用、复制/更新/严格校验及只读 UI 开关；用户规则在 pack 之后执行。

## 2. Shared recognizer extraction

- [x] 定义 provider-neutral recognition package/result 和可注入 metadata provider 接口。
- [x] 把 Profile 预处理、候选生成、TMDB 匹配、置信度与分类从 `DownloadWorker` 提取到公共服务。
- [x] 让 qBittorrent/115 下载完成继续使用快照调用公共入口，保持可信主媒体筛选和现有错误码。
- [x] 用完整七武士发行名、BDMV、电影包和剧集包夹具证明下载与扫描输出一致。

## 3. Scan facts and grouping

- [x] 将 `ScanLocal`、`ScanProvider`、`InspectLocalFile` 改为只产出文件事实与结构提示，不生成最终 TMDB/分类投影。
- [x] 实现电影、剧集、季目录、根级独立视频和光盘结构的 deterministic recognition-unit grouping。
- [x] 增加扫描轮内分组去重、文件指纹和跨轮缓存键。
- [x] 覆盖 115 bulk tree、顺序 BFS、本地全量和本地 watcher 的相同分组结果。

## 4. v25 persistence and cache

- [x] 增加 recognition、cache 表和 entry/scan-run 加法字段、索引与外键。
- [x] 实现 fresh、v24 upgrade、重复迁移及 source replacement cascade 测试。
- [x] 实现正向/短期失败缓存、过期和 Profile/language/region fingerprint 失效。
- [x] 保证缓存、API、日志、audit 不出现 provider ID、绝对路径、凭据或原始响应。

## 5. Reconciliation integration

- [x] 在 MediaLibrary supervisor 中注入 metadata settings/public recognizer 和进程级共享 limiter。
- [x] 枚举后、事务外批量识别，提交时校验 generation/source identity，避免陈旧结果覆盖新配置。
- [x] 完整扫描删除已证明消失的数据，partial 保留；provider stable ID 移动/改名更新而不重复。
- [ ] 本地事件和 115 事件仅枚举/重算受影响单元（当前事件已独立合并唤醒统一只读 reconciliation，指纹/缓存避免未变化单元重复 TMDB；affected-unit 枚举仍待后续优化），定时增量/全量继续补漏。
- [x] Profile revision 更新重新自动识别/分类，同时保留人工 override。

## 6. Manual correction API

- [x] 增加 recognition 分页、单项 retry、TMDB candidate search、override 和 clear override service/API。
- [x] 扩展 TMDB client 的受限候选搜索与按 ID 详情读取；服务端验证客户端选择。
- [x] 增加 RBAC、strict JSON、opaque token、no-store、安全错误和 audit 覆盖。
- [x] 验证无凭据、认证、网络、no-match、低置信度与并发配置变化行为。

## 7. Web UI and observability

- [x] 媒体清单增加全部/已识别/未识别筛选和安全匹配摘要。
- [x] 增加未识别重试、TMDB 搜索确认、人工覆盖标记与清除操作，使用全局 Toast。
- [x] 扫描记录显示识别统计，保持数据库分页、响应式和浅色/深色主题。
- [x] 增加 `media_recognition` operation、HTTP route mapping、批次 start/terminal 日志及脱敏测试。

## 8. Compatibility, specs and verification

- [x] 回归下载完成、Transfer、115 life event、本地 watcher、目录更换和 catalog 分页。
- [x] 更新 backend specs、Server architecture/roadmap、API/Web UI contracts。
- [x] 运行 focused Go/Web tests、全量 Go test/vet/build/module verify 和 Web UI permission/test/typecheck/lint/build。
- [x] 运行 `server/test.ps1`、`git diff --check`，确认无真实来源写入和遗留 Server 进程。

Validation gate:

```powershell
cd server
go test ./internal/medialibrary ./internal/services ./internal/httpserver ./pkg/metadata/tmdb
go test ./...
go vet ./...
go build ./cmd/server
go build -tags webui ./cmd/server
go mod verify
cd webui
npm run permissions:check
npm test -- --run
npm run typecheck
npm run lint
npm run build
cd ../..
git diff --check
cd server
.\test.ps1
```

## Risky files and rollback points

- `server/internal/services/download.go`: 提取 recognizer 时必须保持下载快照、完成清单筛选、错误码和 retry 清理行为。
- `server/internal/services/media_library.go`: 不得在 DB transaction 中调用 TMDB；提交前必须验证 source/generation，partial 不能删除未见条目。
- `server/internal/database/migrations.go`: v25 只能加表、列和索引，不重写或删除 v24 条目。
- `server/internal/medialibrary/scan.go`: provider/local 事实必须一致，不能让 provider adapter 获得 Profile/TMDB 依赖。
- `server/webui/src/views/MediaLibrariesView.vue`: 保持现有分页与详情请求取消，避免识别弹层阻塞页面或制造陈旧响应。
