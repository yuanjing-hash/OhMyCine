# Player 下载管理与完整离线播放 — 实施计划

## 1. 准备与基线

- [x] 读取前端、Rust/Tauri、Server Player API和安全规范；记录当前下载schema与命令基线。
- [x] 运行现有 `verify:download-planning`、`verify:android-downloads`、`verify:server-datasource`、`verify:danmaku`、TypeScript和Rust基线检查。
- [ ] 为下载数据流添加测试fixture：稳定descriptor、短期302、Emby重定向、Server本地Range和过期重解析。

## 2. 数据契约与SQLite迁移

- [x] 重构下载DTO为稳定descriptor，加入variant、速度、ETA、分段和附件状态；确保DTO无URL/Header/credential字段。
- [x] 增加下载设置命令与安全范围：默认目录、同时任务数、单任务分段数、全局限速。
- [x] 增加 `download_segments`、cleanup事实和任务schema迁移；迁移旧cancelled/queued/running/paused/completed记录。
- [x] 建立 `offline_media.sqlite`、版本化详情快照、offline item和asset表。
- [x] 补齐schema敏感字段、迁移幂等和旧任务兼容测试。

## 3. 调度器、分段与取消

- [x] 从当前即时 `start_task` 重构为常驻scheduler和公平队列。
- [x] 实现并发task semaphore、Range探测、桌面分段随机写、区间checkpoint和最终覆盖校验。
- [x] 实现全局token bucket、滚动速度和ETA事件。
- [x] 实现pause/resume/restart recovery；用户暂停与进程中断状态分离。
- [x] 实现cancel intent、worker收敛、partial/task删除、removed事件和内部cleanup重试；取消任务永不进入retry UI。
- [x] Android复用调度与限速，保留安全单流SAF写入和前台通知。

## 4. Provider resolver与临时地址刷新

- [x] 将现有local、Alist、CloudDrive2、WebDAV、123、夸克接入统一稳定身份解析边界。
- [x] Emby/Jellyfin resolver按item/media source重新请求PlaybackInfo或静态流入口，支持短期302刷新和凭据错误分类。
- [x] Server物理媒体resolver读取安全credential并重复请求受保护entry stream，覆盖Server本地文件与115重定向。
- [x] Server在线插件resolver使用稳定library/work/segment/version/variant身份并只复用现有API；现有API不足时在Player显示能力不可用并记录后续Server契约，不修改 `server/`。
- [x] 实现有界自动恢复、跨源Header清空、HTTPS降级拒绝和实体身份续传检查。
- [ ] 增加真实本地HTTP端到端测试，不只做静态源码断言。

## 5. 下载规划与媒体操作

- [x] 修正现有下载规划：一个单集默认只选当前版本/清晰度，不自动下载全部版本。
- [x] 为电影、单集、季、整剧提供版本/清晰度确认模型和预计大小摘要。
- [x] 扩展media action支持Server物理媒体；区分Player离线下载与Server下载入库；Server在线插件在缺少安全离线流契约时保持禁用。
- [x] 验证桌面右键、触摸长按和当前播放项动作均走同一adapter。

## 6. 离线包与离线DataSource

- [x] 视频原子完成后事务建立offline item与作品层级。
- [x] 生成有界详情快照，将海报/背景/still存入持久离线资产目录。
- [x] 下载外置字幕并保存可安全获取的Provider弹幕；内嵌字幕继续由离线视频承载。
- [x] 附件失败独立标记和重试，不影响视频ready状态。
- [x] 实现OfflineDataSource的list/search/detail/stream和只读能力。
- [x] 实现包引用计数与安全删除，避免删除共享作品资产或下载root外文件。

## 7. 本地优先播放与离线回退

