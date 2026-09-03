import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { normalizePlayerInteractionSettings } from '../src/services/playerInteractionSettings'

const root = new URL('../', import.meta.url)

async function source(path: string): Promise<string> {
  return readFile(fileURLToPath(new URL(path, root)), 'utf8')
}

const defaults = normalizePlayerInteractionSettings({})
assert.deepEqual({
  mode: defaults.frameInterpolationMode,
  target: defaults.frameInterpolationTarget,
  quality: defaults.frameInterpolationQuality,
}, {
  mode: 'off',
  target: 'auto',
  quality: 'auto',
})

const normalized = normalizePlayerInteractionSettings({
  frameInterpolationMode: 'invalid' as never,
  frameInterpolationTarget: '240' as never,
  frameInterpolationQuality: 'cinematic' as never,
})
assert.equal(normalized.frameInterpolationMode, 'off')
assert.equal(normalized.frameInterpolationTarget, 'auto')
assert.equal(normalized.frameInterpolationQuality, 'auto')

const useMpv = await source('src/composables/useMpv.ts')
for (const field of ['frameInterpolationMode', 'frameInterpolationTarget', 'frameInterpolationQuality'])
  assert.match(useMpv, new RegExp(`${field}: settings\\.${field}`))

const diagnosticFields = [
  'frameInterpolationRequestedMode',
  'frameInterpolationEffectiveState',
  'frameInterpolationReason',
  'frameInterpolationBackend',
  'frameInterpolationInputHdrKind',
  'frameInterpolationOutputHdrMode',
  'frameInterpolationTargetFps',
  'frameInterpolationFlowScale',
  'frameInterpolationModelTimeP50Ms',
  'frameInterpolationModelTimeP95Ms',
  'frameInterpolationDroppedFrames',
  'frameInterpolationCapability',
] as const

const rustShared = await source('src-tauri/src/commands/player_shared.rs')
const rustPlayer = await source('src-tauri/src/mpv/player.rs')
const rustMobile = await source('src-tauri/src/mpv/mobile.rs')
const rustDesktopCommand = await source('src-tauri/src/commands/player.rs')
const kotlinPlugin = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvPlugin.kt')
const kotlinHost = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/MpvSurfaceHost.kt')
const kotlinPolicy = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/FrameInterpolationPolicy.kt')
const kotlinPolicyTest = await source('src-tauri/gen/android/app/src/test/java/com/ohmycine/player/mpv/FrameInterpolationPolicyTest.kt')
const androidCapabilityProbe = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/AndroidFrameInterpolationCapabilityProbe.kt')
const androidNativeBridge = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/AndroidFrameInterpolationNative.kt')
const androidController = await source('src-tauri/gen/android/app/src/main/java/com/ohmycine/player/mpv/FrameInterpolationController.kt')
const androidNativeProbe = await source('src-tauri/gen/android/app/src/main/cpp/frame_interpolation_probe.cpp')
const androidNcnnProbe = await source('src-tauri/gen/android/app/src/main/cpp/ncnn_model_probe.cpp')
const androidRifeWarp = await source('src-tauri/gen/android/app/src/main/cpp/rife_warp.cpp')
const androidFrameSource = await source('src-tauri/gen/android/app/src/main/cpp/android_frame_source.cpp')
const androidFrameSourceJni = await source('src-tauri/gen/android/app/src/main/cpp/android_frame_source_jni.cpp')
const androidFrameSession = await source('src-tauri/gen/android/app/src/main/cpp/android_frame_interpolation_session.cpp')
const androidFrameProcessor = await source('src-tauri/gen/android/app/src/main/cpp/android_ncnn_frame_processor.cpp')
const androidDataspaceCompat = await source('src-tauri/gen/android/app/src/main/cpp/android_dataspace_compat.h')
const androidCmake = await source('src-tauri/gen/android/app/src/main/cpp/CMakeLists.txt')
const androidNativeVerification = await source('scripts/verify-android-frame-interpolation-native.mjs')
const rustController = await source('src-tauri/src/mpv/frame_interpolation.rs')
const desktopRuntimeSetup = await source('scripts/setup-libmpv.mjs')
const androidRuntimeSetup = await source('scripts/setup-libmpv-android.mjs')
const modelSetup = await source('scripts/setup-frame-interpolation-model.mjs')
const onnxExporter = await source('scripts/export-rife-v4.6-flow-mask.py')
const runtimeManifest = JSON.parse(await source('src-tauri/resources/frame-interpolation/runtime-manifest.json'))
const modelManifest = JSON.parse(await source('src-tauri/resources/frame-interpolation/model-manifest.json'))
const interpolationSettingsUi = await source('src/components/player/FrameInterpolationSettingsContent.vue')
const desktopSettingsPanel = await source('src/components/player/PlayerSettingsPanel.vue')
const mobileControls = await source('src/components/player/MobilePlayerControls.vue')
const playerView = await source('src/views/PlayerView.vue')
const windowsProbe = await source('tools/windows-frame-generation-probe/src/main.rs')
const windowsDirectMlProbe = await source('src-tauri/native/windows_frame_interpolation_probe.cpp')
const windowsAssetGate = await source('src-tauri/src/mpv/windows_frame_interpolation_assets.rs')
const windowsBundle = JSON.parse(await source('src-tauri/tauri.windows.conf.json'))
const releaseWorkflow = await source('.github/workflows/player-beta-release.yml')

