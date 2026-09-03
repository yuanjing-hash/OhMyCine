# Technical Design

## 1. Architecture Boundary

保留 libmpv 负责解复用、网络协议、硬件解码、音轨、seek、时钟和 HDR/Dolby Vision 元数据处理。新增 `FrameGenerationController` 作为平台无关控制面，新增 Windows 与 Android 原生 GPU 后端。帧生成层不得成为普通播放的单点故障。

```text
Vue settings/status
        │
Tauri command/event contract
        │
FrameGenerationController
   ├── WindowsFrameGenerationBackend
   └── AndroidFrameGenerationBackend
        │
libmpv original output remains available as bypass
```

控制面拥有请求状态、能力探测、媒体世代、状态机和诊断；原生后端拥有纹理、模型、同步原语与输出表面。

## 2. Shared Contracts

### 2.1 Settings

```ts
type FrameInterpolationMode = 'off' | 'auto'
type FrameInterpolationTarget = 'auto' | '48' | '60' | '120'
type FrameInterpolationQuality = 'auto' | 'quality' | 'balanced' | 'performance'
```

设置加入现有 `PlayerInteractionSettings` 与受控 `MpvEngineSettings`。旧数据缺字段时迁移为 `off/auto/auto`。

### 2.2 Capability

```ts
interface FrameInterpolationCapability {
  supported: boolean
  backend: 'windows-directml' | 'android-ncnn-vulkan' | null
  reason: string | null
  apiLevel?: number
  gpuName?: string
  gpuAdapterId?: string
  fp16: boolean
  hdrKinds: Array<'sdr' | 'pq' | 'hlg' | 'hdr10plus' | 'dolby-vision'>
  maxTargetFps: number | null
}
```

### 2.3 Effective State

`disabled → probing → active` 是正常路径。媒体世代变化、seek、场景切换和表面重建进入 `temporary-bypass`；硬解、HDR 路径或性能问题进入带原因的 fallback。恢复必须通过冷却和重新探测，不能单帧抖动。

## 3. HDR/Dolby Vision Pipeline

### 3.1 Input processing

固定并验证 libmpv、FFmpeg 与 libplacebo 版本。媒体加载后读取：

- `video-params/gamma`、primaries、sig-peak、MaxCLL/MaxFALL；
- Dolby Vision profile、RPU/EL 可用性；
- HDR10+ dynamic metadata；
- `hwdec-current` 与输出格式。

libmpv/libplacebo 先完成 DV reshape、EL pairing 与动态色调映射，再输出高精度处理表面。P7 缺 EL 时标记 `base-layer-fallback`。

### 3.2 Canonical processing values

- HDR10/HLG/HDR10+/DV：FP16 线性光，高精度宽色域；记录 reference white 与 peak luminance。
- Windows 捕获表面使用 `R16G16B16A16_FLOAT` scRGB。
- Android 中间表面使用 `RGBA_FP16` AHardwareBuffer 与正确 ADataSpace。
- SDR 仍可使用 FP16 管线，最终输出 SDR。

模型输入不是原始 PQ/HLG 数值。Compute pass 生成低分辨率、范围受控的 tone-compressed proxy。模型输出双向 flow 和 occlusion mask；confidence 由双向 warp 后的 proxy/亮度差异派生，最终 compute pass 在原始 FP16 帧上采样和混合。

### 3.3 Output semantics

- Windows HDR 桌面：FP16 scRGB swapchain，由 Windows compositor 映射到显示器。
- Android HDR surface：按显示能力选择 BT.2020 PQ/HLG 或平台线性 HDR 输出。
- HDR10+/DV 的动态效果已经体现在生成前的像素中；输出可为逐场景 HDR10/PQ/scRGB，不承诺原始动态元数据传输。

## 4. Windows Backend

```text
libmpv wid/gpu-next/D3D11 source HWND
  → Windows Graphics Capture (FP16)
  → D3D11On12/shared-resource bridge
  → proxy compute
  → ONNX Runtime DirectML flow/mask
  → D3D12 FP16 warp/composite
  → HDR-aware FSR EASU/RCAS
  → frame-latency controlled swapchain
  → transparent Tauri WebView overlay
```

窗口层级为 mpv source HWND、frame-generation output HWND、Tauri WebView。WGC 只捕获 source HWND，避免递归。输出层只有在第一帧生成并验证后才显示；任何错误先隐藏输出层，再销毁资源。

捕获设备、DirectML 设备与输出 adapter LUID 必须一致。不同 GPU 时默认不可用，除非后续明确实现受控跨适配器路径。