- [x] 建立统一playback resolver，在远程DataSource前检查精确offline版本。
- [x] Rust校验本地视频存在、大小/指纹和root边界后创建进程内local locator。
- [x] 本地文件缺失时修正offline状态并在线回退；不存在网络时显示明确离线文件缺失错误。
- [x] 详情加载失败时回退offline snapshot；冷启动OfflineDataSource可独立导航。
- [x] 离线播放加载字幕/弹幕，并保持原作品播放历史、偏好和完成状态identity。

## 8. 下载中心、设置与徽标

- [x] 新建Pinia download/offline stores，集中加载任务、设置和离线摘要并订阅原生事件。
- [x] 新建 `/downloads` 路由和DownloadsView：进行中/已完成/失败/设置。
- [x] 将下载目录从SettingsView迁移到下载中心设置，增加并发、线程和限速控件及说明。
- [x] FloatingControls增加下载入口/徽标，Mobile Quick增加下载管理入口，删除App级独立圆钮。
- [x] 任务卡加入速度、ETA、大小、分段、附件和聚合状态；实现pause/resume/cancel/retry/open/delete。
- [x] MediaCard、Home、Search和Detail接入批量offline索引，左上角显示已下载/部分下载标识且不影响右下角已播放状态。

## 9. 文档、专项验证与质量门

- [x] 更新Player架构、安全设计、路线图和DataSource/下载契约文档。
- [x] 新增或更新 `verify:download-planning`、`verify:android-downloads`、`verify:server-datasource`、`verify:danmaku`及下载中心UI脚本。
- [x] 运行：`npm run typecheck`、`npm run lint`、`npm run build`。
- [x] 运行全部相关 `npm run verify:*` 下载/Server/媒体操作/安全路由/弹幕回归。
- [x] 运行：`cargo fmt --check`、`cargo check`、`cargo clippy --all-targets --all-features -- -D warnings`、`cargo test`。
- [x] 运行Android ARM64 Rust目标编译并组装universal debug APK，验证Android专用绑定与异步`Send`边界。
- [x] 检查 `git diff -- server` 为空，确认未与并行Server任务交叉；Server行为只通过现有API fixture验证。
- [ ] Windows原生手测：并发/限速、取消消失、Server本地、Server 115地址失效、Emby地址失效、断网冷启动、徽标与本地优先播放。
- [ ] Android手测：SAF目录、前台通知、暂停/取消、重启续传、离线详情、字幕/弹幕和本地播放。

## 10. 高风险文件与回滚点

- `player/src-tauri/src/commands/downloads.rs`：先保留旧命令名兼容层，模块拆分后再删除内部旧路径。
- `player/src-tauri/src/commands/provider_file.rs`：resolver迁移不能影响现有删除/播放功能。
- `player/src/services/datasource/types.ts`：新能力必须可选，不能要求所有DataSource同时实现。
- `player/src/views/PlayerView.vue`：本地优先层不得把URL/路径写入route，也不能破坏当前播放进度同步。
- `player/src/views/SettingsView.vue`：只迁移下载设置，不改其它Player设置。
- `server/`：由并行任务独占，本任务禁止修改；只读取现有Player API契约并在Player侧适配。
- SQLite迁移必须幂等且只增量扩展；发生失败时保留旧数据库和旧下载文件，不执行破坏性清理。

## 11. 本轮实现记录（2026-08-26）

已完成下载中心、统一调度、暂停/继续/取消/失败重试、稳定身份重新解析、Server物理媒体下载、本地优先播放、离线摘要索引、下载徽标及设置迁移。取消任务会立即从用户队列删除，清理失败只进入内部清理队列；短期地址恢复耗尽后才进入失败状态。

本轮暂未完成：桌面真实Range多分段与严格全局token bucket、完整详情/图片/字幕/弹幕离线包、附件独立重试、可冷启动导航的OfflineDataSource、Android完成文件解析与删除、真实本地HTTP地址失效端到端测试、Windows/Android人工运行验证。Server在线插件缺少用途受限的稳定离线文件流接口时保持明确禁用，本任务未修改Server。