for (const field of diagnosticFields) {
  assert.match(useMpv, new RegExp(field))
  assert.match(kotlinHost, new RegExp(field))
}

for (const field of ['frame_interpolation_requested_mode', 'frame_interpolation_effective_state', 'frame_interpolation_capability'])
  assert.match(rustShared, new RegExp(field))

assert.match(rustMobile, /#\[serde\(flatten\)\][\s\S]*?FrameInterpolationDiagnostics/)
assert.match(rustPlayer, /get_property_string\("hwdec-current"\)/)
assert.match(rustPlayer, /get_property_string\("video-format"\)/)
assert.match(rustPlayer, /get_property_string\("audio-codec-name"\)/)
assert.match(rustPlayer, /get_property_flag\("vo-configured"\)/)
assert.match(rustDesktopCommand, /player\.playback_diagnostics\(\)/)
assert.doesNotMatch(rustDesktopCommand, /hardware_decoder:\s*None/)

for (const field of ['frameInterpolationMode', 'frameInterpolationTarget', 'frameInterpolationQuality']) {
  assert.match(kotlinPlugin, new RegExp(field))
  assert.match(kotlinHost, new RegExp(field))
}

assert.match(rustShared, /frame_interpolation_mode == "off"[\s\S]*?\("disabled", None\)/)
assert.match(rustShared, /"unavailable-no-hwdec"/)
assert.match(rustShared, /"backend-unavailable"/)
assert.match(kotlinHost, /FrameInterpolationController\(\)/)
assert.match(kotlinHost, /frameInterpolationController\.setGates/)
assert.match(kotlinPolicy, /requestedMode == "off"[\s\S]*?"disabled"/)
assert.match(kotlinPolicy, /startsWith\("mediacodec"\)[\s\S]*?"unavailable-no-hwdec"/)
assert.match(kotlinPolicy, /if \(backendAvailable\) "probing" else "backend-unavailable"/)
assert.match(kotlinPolicyTest, /invalidSettingsNormalizeToSafeDefaults/)
assert.match(kotlinPolicyTest, /interpolationNeverArmsWithoutMediaCodecOrBackend/)
assert.match(androidCapabilityProbe, /MIN_API_LEVEL = 29/)
assert.match(androidCapabilityProbe, /FEATURE_VULKAN_HARDWARE_VERSION/)
assert.match(androidCapabilityProbe, /HardwareBuffer\.RGBA_FP16/)
assert.match(androidCapabilityProbe, /USAGE_GPU_SAMPLED_IMAGE or HardwareBuffer\.USAGE_GPU_COLOR_OUTPUT/)
assert.match(androidCapabilityProbe, /HDR_TYPE_DOLBY_VISION/)
assert.match(androidCapabilityProbe, /HDR_TYPE_HDR10_PLUS/)
assert.match(androidCapabilityProbe, /verifyModelAssets/)
assert.match(androidCapabilityProbe, /val supported = apiLevel >= MIN_API_LEVEL/)
assert.match(androidCapabilityProbe, /backend = if \(supported\) "android-ncnn-vulkan" else null/)
assert.match(androidCapabilityProbe, /nativeProbe\.sdrDataspace &&[\s\S]*?nativeProbe\.ncnnInferenceSelfTest &&[\s\S]*?modelBundled/)
assert.match(androidNativeBridge, /nativeProbe\(modelParamPath: String, modelBinPath: String\)/)
assert.match(androidNativeProbe, /AImageReader_newWithUsage/)
assert.match(androidNativeProbe, /AIMAGE_FORMAT_RGBA_FP16/)
assert.match(androidNativeProbe, /android_compat::set_buffers_dataspace/)
assert.match(androidDataspaceCompat, /dlopen\("libnativewindow\.so"/)
assert.match(androidDataspaceCompat, /dlsym/)
assert.match(androidDataspaceCompat, /ANativeWindow_setBuffersDataSpace/)
assert.match(androidNativeProbe, /ADATASPACE_BT2020_PQ/)
assert.match(androidNativeProbe, /ADATASPACE_BT2020_HLG/)
assert.match(androidNativeProbe, /ADATASPACE_SRGB_LINEAR/)
assert.match(androidNativeProbe, /sdrDataspace/)
assert.match(androidCapabilityProbe, /nativeProbe\.sdrDataspace[\s\S]*?add\("sdr"\)/)
assert.match(androidCapabilityProbe, /hasHdrOutput && nativeProbe\.hdrDataspace && nativeProbe\.linearHdrDataspace/)
assert.match(androidNativeProbe, /vkGetAndroidHardwareBufferPropertiesANDROID/)
assert.match(androidNativeProbe, /VkImportAndroidHardwareBufferInfoANDROID/)
assert.match(androidNativeProbe, /vkBindImageMemory/)
assert.match(androidNativeProbe, /VK_ANDROID_EXTERNAL_MEMORY_ANDROID_HARDWARE_BUFFER_EXTENSION_NAME/)
assert.match(androidNcnnProbe, /network\.load_param/)
assert.match(androidNcnnProbe, /network\.load_model/)
assert.match(androidNcnnProbe, /use_vulkan_compute = true/)
assert.match(androidNcnnProbe, /load_and_test_model\(model_param_path, model_bin_path, false\)/)
assert.match(androidNcnnProbe, /extractor\.extract\("327"/)
assert.match(androidNcnnProbe, /extractor\.extract\("332"/)
assert.match(androidNcnnProbe, /do not consume out0 for either[\s\S]*?with SDR[\s\S]*?linearized[\s\S]*?common warp\/blend pass/)
assert.doesNotMatch(androidNcnnProbe, /WarpPlaceholder/)
assert.match(androidRifeWarp, /support_vulkan = true/)
assert.match(androidRifeWarp, /opt\.use_fp16_packed/)
assert.match(androidRifeWarp, /record_pipeline/)
assert.match(androidFrameSource, /AImageReader_acquireLatestImage/)
assert.match(androidFrameSource, /AImage_getHardwareBuffer/)
assert.match(androidFrameSource, /generation != generation_/)
assert.match(androidFrameSourceJni, /ANativeWindow_toSurface/)
assert.match(androidCmake, /find_package\(ncnn REQUIRED\)/)
assert.match(androidCmake, /__ANDROID_UNAVAILABLE_SYMBOLS_ARE_WEAK__/)
assert.match(androidCmake, /mediandk ncnn vulkan/)
assert.doesNotMatch(androidCmake, /\bnativewindow\b/)
assert.match(androidFrameSource, /android_compat::set_buffers_dataspace/)
assert.match(androidNativeVerification, /fullSessionLinked: true/)
assert.match(androidFrameProcessor, /vkQueuePresentKHR[\s\S]*?present_result != VK_SUCCESS[\s\S]*?\+\+snapshot_\.presented_frames/)
assert.match(androidFrameSession, /processor_snapshot\.presented_frames > 0/)
assert.match(kotlinHost, /snapshot\.firstFramePresented[\s\S]*?backendFirstFrame\(generation\)/)
assert.match(kotlinHost, /frameOutputView\?\.alpha = 0f[\s\S]*?MPVLib\.attachSurface\(holder\.surface\)/)
assert.match(kotlinHost, /"hwdec-current", "video-params\/gamma" -> requestFrameInterpolationReconcile\(\)/)
assert.match(androidFrameSource, /android_get_device_api_level\(\) < 29/)
assert.match(androidController, /frameGeneration != generation/)
assert.match(androidController, /SURFACE_LOST/)
assert.match(androidController, /unavailable-graphic-subtitle/)
assert.match(kotlinHost, /hdmv_pgs_subtitle/)
assert.match(rustController, /generation != self\.generation/)
assert.match(rustController, /UnavailableGraphicSubtitle/)
assert.match(rustController, /CadenceScheduler/)
assert.match(rustController, /luma_sad <= 0\.32/)
assert.match(kotlinHost, /AndroidFrameInterpolationCapabilityProbe\.probe/)
assert.doesNotMatch(rustShared, /effective_state[^\n]*"active"/)
assert.doesNotMatch(kotlinPolicy, /return "active"/)
assert.doesNotMatch(desktopRuntimeSetup, /releases\/latest\/download/)
assert.match(desktopRuntimeSetup, /2026-08-30-e8673660ab/)
assert.match(desktopRuntimeSetup, /7659f968ccea69168aa8924ea1bf7c524e996946d184720d79f92241805f4724/)
assert.match(desktopRuntimeSetup, /verifySha256\(archivePath, expectedSha256\)/)
assert.equal(runtimeManifest.windows.mpvCommit, 'e8673660ab7ee5d4ea8f93e4bf3a6e170ab2a19a')
assert.equal(runtimeManifest.android.release, '2026-04-25')
assert.equal(runtimeManifest.android.archiveSha256, '4400bcba6be9cec1128e24d1eba153d8727384926b0639fa7fe44d4e36b04f81')
assert.match(androidRuntimeSetup, new RegExp(runtimeManifest.android.archiveSha256))
assert.match(androidRuntimeSetup, new RegExp(runtimeManifest.android.ncnn.archiveSha256))
assert.equal(runtimeManifest.android.ncnn.license, 'BSD-3-Clause')
assert.equal(runtimeManifest.windows.onnxRuntimeDirectMl.release, '1.24.4')
assert.equal(runtimeManifest.windows.onnxRuntimeDirectMl.directMlRelease, '1.15.4')
assert.match(desktopRuntimeSetup, new RegExp(runtimeManifest.windows.onnxRuntimeDirectMl.packageSha256))
assert.match(desktopRuntimeSetup, new RegExp(runtimeManifest.windows.onnxRuntimeDirectMl.directMlPackageSha256))
assert.equal(runtimeManifest.interpolationRuntime.status, 'model-audited')
assert.equal(runtimeManifest.interpolationRuntime.licenseAuditComplete, true)
assert.equal(runtimeManifest.interpolationRuntime.model.sourceCommit, 'a7532fc3f9f8f008cd6eecd6f2ffe2a9698e0cf7')
assert.equal(modelManifest.inferenceOutputs.flow.blob, '327')
assert.equal(modelManifest.inferenceOutputs.mask.blob, '332')
assert.equal(modelManifest.inferenceOutputs.prohibitedOutput.blob, 'out0')
assert.match(modelManifest.inferenceOutputs.prohibitedOutput.reason, /SDR and HDR are both synthesized/)
assert.match(runtimeManifest.interpolationRuntime.model.outputContract, /out0 is prohibited for both SDR and HDR/)
const windowsResources = windowsBundle.bundle?.resources ?? {}
assert.equal(
  windowsResources['resources/frame-interpolation/models/rife-v4.6/rife-v4.6-flow-mask.onnx'],
  'resources/frame-interpolation/models/rife-v4.6/rife-v4.6-flow-mask.onnx',
)
for (const runtime of ['onnxruntime.dll', 'onnxruntime_providers_shared.dll', 'DirectML.dll']) {
  assert.equal(windowsResources[`lib/frame-interpolation/${runtime}`], runtime)
  assert.match(releaseWorkflow, new RegExp(`for file in onnxruntime\\.dll onnxruntime_providers_shared\\.dll DirectML\\.dll`))
}
assert.match(releaseWorkflow, /rife-v4\.6-flow-mask\.onnx/)
assert.match(releaseWorkflow, /npm run verify:frame-interpolation:android-native/)
assert.match(releaseWorkflow, /release-windows-msvc:/)
assert.match(releaseWorkflow, /runs-on: windows-latest/)
assert.match(releaseWorkflow, /TARGET: x86_64-pc-windows-msvc/)
assert.match(releaseWorkflow, /npm run tauri:build:windows:native/)
assert.match(releaseWorkflow, /choco install zip -y --no-progress/)
assert.doesNotMatch(releaseWorkflow, /release-windows-gnu:/)
assert.equal(modelManifest.windowsOnnx.outputSha256, '067f1eb525cebb0f3d737aac9ca26425e6fad3cdf9afffcb674bd8b62aa03a54')
assert.deepEqual(modelManifest.windowsOnnx.outputs, ['flow_pixels', 'blend_mask'])
assert.match(modelSetup, new RegExp(modelManifest.files['flownet.bin'].sha256))
assert.match(modelSetup, /--android/)
assert.match(modelSetup, /--windows/)
assert.match(onnxExporter, /output_names=\("flow_pixels", "blend_mask"\)/)
assert.doesNotMatch(onnxExporter, /output_names=.*(?:rgb|merged|out0)/i)
assert.match(interpolationSettingsUi, /视频插帧/)
assert.match(interpolationSettingsUi, /:disabled="!capabilityAvailable"/)
assert.match(interpolationSettingsUi, /不会把 HDR 静默转成 SDR/)
assert.match(desktopSettingsPanel, /FrameInterpolationSettingsContent/)
assert.match(mobileControls, /openPanel\('frameInterpolation'\)/)
assert.match(playerView, /handleUpdateFrameInterpolationSettings/)
assert.match(playerView, /frameInterpolationCapability\.supported !== true/)
assert.match(windowsProbe, /GraphicsCaptureSession::IsSupported/)
assert.match(windowsProbe, /DXGI_FORMAT_R16G16B16A16_FLOAT/)
assert.match(windowsProbe, /D3D11_BIND_SHADER_RESOURCE \| D3D11_BIND_RENDER_TARGET/)
assert.match(windowsProbe, /DirectML\.dll/)
assert.match(windowsDirectMlProbe, /OrtSessionOptionsAppendExecutionProviderEx_DML/)
assert.match(windowsDirectMlProbe, /CreateGPUAllocationFromD3DResource/)
assert.match(windowsDirectMlProbe, /Ort::MemoryInfo memory\(\s*"DML"/)
assert.doesNotMatch(windowsDirectMlProbe, /CreateCpu\(/)
assert.match(windowsDirectMlProbe, /"flow_pixels", "blend_mask"/)
assert.doesNotMatch(windowsDirectMlProbe, /"(?:output|out0)"/)
assert.match(windowsDirectMlProbe, /swap_desc\.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT;/)
assert.doesNotMatch(windowsDirectMlProbe, /swap_desc\.BufferUsage[^;]*DXGI_USAGE_UNORDERED_ACCESS/)
assert.match(windowsDirectMlProbe, /CreateTexture2D\(FP16 composite output\)/)
assert.match(windowsDirectMlProbe, /CopyResource\(\s*wrapped_buffers\[index\]\.Get\(\), composite_texture\.Get\(\)\)/)
assert.match(windowsAssetGate, new RegExp(modelManifest.windowsOnnx.outputSha256))
assert.match(windowsAssetGate, /ohmycine_probe_directml_flow_mask/)

console.log(JSON.stringify({
  backwardCompatibleDefaults: true,
  strictEnumNormalization: true,
  desktopAndAndroidSettingsContract: true,
  sharedDiagnosticsContract: true,
  realDesktopMpvDiagnostics: true,
  noFalseActiveState: true,
  reproducibleMediaRuntime: true,
  reproducibleWindowsFlowMaskOnnx: true,
  windowsDirectMlInferenceSelfTest: true,
  generationSafeStateMachines: true,
  androidNativeFp16AhbVulkanProbe: true,
  androidNcnnModelLoadGate: true,
  androidRifeFlowMaskInferenceGate: true,
  androidFp16FrameRing: true,
  gatedDesktopAndMobileUi: true,
}, null, 2))
