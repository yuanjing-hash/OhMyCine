# 跨平台 HDR/Dolby Vision 硬解视频插帧

## Goal

为 OhMyCine Player 的 Windows 与已发布 Android 客户端增加正式的实时 GPU 视频插帧，使电影与动画在高刷新率显示设备上更流畅，同时保持真实硬件解码、HDR 动态范围和普通播放可靠性。功能不依赖 Server，能力不足时自动旁路，不以 CPU 插帧或静默 SDR 转换维持开关状态。

## Background

- Player 使用 Tauri v2、Vue 3、Rust 与 libmpv。Windows 当前通过 `wid + vo=gpu-next` 输出；Android 通过原生 `SurfaceView + libmpv + MediaCodec` 输出。
- Windows 已默认使用 `gpu-next + auto-safe`，但桌面诊断当前将 `hardwareDecoder` 固定为空；Android 已读取 `hwdec-current`。
- 现有 FSR 是单帧 GLSL 超分/锐化，不是时间域插帧；Shader 中 `FSR_PQ` 固定为 `0`，不能作为 HDR-aware 最终路径。
- FFmpeg `minterpolate` 的像素格式能力不足以承载 10-bit/FP16 HDR 正式链路；VapourSynth copy-back 也不作为最终架构。
- “小黄鸭”指 Lossless Scaling / LSFG。项目可借鉴捕获与输出分离、Flow Scale、性能模型、自适应倍帧、场景切换与 frame pacing，但不得集成闭源 LSFG 模型、`Lossless.dll` 或逆向提取资源。
- Android 工程当前为 `minSdk=24`、`targetSdk=36`。应用继续支持现有系统范围，但 GPU 插帧能力可设置更高的运行时门槛。
- mpv 的 Dolby Vision/HDR10+ 支持是解析并应用动态元数据，再向显示链输出 HDR10/PQ/scRGB 等目标格式；mpv 官方说明 `source-dynamic` 不发送完整 HDR10+ 或 Dolby Vision 元数据，而是利用这些信息产生逐场景 HDR10 输出。本任务采用同一支持语义，不宣称 Dolby Vision 认证信号直通。

## Requirements

### R1. 双平台统一产品协议

- Windows 与 Android 使用同一设置字段、状态枚举、诊断语义和自动降级规则。
- 平台后端分别原生实现：Windows 使用高精度窗口捕获与 D3D GPU 合成；Android 使用离屏 Surface/AHardwareBuffer 与 Vulkan 合成。
- 用户设置仅提供 `off | auto`，默认 `off`，不得提供绕过能力门控的强制模式。
- 目标帧率支持 `auto | 48 | 60 | 120`；质量支持 `auto | quality | balanced | performance`。

### R2. 硬件解码不变量

- 插帧仅在当前媒体真实 `hwdec-current` 非空且属于平台允许的硬解后端时进入 active。
- 插帧不得主动将工作的零拷贝硬解切换为软件解码。
- 加载、切轨、seek、恢复或解码重配置后硬解消失时，插帧立即旁路，普通播放继续。
- Windows 桌面诊断必须报告真实 `hwdec-current`；Android 保持并扩展现有诊断。

### R3. HDR 与 Dolby Vision 输入覆盖

- 正式覆盖 SDR、HDR10/PQ、HLG、HDR10+、Dolby Vision Profile 5、Profile 7 与 Profile 8.x 输入。
- Windows 与 Android 使用可复现、能力一致的 libmpv/FFmpeg/libplacebo 构建，包含适用的 Dolby Vision side data、RPU reshape、Profile 7 enhancement pairing 与 HDR10+ ST2094-40 支持。
- Profile 7 的 enhancement layer 无法由当前构建或设备硬解时必须报告 base-layer fallback，不能误报完整处理。
- Dolby Vision/HDR10+ 动态信息在 mpv/libplacebo 中应用后再进入插帧；最终可输出为平台支持的 HDR10/PQ/scRGB，不宣称原生 Dolby Vision 动态元数据直通或认证。

### R4. 高精度 HDR 插帧

- HDR 的中间纹理、帧缓存、warp、blend、空间缩放和输出不得降为 8-bit SDR。
- 支持 HDR 的路径使用 FP16/scRGB 或平台等价的高精度线性表示。
- 运动估计使用低分辨率、色调压缩代理帧；模型只提供 flow、遮挡 mask 与置信度。
- 最终像素合成必须采样高精度原始帧，不直接采用普通 RIFE 的 SDR RGB 输出作为 HDR 结果。
- HDR 链路无法建立时关闭插帧并保留原生 mpv HDR 播放；禁止静默 tone-map 到 SDR 来维持插帧。

### R5. 质量、时序与同步