自动验证已通过：Player TypeScript类型检查、ESLint、生产构建、下载规划/Android下载/Server数据源/Server在线库/媒体操作/安全播放路由/弹幕专项脚本，以及Rust fmt、check、97项测试和clippy严格检查。

## 12. Trellis质量复核（2026-08-26）

复核直接修复了以下数据一致性与取消语义问题：

- 完成任务不再使用入队时的旧内存副本写 `offline_items.video_bytes`；先从SQLite读取最终进度，且未知总大小会在完成事务中以 `bytes_downloaded` 回填。回归测试覆盖“仅删除历史任务记录后，离线索引仍独立保留且大小正确”。
- 删除失败任务记录会同步精确清理其 `.partial`、segment与任务事实，避免失去稳定任务身份后遗留不可管理的断点文件。
- 修复最终原子重命名附近的取消竞态：取消优先于完成状态，已由该任务生成的最终文件会按受控root和单文件名精确清理；暂时清理失败进入内部cleanup，不作为可重试下载展示。
- 下载Pinia初始化改为故障隔离；任务、离线索引、设置或默认目录中的单项读取失败，不再阻止进度/移除事件监听挂载。

最终自动门禁：TypeScript类型检查、ESLint、生产构建、下载规划/Android下载/Server数据源/Server在线库/媒体操作/安全播放路由/弹幕/移动端专项脚本通过；Rust fmt、check、102项测试及全target/all-feature clippy严格检查通过。`verify:view-architecture` 仍因任务开始前已存在的 `SettingsView.vue` 超过4000行失败，本任务只从该文件删除下载目录区块，没有增加其行数。

仍需后续实施的PRD能力保持不变：真实Range多分段、严格全局token bucket、完整详情/图片/字幕/弹幕离线包与附件独立重试、冷启动OfflineDataSource、Android离线文件解析/删除、Server在线插件用途受限离线流契约及Windows/Android真实运行验证。

## 13. Player续作复核（2026-08-26）

上一节的“仍需后续实施”状态已经过时。本轮按实际代码和自动门禁重新核对后确认：

- 桌面真实Range多分段、实体一致性验证、分段checkpoint、最终覆盖/大小校验和不支持Range时的安全单流退化已经实现。
- 全任务/全分段共享的聚合速率调度已经实现，并覆盖运行时修改限速的单元测试。
- `OfflineDataSource`、有界详情快照、远程详情失败回退、本地文件大小/mtime指纹校验、本地优先播放和Android SAF完成文件解析/删除已经实现。
- 详情页当前选择的媒体版本现在会通过稳定`mediaSourceId`进入下载规划和任务；版本已经消失时明确失败，不再静默回退到另一个版本。普通聚合下载仍对每一集选择Provider主版本，避免隐式下载全部版本。
- 修复离线虚拟数据源路由身份：离线列表/详情现在由`__offline__`拥有导航和播放路由，原来源稳定身份只保留在离线索引和`exactIdentity`中。冷启动、断网或远程详情失败后不再因为媒体项携带原`sourceId`而重新跳回不可用来源。
- 取消仍保持“立即从用户队列消失并删除精确拥有的临时文件”；只有真实失败进入失败分页和重试语义。

当前仍未完成的范围：

- 海报/背景/still的持久离线资产晋升、外置字幕与弹幕持久化，以及附件独立重试命令/UI；当前视频与详情可离线，但`attachment_state`会保持待补全。
- 离线剧集的作品→季→集完整层级与部分下载计数；当前离线源按已完成媒体项平铺展示。
- Provider提供多个静态清晰度时的专用下载确认器；已支持详情页显式媒体版本，尚未提供独立variant选择界面。
- Server在线插件仍因缺少用途受限的稳定离线文件流契约而安全禁用；本任务严格未修改`server/`。
- 短期302失效、实体中途变化和多分段checksum的真实本地HTTP端到端fixture，以及Windows/Android人工运行验证。

## 14. Trellis第二轮质量复核（2026-08-26）

本轮在第二批实现基础上继续沿真实执行路径复核并直接修复：

