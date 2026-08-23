# 插件平台与 Bilibili 在线媒体源实施计划

## 1. 任务拆分

该父任务负责完整需求、共享协议和最终集成审查。实施时拆为以下可独立验证的子任务：

1. `plugin-contract-runtime`
   - Manifest/DTO Schema、WASM Host、权限、生命周期、SDK 最小核心。
2. `server-plugin-management`
   - 数据模型、迁移、插件安装升级回滚、连接/凭据、管理 API 与 Web UI。
3. `online-library-player-api`
   - 在线媒体库、导航、Feed、主页贡献、刷新会话和 Player Bearer API。
4. `media-version-quality-contract`
   - Work/Segment/Version/Variant、剧集版本分组、结构化版本字段和安全播放方案。
5. `player-site-experience`
   - Player 原生站点页、首页聚合配置、来源隔离、层级选集/版本/清晰度菜单。
6. `bilibili-plugin`
   - 在官方插件库中使用公开 SDK 实现登录、浏览、推荐刷新、专区、个人内容、搜索、详情、播放、字幕和弹幕。
7. `bilibili-download`
   - DownloadPlan、媒体工具、DASH 下载/合流、任务阶段和现有入库流水线对接。
8. `hub-plugin-ecosystem`
   - Hub Registry、开发规范、SDK 文档、模板、校验与插件详情页。
9. `plugin-integration-hardening`
   - 跨层契约、故障注入、安全检查、兼容矩阵和最终文档。

平台设计还覆盖元数据、通知、下载器、云盘/存储、媒体服务器、事件/调度、识别分类命名和声明式 UI 等后续 capability；本任务只冻结通用边界并用 Bilibili 驱动首批 Host API，不把后续领域误实现为站点专用接口。

依赖顺序：1 → 2/3/4 → 5/6 → 7/8 → 9。能并行的子任务必须共享已冻结的 Schema 版本，不复制 DTO。

## 2. 阶段一：冻结协议与测试夹具

- [x] 定义 Manifest v1 JSON Schema 和权限枚举。
- [x] 定义 GitHub 插件仓库 Registry v1 Schema、仓库 URL 规范、固定提交获取流程和跨仓库冲突规则。
- [x] 定义插件生命周期、错误码、健康和升级状态机。
- [x] 定义 OnlineLibrary、Navigation、Feed、MediaWork/Segment/Version/Variant DTO。
- [x] 定义 PlaybackPlan、DownloadPlan、Host HTTP/credential/storage/logging API。
- [x] 编写一个无 Host import、导出 v1 ABI 的真实 WASM 安装生命周期 fixture；它只作为自动化测试资产，不发布为用户可见固定内容插件。
- [x] 建立跨 Go/TypeScript/SDK 的契约夹具，防止字段漂移。
- [x] 更新安全和架构文档，明确 PT 不属于插件能力。

验证：Schema 正反例、版本兼容、未知字段/能力、载荷大小、超时和畸形返回测试。

## 3. 阶段二：Server 插件核心与管理端

- [x] 增加插件包、安装、运行代次、权限授权、连接、在线库和私有状态模型及显式迁移。
- [x] 实现隔离插件目录、旁路解包、路径穿越防护、摘要校验和原子切换；可信签名与 trust store 后续补齐。
- [x] 集成 WASM Host，限制内存、调用时间、并发和 Host capability。
- [x] 实现受控 HTTP、凭据句柄、私有 KV、结构化日志与审计。
- [x] 实现安装、启停、升级权限差异、失败回滚和卸载服务/API。
- [x] 增加插件 RBAC 权限目录。
- [x] 完成 Server 插件管理页：卡片、健康、版本、权限、连接、日志入口和操作确认。
- [x] 增加“已安装 / 插件市场 / 仓库设置”分页；仓库设置支持添加、停用、排序、删除和手动刷新 GitHub 仓库地址，市场页只展示通过 Registry/Manifest 校验的插件。
  - [x] 已完成仓库 CRUD、固定提交 Registry 缓存、真实市场发现、Manifest/包/解包树完整性校验、权限二次确认、WASM 安装启停、升级失败补偿、回滚和卸载；受控 Host API 与在线媒体库仍按后续阶段实施。