- 正确处理 23.976/24/25/29.97/30/50/60 FPS 与 VFR，以真实 PTS 和目标 present 时间计算插值时刻。
- 场景切换、低置信度、seek、暂停、窗口/Surface 重建及前后台切换期间不得跨边界生成伪帧。
- 超预算时依次降低 Flow Scale/质量；持续不达标时自动旁路。
- look-ahead 延迟必须补偿到音频时钟，长期播放不产生可感知 A/V 漂移。
- 同一媒体播放过程中不得因单个慢帧频繁启停；降级和恢复需要滞回。

### R6. FSR、字幕与覆盖层

- 处理顺序固定为：真实/生成帧合成 → HDR-aware FSR → 平台输出 → 字幕/弹幕/UI。
- HDR 媒体不得继续走 `FSR_PQ=0` 的旧路径。
- 文本字幕、弹幕和播放器 UI 不参与运动估计，避免拉丝和重影。
- 图形字幕若首版无法可靠分离，必须采取明确的临时旁路策略并显示原因。

### R7. 能力、诊断与恢复

- 暴露 requested mode、capability、effective state、reason、backend、actual hwdec、input HDR kind、output HDR mode、target FPS、Flow Scale、模型耗时与掉帧计数。
- 状态至少包含 `disabled`、`probing`、`active`、`temporary-bypass`、`unavailable-no-hwdec`、`unavailable-hdr-path`、`fallback-performance`、`backend-error`。
- 后端错误、模型加载失败、设备丢失或 Surface/交换链重建不得导致黑屏；原始 libmpv 输出必须可立即恢复。
- 旧设置升级后保持插帧关闭，不改变用户已有 FSR、解码和同步偏好。

### R8. Android 发布约束

- 应用继续支持 `minSdk=24`；插帧运行时建议要求 API 29+、Vulkan 1.1、FP16 与 `VK_ANDROID_external_memory_android_hardware_buffer`。
- Android 不支持设备显示准确不可用原因，普通硬解播放不受影响。
- 支持 Activity 前后台、横竖屏、Surface 重建、刷新率变化、热降频和省电状态。
- 首发至少覆盖现有 Android ARM64 发布 ABI；额外 ABI 必须显式评估模型包体与原生库成本。

### R9. 分发与许可

- 仅打包有明确再分发许可的代码、运行时、模型和权重，并提供第三方 notice。
- 不打包、不下载、不调用 Lossless Scaling 商业资产。
- Windows 与 Android 的模型版本、校验值、包体增量和首次加载耗时可审计。

## Acceptance Criteria

- [ ] Windows 与 Android 设置页提供同一“视频插帧”设置；不可用设备显示原因，不出现无效开关。
- [ ] 24/25/30 FPS SDR 测试片在受支持设备上稳定生成目标帧，诊断同时显示真实硬解与 active 插帧后端。
- [ ] 强制软件解码或播放硬解不支持的编码时，插帧自动关闭，视频与音频继续正常播放。
- [ ] HDR10 10-bit、HLG 与 HDR10+ 测试片不发生 8-bit 中间转换、明显高光裁切、错误色域或静默 SDR 回退。
- [ ] Dolby Vision P5、P7、P8.x 按已定义语义完成解码、动态映射和插帧；完整 EL 与 base-layer fallback 状态准确可见。
- [ ] 场景切换、seek、暂停/恢复、窗口缩放、Android 前后台/横竖屏/Surface 重建不会跨边界造帧或黑屏。
- [ ] 文本字幕、弹幕和播放器 UI 不参与插帧；图形字幕按明确策略旁路。
- [ ] GPU 性能不足、模型加载失败、设备丢失或 HDR 输出失败时，无需重载媒体即可恢复原始画面。
- [ ] 23.976→60、24→60、25→50/60、30→60 无长期 pacing 漂移，A/V 同步满足测试阈值。
- [ ] Windows 构建、Android ARM64 Release 构建、TypeScript、Rust、Kotlin/NDK 与专项验证通过。
- [ ] 第三方许可证、notice、模型校验和包体变化通过发布检查。

## Out of Scope

- 对 Lossless Scaling/LSFG 商业模型、DLL 或逆向 Shader 的集成与再分发。
- 对播放器外其他应用或游戏进行捕获、注入或插帧。
- 用 CPU 插帧作为 GPU或硬解失败后的隐藏兜底。
- 首版开放任意模型文件、任意 mpv 参数或算法插件。
- 宣称 Dolby Vision 官方认证、电视端 Dolby Vision 信号直通或为生成帧合成全新 Dolby Vision RPU。

## Technical Notes

- Windows 首选 WGC FP16/scRGB → DirectML → D3D12；Android 首选 AImageReader/AHardwareBuffer RGBA16F → ncnn Vulkan → Vulkan Surface。
- 同一 RIFE-derived flow/mask 模型保持逻辑一致，平台分别导出 ONNX 与 ncnn 资产。
- 技术未知项通过先行验证收敛：Windows HDR WGC 捕获、Android FP16 离屏 Surface、DV P7 双层硬解，以及模型权重再分发许可。验证失败不得改变产品降级原则，只决定受支持设备集合。