- Android删除失败任务现在直接调用SAF插件清除精确拥有的partial；插件暂时失败时才写内部cleanup事实，不再出现任务已消失但临时文件必然残留。
- Android启动恢复会异步消费`android_saf_partial`与`android_saf_final`清理记录。中断在最终重命名附近的取消任务同时覆盖完成文件清理，并用条件完成更新防止`cancel_requested`被晚到的`completed`覆盖。
- Android完成文件解析新增基于Document ID、大小与修改时间的实体指纹；离线索引持久化该指纹，本地优先解析会优先读取离线索引而不是仅有大小的历史任务行，播放前同时核对大小和指纹，能够拒绝同大小替换文件。
- 共享限速设置更新会重置旧预约；已等待worker每100毫秒检查限速变化，关闭或修改限速不会继续睡眠旧的长预约。分段worker完成后递减`active_segments`并发送更新，UI不再一直显示启动时的分段数。
- 修复OfflineDataSource跨层DTO漂移：Rust离线详情返回真实离线记录`id`，虚拟来源按该ID精确读取版本详情；原来源不可用时仍可按`sourceId + itemId`回退最新可用离线快照，多个版本不会在离线列表点击时串详情。
- Android专项脚本补充最终文件取消清理、延期SAF清理和完成文件实体校验断言；Rust新增“晚到取消不能被完成覆盖”回归测试。

自动门禁结果：TypeScript类型检查、ESLint、Vite生产构建和下载规划/Android下载/Server数据源/Server在线库/媒体操作/安全播放路由/弹幕/移动端专项脚本全部通过；Windows Rust fmt、check、106项测试及全target/all-feature clippy严格检查通过；`git diff --check`通过且`server/**`无本任务改动。

Android Rust目标检查已使用本机NDK clang发起，但在进入OhMyCine crate前被Tauri依赖缓存中的`.tauri/tauri-api`目录已存在错误阻断，未删除用户Cargo缓存；Android真实APK、SAF运行、前台通知和断网播放仍需设备手测。`verify:view-architecture`仍只命中任务前已存在的`SettingsView.vue`超过4000行，本任务删除了其下载目录区块而未增加体积。

仍未完成的PRD范围保持为：持久海报/背景/still、外置字幕和弹幕资产及附件独立重试；离线作品→季→集层级和部分下载计数；独立清晰度variant选择器；Server在线插件用途受限离线流契约；短期302/实体变化/checksum真实HTTP fixture与Windows/Android人工运行验证。

## 15. Player附件、层级与清晰度收口（2026-08-27）

第11至14节保留为历史执行记录，其中“附件、层级、variant仍未完成”的描述已被本节取代。本轮完成：

- 将海报、背景、单集图、外置字幕和Provider弹幕保存到Player拥有的`data/offline/<package-id>/assets`；数据库仅保存受控相对路径、类型、状态和安全错误码，URL/Header只作为单次Tauri命令的瞬时输入。
- 视频完成后自动补全附件；附件失败不会影响视频ready状态，下载中心显示`pending/syncing/partial/complete`并允许单独重试。
- `OfflineDataSource`读取持久图片、字幕和弹幕；离线视频优先播放时继续使用原来源媒体身份写观看历史，避免重复记录。
- 离线剧集按作品→季→集组织；已知远程总集数时徽标显示`3/12`，只有离线索引时显示`3 集已下载`。
- 详情页增加独立“离线下载”动作；Provider明确提供多个静态variant时显示清晰度选择，选择结果以稳定`mediaSourceId + variantId`进入任务，不制造转码清晰度。
- 修复附件查询的Rust生命周期错误；修复删除单集或纠正缺失文件时遗留其附件记录/文件的问题，并在每个离线SQLite连接启用外键级联。受控包目录拒绝符号链接，最后一个条目删除后才移除整个包。

