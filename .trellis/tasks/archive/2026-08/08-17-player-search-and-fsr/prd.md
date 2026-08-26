# Player 作品级搜索、剧内检索与 FSR 超分

## Goal

让 Player 的媒体搜索遵循“作品优先、单集在剧内查找”的信息层级，并为低分辨率视频提供用户可控、失败可回退的 FSR 空间超分能力。

## Background

- 用户在 Android 的 Emby 数据源中搜索“哆啦A梦”时，结果页平铺了大量单集缩略图，而不是优先展示电视剧海报和电影海报。
- `player/src/services/datasource/emby.ts:463` 的顶层搜索当前明确请求 `Movie,Series,Episode`，因此单集进入结果页是数据查询契约导致的，不只是网格渲染问题。
- `player/src/views/MediaDetailView.vue` 已经按季加载并展示分集，但目前没有标题搜索入口。
- Windows 使用 libmpv `vo=gpu-next` 的独立视频 HWND；Android 使用 libmpv 的 Android Surface 与 OpenGL ES。FSR 会同时影响设置、前端到原生设置契约、资源打包和两个运行时。

## Task Map

- `08-17-player-work-level-search`：修复单数据源与跨数据源的作品级搜索层级。
- `08-17-player-series-episode-search`：实现 Series 详情中的跨季分集标题搜索与定位。
- `08-17-player-fsr-upscaling`：实现 FSR 1 Shader、设置、两端原生渲染与回退诊断。

## Requirements

### R1. 作品级顶层搜索

- 顶层媒体库搜索和跨数据源搜索不得把同一电视剧的各个 Episode 平铺为独立作品。
- 当数据源能提供电视剧作品实体时，电视剧结果必须使用 Series 项目及其作品海报；电影继续作为 Movie 项目展示。
- 已识别的原始文件电视剧应复用现有本地分组结果，不因本次修复退化为文件级列表。
- 搜索结果仍需保持正确的数据源归属、详情导航和播放入口。

### R2. 剧内分集标题搜索

- Series 详情页增加清晰的搜索按钮；桌面和手机均可使用。
- 搜索范围为当前剧集的分集标题，结果需要显示季/集编号与标题，避免同名分集无法区分。
- 选择结果后应切换到对应季、选中对应分集并把它滚动到可见位置；搜索本身不得自动开始播放。
- 清空或关闭搜索后恢复正常选集界面和原有选中/续播规则。

### R3. FSR 超分

- 使用适合通用视频帧、无需运动矢量的 AMD FidelityFX Super Resolution 1.x 空间超分方案；不得把需要游戏引擎运动矢量的 FSR 2/3 冒充为可用视频超分。
- FSR 必须通过现有 libmpv 渲染路径生效，而不是在 Vue/WebView 中二次缩放视频。
- 设置必须持久化并从 Vue 传递到 Windows Rust/libmpv 与 Android libmpv Surface。
- 首发同时覆盖 Windows 与 Android，模式提供“关闭 / 自动 / 强制”，默认“自动”。“强制”只绕过保守的设备能力判断，仍不得在同尺寸或缩小场景运行超分。
- 用户可手动调整 RCAS 锐化强度、启用/关闭 RCAS 降噪，并设置“自动 / 1080p / 1440p / 2160p”的目标分辨率上限；档位按输出画面短边解释并保持宽高比。
- Android 播放中入口位于右上角“三点”播放工具菜单内；Windows 桌面入口位于播放时的设置面板内。
- 仅在发生放大时启用超分；原分辨率或缩小播放不应额外锐化。
- Shader 不支持、编译失败或运行设备能力不足时必须回退到正常 libmpv 缩放，不能阻止播放。
- 回退时保留可读的“已关闭 / 未触发 / 已启用 / 已回退”诊断状态；Android 不支持的 GPU 默认静默回退，不以阻塞弹窗打断播放。
- 引入的 FSR 源码或 Shader 必须来自许可证明确、可随开源项目再分发的来源，并保留必要许可声明。

### R4. 兼容与质量

- Player 不依赖 Server 才能完成上述功能。
- Emby 与 Jellyfin 共用适配器时必须同时验证；本地已刮削媒体和原始文件源不得出现明显回归。
- 不改动并行进行中的 Server 下载器、传输和 Web UI 文件。
- 更新受影响的 Player 架构文档、Trellis 前端规范和自动化验证。

## Acceptance Criteria

- [ ] 在同时拥有“哆啦A梦”电视剧和电影的 Emby/Jellyfin 库中搜索“哆啦A梦”，顶层结果展示电视剧作品海报和电影作品，不再平铺每一集。
- [ ] 跨数据源搜索遵循同一作品层级；可识别电视剧不因聚合而重新展开为 Episode。
- [ ] Series 详情页可按分集标题搜索，跨季结果带季/集编号，选择后正确切季、选中并定位，但不自动播放。
- [ ] 搜索无结果、清空和关闭状态在桌面与 Android 布局中均可恢复。
- [ ] FSR 设置持久化，实际通过 libmpv 管线应用；低分辨率视频放大时可观察到生效状态，非放大场景不启用。
- [ ] Windows 播放设置和 Android 右上角“三点”菜单均可切换“关闭 / 自动 / 强制”，并可调整锐化强度、RCAS 降噪和目标分辨率上限；重启后保持设置。
- [ ] Windows 和约定范围内的 Android 设备不支持 FSR 时仍能正常播放，并提供可诊断但不泄露路径或凭据的状态。
- [ ] Player lint、typecheck、前端构建、Rust fmt/check/clippy/test、Windows MSVC 检查及 Android 播放集成验证通过。

## Out of Scope

- FSR 2/3、帧生成、插帧或需要运动矢量的时域超分。
- AI 视频修复、降噪、补帧和厂商专属 RTX Video Super Resolution。
- 改写 Emby/Jellyfin 云端元数据或修改媒体源文件。
- 与本任务无关的 Server 下载、传输或管理界面改动。