## 5. Android Backend

```text
MediaCodec hardware decode
  → libmpv gpu-next
  → offscreen AImageReader/AHardwareBuffer RGBA16F Surface
  → Vulkan external-memory import
  → proxy compute
  → ncnn Vulkan flow/mask
  → Vulkan FP16 warp/composite + FSR
  → real SurfaceView/ANativeWindow
  → WebView overlay
```

应用继续 `minSdk=24`，后端运行时最低建议 API 29。能力探测要求 Vulkan 1.1、external AHardwareBuffer、FP16 storage/compute、支持的 HDR dataspace 与足够的模型基准性能。

生命周期：

- `surfaceCreated` 创建输出与离屏输入；
- `surfaceChanged` 刷新尺寸、刷新率和 HDR dataspace；
- `surfaceDestroyed` 先切回/停止 present，再释放 Vulkan、AImageReader 和 mpv attachment；
- 前后台切换、热状态严重、刷新率变化时进入 temporary bypass 并重建。

API 24–28、无 Vulkan/FP16 或不兼容 GPU 保持普通 mpv Surface 路径。

## 6. Model and Runtime

模型基于明确许可的 RIFE 实现，导出共享的 flow/mask 语义：

- Windows 资产：ONNX FP16，DirectML execution provider；
- Android 资产：ncnn param/bin FP16；
- 资产包含版本、SHA-256、来源与 license notice；
- 不直接使用模型的最终 SDR RGB 输出。

`Flow Scale` 为 1.0/0.67/0.5。Auto 首次按输出尺寸进行短基准并缓存到 GPU/驱动/模型版本组合；运行中依据 P95 生成耗时和掉帧率降档，恢复采用滞回。

## 7. Pacing and A/V Sync

每个真实帧记录媒体 PTS、单调时钟捕获时间和媒体世代。目标 present 时间落在两真实帧之间时计算 `t=(presentPts-p0)/(p1-p0)`。

- 23.976/24→60 使用任意 timestep，不使用固定 3:2 重复。
- 场景切换或低 confidence 选择最近真实帧。
- seek/切轨递增媒体世代并清空队列。
- look-ahead 延迟通过受控音频 delay 或视频调度补偿；停止插帧时平滑恢复。
- DXGI frame latency waitable object 与 Android Choreographer/Display timing 驱动 present。

## 8. Subtitles, Danmaku and FSR

插帧激活时禁止将字幕烧入 source capture。文本字幕通过 mpv subtitle properties/libass 输出到后置覆盖层；弹幕和 Vue UI 已位于 WebView 覆盖层。图形字幕在独立解码完成前触发媒体级旁路。

旧 FSR 保留给非插帧 SDR 路径。插帧 active 时关闭 mpv managed FSR，由平台 compute 后端在生成帧之后执行 HDR-aware EASU/RCAS。HDR 边缘/锐化权重在压缩亮度域计算，最终采样保持 FP16。

## 9. Failure and Rollback

- 原始 mpv HWND/Surface 从不因帧生成 active 而销毁。
- 启用顺序：创建资源 → 捕获真实帧 → 推理/合成成功 → 显示输出层。
- 禁用顺序：隐藏输出层 → 恢复原始表面 → 停止捕获 → 释放模型/纹理。
- 后端 panic/exception 不跨 C ABI；转换为状态和 reason。
- 功能默认关闭，因此版本回滚不需要数据迁移；未知设置字段由旧版本忽略。

## 10. Trade-offs

- 选择 post-render 高精度帧获取而不是重写播放器，保留 libmpv 的网络、音轨、seek 与 HDR/DV 能力。
- Windows 使用 DirectML、Android 使用 ncnn Vulkan，增加双运行时测试成本，但符合各平台设备和图形栈。
- Dolby Vision 输出采用 mpv 相同的动态映射支持语义，不合成新 RPU；这是实现跨平台 GPU 插帧的必要边界。
- Android 首版能力门槛高于应用 minSdk，以可靠旁路换取不破坏现有发行覆盖。

## 11. Research References

- mpv options: `target-colorspace-hint` and `target-colorspace-hint-mode`.
- mpv source: `demux/dovi_split.c`, `filters/f_enhancement_pair.c`.
- Microsoft Windows Graphics Capture HDR guidance.
- RIFE and `rife-ncnn-vulkan` MIT repositories; selected weights require separate artifact-level license audit.
- Lossless Scaling public product behavior and lsfg-vk synchronization documentation are design references only.
