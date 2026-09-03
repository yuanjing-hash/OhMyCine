package com.ohmycine.player.mpv

import android.content.Context
import android.os.Build
import android.view.Surface
import org.json.JSONObject
import java.io.File

internal data class AndroidNativeFrameInterpolationProbe(
    val loaded: Boolean,
    val imageReaderFp16: Boolean,
    val sdrDataspace: Boolean,
    val pqDataspace: Boolean,
    val hlgDataspace: Boolean,
    val linearHdrDataspace: Boolean,
    val hdrDataspace: Boolean,
    val vulkan11: Boolean,
    val ahardwareBufferExternalMemory: Boolean,
    val shaderFloat16: Boolean,
    val ncnnVulkan: Boolean,
    val ncnnModelLoaded: Boolean,
    val ncnnInferenceSelfTest: Boolean,
    val gpuName: String?,
    val ncnnDiagnostic: String?,
    val reason: String?,
)

internal data class AndroidNativeFrameSessionSnapshot(
    val prepared: Boolean,
    val running: Boolean,
    val generation: Long,
    val acquiredFrames: Long,
    val sourceDroppedFrames: Long,
    val importedFrames: Long,
    val importFailures: Long,
    val staleFrames: Long,
    val proxyFrames: Long,
    val inferredPairs: Long,
    val latestInferenceMs: Double,
    val latestTimestampNs: Long,
    val queuedFrames: Int,
    val outputSurfaceReady: Boolean,
    val firstFrameImported: Boolean,
    val firstFramePresented: Boolean,
    val reason: String?,
)

internal object AndroidFrameInterpolationNative {
    private val loaded = runCatching { System.loadLibrary("ohmycine_framegen") }.isSuccess

    private external fun nativeProbe(modelParamPath: String, modelBinPath: String): String
    private external fun nativeCreateInputSurface(
        width: Int,
        height: Int,
        dataSpaceMode: Int,
        generation: Long,
    ): Surface?
    private external fun nativeDestroyInputSurface()
    private external fun nativeFrameSourceSnapshot(): String
    private external fun nativePrepareSession(
        outputSurface: Surface,
        width: Int,
        height: Int,
        dataSpaceMode: Int,
        generation: Long,
        modelParamPath: String,
        modelBinPath: String,
    ): Surface?
    private external fun nativeStartSession(): Boolean
    private external fun nativeStopSession()
    private external fun nativeSessionSnapshot(): String