验证：Go 单元/集成测试、恶意 zip、越权域名、凭据隔离、插件 panic/trap、超时、回滚和并发启停。

## 4. 阶段三：在线媒体库与 Player API

- [x] 实现插件连接发布在线媒体库和动态导航。
- [x] 实现声明式 Feed、cursor、refresh session、缓存和限速。
- [x] 扩展 `/api/v1/player/*`，只返回安全 opaque ID 和 DTO。
- [x] 实现 HomeContribution 候选和来源级错误。
- [x] 实现站点动作权限校验和幂等请求标识。
- [x] 实现在线历史分页、播放进度事件回传与来源级失败隔离。
- [x] 确保插件连接停用后 Player 返回明确的来源不可用状态。

验证：Bearer 权限、分页游标、刷新会话隔离、单插件失败不影响其它库、DTO 脱敏和兼容测试。

## 5. 阶段四：通用多版本与播放方案

- [x] 扩展 Server 媒体条目版本描述，并从现有 release version parser 保守回填。
- [x] 将剧集响应整理成 Work → Segment → Version，修复同一集多个版本显示为重复集数的问题。
- [x] 扩展 Player 类型和运行时校验，兼容旧 Server。
- [x] 扩展 MediaStreamRequest/PlaybackPlan 支持 DASH、variants、期限和刷新选择令牌。
- [x] 统一电影、剧集、Bilibili 分 P 的内容版本模型，以及与版本分离的 StreamVariant 清晰度模型。
- [x] 抽取并复用现有播放队列/选集菜单，只负责集数/分 P 和媒体版本选择。
- [x] 在 PlayerControls 增加独立清晰度按钮，只切换当前版本的 StreamVariant；少于两个可用档位时隐藏。
- [x] 实现播放中无损切换：先解析、成功替换、恢复进度/状态、失败回退。
- [x] 将播放进度、版本偏好和字幕/音轨偏好分别绑定正确身份层级。
- [x] 验证多版本入库、冲突、NFO 和 STRM 不互相覆盖。

验证：电影多剪辑、剧集单集多版本、跨来源版本、Bilibili 多分 P/多清晰度、失效 variant、切换失败回退。

## 6. 阶段五：Player 站点体验与主页聚合

- [x] 扩展 ServerDataSource 动态子来源/在线库支持。
- [x] 实现声明式站点主页、导航、栏目、搜索和详情渲染。
- [x] 实现来源徽标、站点动作和不支持能力的隐藏策略。
- [x] 实现设备侧主页来源/Feed 启用、顺序和位置配置。
- [x] 顶部 Hero 混合可信来源，下方按来源分栏。
- [x] 实现全局刷新和单栏目刷新，保持旧推荐会话滚动状态。
- [x] 保持 Player 未连接 Server 时的独立可用性。

验证：现有首页聚合与搜索脚本、新增插件来源故障隔离脚本、桌面/Android 响应式与键盘/遥控交互检查。

## 7. 阶段六：Bilibili 浏览与播放插件

- [x] 在独立官方插件库/插件包中创建 Bilibili 插件，使用公开 SDK、Manifest、权限和 Host API；添加核心代码无 Bilibili 专用分支的验证。
- [x] 实现扫码登录、二维码过期、轮询限速、会话加密保存和重新认证。
- [x] 实现主页推荐刷新、热门、排行、专区和分页。
- [x] 实现搜索、详情、UP 主、合集、分 P/剧集。
- [x] 实现收藏、稍后再看、历史、关注与追更读取。
- [x] 按能力逐项实现远端写操作并提供明确确认/错误。
- [x] 实现账号权限内的播放清晰度、DASH、字幕和弹幕解析。
- [x] 实现 Bilibili 历史分页与播放进度回传；Player 插件弹幕轨道优先、旧渠道回退。
- [x] 通过 Host HTTP 访问白名单域名并补齐插件模块日志。

验证：插件独立安装/停用/升级/卸载、匿名/登录态、二维码过期、Cookie 失效、限流、分页重复、地区/付费不可用、播放 URL 过期和独立清晰度按钮切换。

## 8. 阶段七：Bilibili 真实下载