最终自动门禁：Vue TypeScript类型检查、ESLint、Vite生产构建；下载规划、Android下载、Server DataSource、Server在线库、媒体操作、安全播放路由、弹幕、移动端、Android播放、播放来源生命周期、Server库导航、媒体库封面和清晰度切换专项脚本全部通过；Rust `fmt`、`check`、108项测试及全target/all-feature Clippy严格检查通过。

仍需真实环境验证或外部契约支持：Windows下载中断/302过期/断网冷启动手测；Android APK、SAF、通知和断网播放设备手测；Server在线插件缺少用途受限稳定离线流时继续安全禁用；真实本地HTTP短期302/实体变化/checksum fixture仍待补充。本任务未修改`server/`。

## 16. Trellis最终安全与一致性复核（2026-08-27）

最终复核沿附件重试、离线播放进度和本地资产路径重新走完真实代码路径，并直接修复：

- 附件重试不再预先删除已有`offline_assets`行和文件。新附件先经过类型/大小/结构校验，写入内容寻址的新文件并成功更新数据库后才删除旧文件；网络或数据库失败时保留已有完整海报、字幕和弹幕。失败占位也不能把现有`complete`行降级为`failed`，新增回归测试锁定该行为。
- 附件网络边界拒绝Host、Range、Content-Length和hop-by-hop/request-shaping Header，限制Header数量、单值和总字节；跨源重定向继续清空全部Provider Header并禁止HTTPS降级。
- 海报、字幕和弹幕分别使用大小上限；响应Content-Type执行早期兼容性检查，最终仍以文件签名/UTF-8/JSON结构为准。弹幕在Rust落盘前限制为最多20万条，并校验ID、时间、模式、颜色和文本长度。
- 离线资产读路径不再隐式创建目录；写入和删除会拒绝符号链接，Windows还显式拒绝reparse point。文件使用同目录临时文件、`sync_all`和最终rename，避免半写附件被登记为完成。
- 离线播放历史原先只把历史主键映射回原来源，但进度DTO的`itemId`仍可能使用离线记录ID；现在历史、来源进度同步和完成事件统一使用`offlineOriginItemId`。

最终自动门禁：Vue TypeScript、ESLint、Vite生产构建；下载规划、Android下载、Server DataSource、Server在线库、媒体操作、安全播放路由、弹幕、移动端、Android播放、播放来源生命周期、Server库导航、媒体库封面和清晰度切换专项脚本全部通过；Rust fmt、check、110项测试及全target/all-feature Clippy严格检查通过；`git diff --check`通过，未修改`server/**`。

仍需真实环境或外部契约验证：Windows/Android实际下载、SAF、通知、短期302过期和断网冷启动；Server在线插件的用途受限稳定离线流。当前公共`MediaItem`对单集没有跨Provider一致的父剧集稳定ID，离线层级只能在来源内按剧名/季号投影；要彻底消除同名剧碰撞，需要后续DataSource契约补充稳定`seriesIdentity`，不能用显示标题冒充稳定身份。

## 17. Android真实编译与发布前门禁（2026-08-27）

- Android ARM64 Rust目标已经编译成功，Gradle已产出`player/src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk`。
- 真实Android编译发现取消/删除辅助路径曾把`rusqlite::Connection`跨过异步SAF调用，导致Future不满足`Send`；同时清理查询存在statement临时生命周期问题。实现已改为在`.await`前结束SQLite作用域，平台文件操作完成后重新打开数据库写最终事实。
- 最终自动门禁保持通过：Vue TypeScript、ESLint、Vite生产构建、13组Player专项验证、Rust fmt/check、110项测试、全target/all-feature Clippy严格检查、Windows MSVC release编译、Windows NSIS安装包、Android ARM64 Rust编译与APK组装、`git diff --check`；`server/**`无本任务修改。
- 真实本地HTTP短期302/实体变化/checksum fixture仍未完成；Windows与Android真实设备手测仍未执行，因此第9节两项人工验证保持未勾选，不能由编译或模拟门禁替代。
