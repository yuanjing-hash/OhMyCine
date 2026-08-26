# 接入 libmpv FSR 超分

## Goal

为低分辨率视频在大屏播放时提供基于 AMD FidelityFX Super Resolution 1.x 的空间超分与锐化，同时确保不支持时无损回退到普通播放。

## Confirmed Facts

- Windows Player 在 `player/src-tauri/src/mpv/player.rs` 中使用 `vo=gpu-next`/`gpu`、独立视频 HWND 和 libmpv 属性设置；视频不经过 WebView 绘制。
- Android Player 在 `MpvSurfaceHost.kt` 中使用 libmpv Android Surface、`gpu-context=android` 与 OpenGL ES，并已有原生引擎设置桥。
- `PlayerInteractionSettings` 已持久化视频输出、硬解、缓存和同步设置，可扩展统一的超分策略。
- 官方 `GPUOpen-Effects/FidelityFX-FSR` 仓库提供 FSR 1.x EASU/RCAS 源码，许可证为 MIT；FSR 1.x 不需要视频运动矢量，适合通用播放器集成。
- FSR 2/3 需要游戏引擎提供的运动矢量、深度和抖动等时域输入，不适合作为当前通用视频播放管线的直接功能。

## Requirements

- 使用 FSR 1.x EASU + RCAS 的 libmpv 用户 Shader 路径，不在 Vue/WebView 中缩放视频。
- 仅在目标显示尺寸大于源视频尺寸时运行 EASU/RCAS；同尺寸或缩小播放不做额外锐化。
- 用户设置至少能够关闭 FSR，并明确显示当前策略；设置通过现有引擎配置桥应用于 Windows 与最终约定的 Android 范围。
- 首发支持 Windows 与 Android，模式为“关闭 / 自动 / 强制”且默认“自动”；“自动”只在画面确实放大且 Shader 能力可用时启用，“强制”绕过保守能力预判但不绕过放大条件和失败回退。
- 提供 RCAS 锐化强度、RCAS 降噪开关，以及“自动 / 1080p / 1440p / 2160p”的目标分辨率上限。UI 数值必须符合“数值越高越锐”的直觉；分辨率档位按输出画面短边解释并保持宽高比，原生层负责映射、有限值、范围和默认值校验。
- Windows 在播放设置面板中展示 FSR 控件；Android 在右上角“三点”菜单中增加 FSR 子面板，不能挤占长按倍速手势或把桌面右键菜单带回手机。
- Shader 资源随应用安装包分发，路径解析不能依赖开发目录或用户绝对路径。
- Shader 编译或加载失败时清除该 Shader 并回退至普通 libmpv 缩放，播放、硬解、字幕和弹幕不受阻塞。
- 诊断状态可说明启用、未触发或回退原因，但不得记录媒体签名 URL、请求头或凭据。
- 保留官方 MIT 许可和派生说明，并验证打包产物包含所需资源。

## Acceptance Criteria

- [x] 设置可保存并在重启后恢复 FSR 策略。
- [x] Windows 播放设置与 Android 三点菜单可调整模式、锐化强度、降噪和目标分辨率上限，运行时修改可安全应用而不重载媒体。
- [ ] 低分辨率视频放大播放时，libmpv 实际加载 FSR Shader；同尺寸/缩小播放不启用。
- [ ] Windows `gpu-next` 和 `gpu` 的约定支持范围内均能正常播放或安全回退。
- [ ] Android 在约定支持范围内能应用 FSR 或根据能力安全回退，不出现黑屏、崩溃或播放阻塞。
- [x] 运行时关闭 FSR 后恢复普通缩放，不要求删除用户配置或重置播放器。
- [x] 安装包、标准包、便携包及 Android 包包含所需 Shader/许可资源。
- [ ] FSR 不破坏硬件解码、字幕、弹幕、窗口缩放、全屏和移动手势。

## Out of Scope

- FSR 2/3、帧生成、插帧、运动补偿或 AI 时域超分。
- 厂商专属驱动超分、RTX Video Super Resolution 或系统级图像增强开关。
- 自动下载第三方 Shader 或允许任意不受信任 Shader 路径。