- [x] 定义并实现宿主 DownloadPlan 执行器，不允许插件提供任意命令或路径。
- [x] 增加 FFmpeg MediaTool 抽象、版本探测、固定参数和安全日志。
- [x] 增加 Windows 隔离工具安装脚本，固定版本、SHA-256、许可证并 gitignore 工具目录。
- [x] 下载视频、音频、字幕/弹幕到任务专属暂存目录。
- [x] 持久化解析、视频下载、音频下载、合流、校验和入库阶段。
- [x] 实现暂停/取消/重试及任务受管临时产物清理。
- [x] 合流完成后生成标准 manifest，进入现有识别、整理、转移和历史链路。
- [x] 保留清晰度/版本后缀，验证多版本不被冲突策略误覆盖。

验证：单文件、DASH 双轨、多分 P、断点重试、URL 过期重解析、取消清理、FFmpeg 缺失/失败、磁盘不足、入库失败重试。

## 9. 阶段八：Hub、SDK 与发布规范

- [x] 替换旧 Go plugin 和任意 UI 注入文档。
- [x] 发布 Manifest/DTO Schema、SDK、fixture、模板插件和 mock host。
- [ ] 实现插件 lint、pack、checksum/sign 和兼容性测试命令。
  - [x] 已提供确定性 `.omcp`/独立 Manifest 打包、SHA-256 写入、Schema 校验和可安装 fixture；可信签名生成与 trust store 后续补齐。
- [x] 更新 Hub 插件详情、权限、兼容矩阵、校验状态和升级差异展示。
- [x] 建立 Registry Schema 校验和官方 Bilibili 插件条目；独立 CI 工作流后续补齐。
- [ ] 记录第三方插件提交、审查、撤回和安全公告流程。

验证：保留最小自动化 fixture 验证 ABI 与生命周期；不再制作用户可见固定内容站点。Bilibili 必须完全通过公开协议运行，未来第二个真实插件无需修改 Player/Server 专用代码即可组合其它 capability。

## 10. 阶段九：集成与质量门

- [x] 数据库迁移、降级兼容和旧 DTO 回归。
- [x] 插件安装/升级/回滚故障注入（当前 WASM 安装生命周期范围）。
- [ ] SSRF、路径穿越、Zip Slip、凭据泄露、日志脱敏和权限升级测试。
  - [x] 已覆盖 GitHub 固定来源、受控 Release CDN、Zip Slip/链接/Reparse Point/保留名/大小、解包树篡改和权限指纹；Host HTTP/凭据能力开放后继续补齐对应测试。
- [x] Server/Player/Hub 构建与类型检查。
- [ ] Windows Server + Player 真实 Bilibili 登录、浏览、播放、清晰度切换和下载入库验收。
- [x] Player 本地历史多页、Bilibili 历史翻页、远端进度同步失败隔离与插件弹幕回退自动化验收。
- [x] 更新当前插件发现、安装生命周期、SDK 打包、Hub 安装和安全设计文档；在线库/Bilibili 阶段继续同步后续契约。
- [x] 最终审查 PT 内建边界未被破坏，现有本地/115/Emby 数据源无回归。

## 11. 验证命令

```powershell
cd server
go test ./...
golangci-lint run

cd ..\player
npm run verify:server-datasource
npm run verify:home-source-fault-isolation
npm run verify:home-aggregate-search
npm run typecheck
npm run lint
npm run build
cd src-tauri
cargo test
cargo clippy -- -D warnings

cd ..\..\hub
npm run build
```

实现时为插件协议、层级媒体选择和 Bilibili 流程增加专用验证脚本；不能仅用编译通过代替 Windows 真实播放与下载验收。

## 12. 风险文件与回滚点

- `player/src/services/datasource/types.ts`、`server.ts`、首页聚合和播放上下文是跨层契约高风险区；每次 Schema 变化必须同步 fixture。
- Server `player_media.go` 和媒体目录 `work_key` 分组影响现有本地/115 播放；先兼容扩展，再启用新分组响应。
- 播放器控制菜单改动不得破坏现有字幕、音轨、弹幕、播放队列和移动端布局。
- 插件数据库迁移只增加新表/字段，禁止迁移时删除或移动用户媒体。
- FFmpeg 工具和插件包都安装到精确隔离目录；失败回滚只移除本次受管目录，不碰用户数据。