    fun createInputSurface(width: Int, height: Int, hdrKind: String, generation: Long): Surface? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q || !loaded || width <= 0 || height <= 0)
            return null
        return runCatching {
            nativeCreateInputSurface(width, height, dataSpaceMode(hdrKind), generation)
        }.getOrNull()
    }

    fun prepareSession(
        context: Context,
        outputSurface: Surface,
        width: Int,
        height: Int,
        hdrKind: String,
        generation: Long,
    ): Surface? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q || !loaded ||
            !outputSurface.isValid || width <= 0 || height <= 0) {
            return null
        }
        return runCatching {
            val model = installModel(context)
            nativePrepareSession(
                outputSurface,
                width,
                height,
                dataSpaceMode(hdrKind),
                generation,
                model.first.path,
                model.second.path,
            )
        }.getOrNull()
    }

    fun startSession(): Boolean = loaded && runCatching { nativeStartSession() }.getOrDefault(false)

    fun stopSession() {
        if (loaded)
            runCatching { nativeStopSession() }
    }

    fun sessionSnapshot(): AndroidNativeFrameSessionSnapshot? {
        if (!loaded)
            return null
        return runCatching {
            val value = JSONObject(nativeSessionSnapshot())
            AndroidNativeFrameSessionSnapshot(
                prepared = value.optBoolean("prepared"),
                running = value.optBoolean("running"),
                generation = value.optLong("generation"),
                acquiredFrames = value.optLong("acquiredFrames"),
                sourceDroppedFrames = value.optLong("sourceDroppedFrames"),
                importedFrames = value.optLong("importedFrames"),
                importFailures = value.optLong("importFailures"),
                staleFrames = value.optLong("staleFrames"),
                proxyFrames = value.optLong("proxyFrames"),
                inferredPairs = value.optLong("inferredPairs"),
                latestInferenceMs = value.optDouble("latestInferenceMs"),
                latestTimestampNs = value.optLong("latestTimestampNs"),
                queuedFrames = value.optInt("queuedFrames"),
                outputSurfaceReady = value.optBoolean("outputSurfaceReady"),
                firstFrameImported = value.optBoolean("firstFrameImported"),
                firstFramePresented = value.optBoolean("firstFramePresented"),
                reason = value.optString("reason").takeIf { it.isNotBlank() },
            )
        }.getOrNull()
    }

    fun destroyInputSurface() {
        if (loaded)
            runCatching { nativeDestroyInputSurface() }
    }

    fun frameSourceSnapshot(): JSONObject? {
        if (!loaded)
            return null
        return runCatching { JSONObject(nativeFrameSourceSnapshot()) }.getOrNull()
    }

    fun probe(context: Context): AndroidNativeFrameInterpolationProbe {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            return unavailable("视频插帧要求 Android 10（API 29）或更高版本。")
        }
        if (!loaded) {
            return unavailable("Android ncnn Vulkan 视频插帧运行库尚未打包。")
        }
        return runCatching {
            val model = installModel(context)
            val value = JSONObject(nativeProbe(model.first.path, model.second.path))
            AndroidNativeFrameInterpolationProbe(
                loaded = true,
                imageReaderFp16 = value.optBoolean("imageReaderFp16"),
                sdrDataspace = value.optBoolean("sdrDataspace"),
                pqDataspace = value.optBoolean("pqDataspace"),
                hlgDataspace = value.optBoolean("hlgDataspace"),
                linearHdrDataspace = value.optBoolean("linearHdrDataspace"),
                hdrDataspace = value.optBoolean("hdrDataspace"),
                vulkan11 = value.optBoolean("vulkan11"),
                ahardwareBufferExternalMemory = value.optBoolean("ahardwareBufferExternalMemory"),
                shaderFloat16 = value.optBoolean("shaderFloat16"),
                ncnnVulkan = value.optBoolean("ncnnVulkan"),
                ncnnModelLoaded = value.optBoolean("ncnnModelLoaded"),
                ncnnInferenceSelfTest = value.optBoolean("ncnnInferenceSelfTest"),
                gpuName = value.optString("gpuName").takeIf { it.isNotBlank() },
                ncnnDiagnostic = value.optString("ncnnDiagnostic").takeIf { it.isNotBlank() },
                reason = value.optString("reason").takeIf { it.isNotBlank() },
            )
        }.getOrElse { error ->
            AndroidNativeFrameInterpolationProbe(
                loaded = true,
                imageReaderFp16 = false,
                sdrDataspace = false,
                pqDataspace = false,
                hlgDataspace = false,
                linearHdrDataspace = false,
                hdrDataspace = false,
                vulkan11 = false,
                ahardwareBufferExternalMemory = false,
                shaderFloat16 = false,
                ncnnVulkan = false,
                ncnnModelLoaded = false,
                ncnnInferenceSelfTest = false,
                gpuName = null,
                ncnnDiagnostic = null,
                reason = "Android 视频插帧原生能力探测失败：${error.message ?: "unknown"}",
            )
        }
    }

    private fun unavailable(reason: String) = AndroidNativeFrameInterpolationProbe(
        loaded = false,
        imageReaderFp16 = false,
        sdrDataspace = false,
        pqDataspace = false,
        hlgDataspace = false,
        linearHdrDataspace = false,
        hdrDataspace = false,
        vulkan11 = false,
        ahardwareBufferExternalMemory = false,
        shaderFloat16 = false,
        ncnnVulkan = false,
        ncnnModelLoaded = false,
        ncnnInferenceSelfTest = false,
        gpuName = null,
        ncnnDiagnostic = null,
        reason = reason,
    )

    private fun dataSpaceMode(hdrKind: String): Int = when (hdrKind) {
        "pq", "hdr10plus", "dolby-vision" -> 1
        "hlg" -> 2
        "scrgb-linear" -> 3
        else -> 0
    }

    private fun installModel(context: Context): Pair<File, File> {
        val directory = File(context.filesDir, "frame-interpolation/rife-v4.6").apply { mkdirs() }
        fun install(name: String): File {
            val target = File(directory, name)
            context.assets.open("frame-interpolation/models/rife-v4.6/$name").use { input ->
                target.outputStream().use { output -> input.copyTo(output) }
            }
            return target
        }
        return install("flownet.param") to install("flownet.bin")
    }
}
