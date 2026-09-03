package com.ohmycine.player.mpv

import android.content.Context
import android.content.pm.PackageManager
import android.graphics.PixelFormat
import android.hardware.HardwareBuffer
import android.os.Build
import android.view.Display
import java.security.MessageDigest

internal object AndroidFrameInterpolationCapabilityProbe {
    private const val MIN_API_LEVEL = 29
    private const val MIN_VULKAN_VERSION = 0x00401000
    private const val MODEL_MANIFEST = "frame-interpolation/manifest.json"
    private val MODEL_FILES = mapOf(
        "frame-interpolation/models/rife-v4.6/flownet.param" to
            "724569596bcd1e7b9fa50455c604777ebed99746d2ef40aa86e31b5725f1053c",
        "frame-interpolation/models/rife-v4.6/flownet.bin" to
            "f334ed2260149ce0188a6dcf049844e8b0cdd912e01cbcfb63553157d2508958",
        "frame-interpolation/models/rife-v4.6/LICENSE" to
            "a73beab18143600af0b10c6050a953ec233775ce31c2bf3373a794db535329fd",
    )
    @Volatile
    private var cachedNativeProbe: AndroidNativeFrameInterpolationProbe? = null
    @Volatile
    private var cachedModelIntegrity: Boolean? = null

    fun probe(context: Context, display: Display?): FrameInterpolationCapability {
        val apiLevel = Build.VERSION.SDK_INT
        val vulkan11 = context.packageManager.hasSystemFeature(
            PackageManager.FEATURE_VULKAN_HARDWARE_VERSION,
            MIN_VULKAN_VERSION,
        )
        val fp16 = apiLevel >= MIN_API_LEVEL && supportsFp16HardwareBuffer()
        val nativeProbe = cachedNativeProbe ?: synchronized(this) {
            cachedNativeProbe ?: AndroidFrameInterpolationNative.probe(context).also {
                cachedNativeProbe = it
            }
        }
        val modelBundled = cachedModelIntegrity ?: synchronized(this) {
            cachedModelIntegrity ?: verifyModelAssets(context).also {
                cachedModelIntegrity = it
            }
        }
        val displayHdrTypes = if (apiLevel >= 24) {
            (display?.hdrCapabilities?.supportedHdrTypes ?: intArrayOf()).toSet()
        } else {
            emptySet()
        }
        val hasHdrOutput = displayHdrTypes.any { type ->
            type == Display.HdrCapabilities.HDR_TYPE_HDR10 ||
                type == Display.HdrCapabilities.HDR_TYPE_HLG ||
                type == Display.HdrCapabilities.HDR_TYPE_HDR10_PLUS ||
                type == Display.HdrCapabilities.HDR_TYPE_DOLBY_VISION
        }
        val hdrKinds = buildList {
            if (nativeProbe.sdrDataspace)
                add("sdr")
            if (hasHdrOutput && nativeProbe.hdrDataspace && nativeProbe.linearHdrDataspace) {
                // These are accepted input kinds, not a promise to preserve
                // their dynamic metadata bitstream. mpv/libplacebo applies
                // HDR10+/DV mapping before interpolation and the backend may
                // present the resulting pixels as PQ or HLG.
                addAll(listOf("pq", "hlg", "hdr10plus", "dolby-vision"))
            }
        }
        val supported = apiLevel >= MIN_API_LEVEL &&
            vulkan11 &&
            fp16 &&
            nativeProbe.loaded &&
            nativeProbe.imageReaderFp16 &&
            nativeProbe.sdrDataspace &&
            nativeProbe.vulkan11 &&
            nativeProbe.shaderFloat16 &&
            nativeProbe.ahardwareBufferExternalMemory &&
            nativeProbe.ncnnVulkan &&
            nativeProbe.ncnnModelLoaded &&
            nativeProbe.ncnnInferenceSelfTest &&
            modelBundled
        val reason = when {
            apiLevel < MIN_API_LEVEL -> "视频插帧要求 Android 10（API 29）或更高版本。"
            !vulkan11 -> "设备不支持 Vulkan 1.1，已保持原生硬解播放。"
            !fp16 -> "设备不支持 FP16 AHardwareBuffer，HDR 视频插帧不可用。"
            !nativeProbe.loaded -> nativeProbe.reason ?: "Android 视频插帧原生运行库尚未打包。"
            !nativeProbe.imageReaderFp16 -> nativeProbe.reason ?: "RGBA16F AImageReader 输出不可用。"
            !nativeProbe.sdrDataspace -> nativeProbe.reason ?: "线性 SDR FP16 Surface dataspace 不可用。"
            !nativeProbe.vulkan11 -> nativeProbe.reason ?: "原生 Vulkan 1.1 探测失败。"
            !nativeProbe.shaderFloat16 -> nativeProbe.reason ?: "Vulkan FP16 shader/storage 不可用。"
            !nativeProbe.ahardwareBufferExternalMemory -> nativeProbe.reason ?: "AHardwareBuffer 无法导入 Vulkan。"
            !nativeProbe.ncnnVulkan -> nativeProbe.reason ?: "ncnn Vulkan 运行时不可用。"
            !nativeProbe.ncnnModelLoaded -> nativeProbe.reason ?: "RIFE 模型未通过 ncnn 加载自检。"
            !nativeProbe.ncnnInferenceSelfTest -> nativeProbe.reason ?: "RIFE Vulkan flow/mask 推理自检失败。"
            !modelBundled -> "视频插帧模型尚未通过许可与完整性校验。"
            else -> null
        }
        return FrameInterpolationCapability(
            supported = supported,
            backend = if (supported) "android-ncnn-vulkan" else null,
            reason = reason,
            apiLevel = apiLevel,
            gpuName = nativeProbe.gpuName,
            gpuAdapterId = null,
            fp16 = fp16,
            hdrKinds = hdrKinds,
            maxTargetFps = if (supported) 120 else null,
        )
    }

    private fun verifyModelAssets(context: Context): Boolean = runCatching {
        context.assets.open(MODEL_MANIFEST).use { manifest ->
            check(manifest.read() >= 0)
        }
        MODEL_FILES.all { (path, expected) ->
            val digest = MessageDigest.getInstance("SHA-256")
            context.assets.open(path).use { input ->
                val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0)
                        break
                    digest.update(buffer, 0, count)
                }
            }
            digest.digest().joinToString("") { byte -> "%02x".format(byte) } == expected
        }
    }.getOrDefault(false)

    private fun supportsFp16HardwareBuffer(): Boolean {
        if (Build.VERSION.SDK_INT < MIN_API_LEVEL)
            return false
        return runCatching {
            HardwareBuffer.create(
                16,
                16,
                HardwareBuffer.RGBA_FP16,
                1,
                HardwareBuffer.USAGE_GPU_SAMPLED_IMAGE or HardwareBuffer.USAGE_GPU_COLOR_OUTPUT,
            ).use { buffer -> buffer.width == 16 && buffer.height == 16 }
        }.getOrDefault(false)
    }
}
