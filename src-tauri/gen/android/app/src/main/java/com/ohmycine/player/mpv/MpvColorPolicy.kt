package com.ohmycine.player.mpv

import android.app.Activity
import android.content.pm.ActivityInfo
import android.hardware.display.DisplayManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.view.Display
import org.json.JSONObject
import java.util.concurrent.atomic.AtomicLong

/** Source classification never relies on gamut or peak strings as booleans. */
internal object MpvColorPolicy {
    private val sdrTransfers = setOf(
        "bt.1886", "srgb", "gamma1.8", "gamma2.0", "gamma2.2", "gamma2.4",
        "gamma2.6", "gamma2.8",
    )

    fun metadata(value: String?): String? = value?.trim()?.takeIf {
        it.length in 1..64 && it.lowercase() !in setOf("unknown", "auto", "none", "null", "no")
            && it.matches(Regex("[A-Za-z0-9_.\\-]+"))
    }

    fun dolbyProfile(value: String?): Int? = value?.trim()?.toIntOrNull()?.takeIf { it in 1..255 }

    fun transfer(value: String?): String? = when (value?.lowercase()) {
        "pq", "st2084", "smpte2084", "smpte-st-2084" -> "pq"
        "hlg", "arib-std-b67" -> "hlg"
        else -> value?.lowercase()
    }

    fun confirmedSdr(gamma: String?, profile: Int?): Boolean =
        profile == null && transfer(gamma) in sdrTransfers

    fun hdrTransfer(gamma: String?): Boolean = transfer(gamma) in setOf("pq", "hlg")

    fun sdrTarget(gamma: String?): Boolean =
        confirmedSdr(gamma, null) || transfer(gamma) == "linear"
}

/** Display declarations guide requests. Actual renderer target metadata confirms negotiation. */
internal class AndroidHdrCapabilities(
    private val activity: Activity,
    private val changed: () -> Unit,
) : DisplayManager.DisplayListener {
    private val manager = activity.getSystemService(DisplayManager::class.java)
    private var originalColorMode: Int? = null
    private val windowRequestGeneration = AtomicLong()
    private val windowRequestLock = Any()
    private var latestPlaybackGeneration = Long.MIN_VALUE
    val runtimeCompatible: Boolean = runCatching {
        val manifest = activity.assets.open("mpv/runtime-manifest.json").bufferedReader().use {
            JSONObject(it.readText())
        }
        val features = manifest.getJSONObject("features")
        manifest.getInt("schemaVersion") == 1
            && manifest.getString("runtimeId") == "ohmycine-mpv-android-vulkan-2026-04-25-v1"
            && features.getBoolean("vulkan") && features.getBoolean("vulkanLoader")
            && features.getBoolean("androidvk")
            && features.getBoolean("gpuNext") && features.getBoolean("opengl")
            && features.getBoolean("androidHdrEgl") && features.getBoolean("externalOesHighp")
            && features.getBoolean("androidEglInitV2")
            && features.getBoolean("hdrHighPrecisionFbo") && features.getBoolean("strictHardwareDecode")
            && features.getBoolean("doviHardwareGuard")
            && features.getBoolean("checkedJni") && features.getBoolean("loadHook")
            && features.getBoolean("endFileReason") && features.getBoolean("playlistEntryIdentity")
    }.getOrDefault(false)

    init {
        manager?.registerDisplayListener(this, Handler(Looper.getMainLooper()))
    }

    fun hdrTypes(): List<String>? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N)
            return null
        val display = activity.window.decorView.display ?: return null
        return runCatching { display.hdrCapabilities.supportedHdrTypes.map { type ->
            when (type) {
                Display.HdrCapabilities.HDR_TYPE_HDR10 -> "hdr10"
                Display.HdrCapabilities.HDR_TYPE_HLG -> "hlg"
                Display.HdrCapabilities.HDR_TYPE_DOLBY_VISION -> "dolby-vision"
                4 -> "hdr10plus"
                else -> null
            }
        }.filterNotNull().distinct() }.getOrNull()
    }

    fun supports(gamma: String?): Boolean = when (MpvColorPolicy.transfer(gamma)) {
        "pq" -> hdrTypes().orEmpty().any { it == "hdr10" || it == "hdr10plus" }
        "hlg" -> "hlg" in hdrTypes().orEmpty()
        else -> false
    }

    fun outputTransfer(sourceGamma: String?): String? = when {
        supports(sourceGamma) -> MpvColorPolicy.transfer(sourceGamma)
        supports("pq") -> "pq"
        supports("hlg") -> "hlg"
        else -> null
    }

    fun canRequestHdrEgl(): Boolean = runtimeCompatible && outputTransfer(null) != null

    fun requestWindowHdr(enabled: Boolean, playbackGeneration: Long = 0L) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O)
            return
        val generation = synchronized(windowRequestLock) {
            if (playbackGeneration < latestPlaybackGeneration)
                return
            latestPlaybackGeneration = playbackGeneration
            windowRequestGeneration.incrementAndGet()
        }
        activity.runOnUiThread {
            if (generation != windowRequestGeneration.get())
                return@runOnUiThread
            if (enabled) {
                if (originalColorMode == null)
                    originalColorMode = activity.window.colorMode
                if (activity.window.colorMode != ActivityInfo.COLOR_MODE_HDR)
                    activity.window.colorMode = ActivityInfo.COLOR_MODE_HDR
            } else {
                originalColorMode?.let {
                    if (activity.window.colorMode != it)
                        activity.window.colorMode = it
                }
                originalColorMode = null
            }
        }
    }

    fun close() {
        manager?.unregisterDisplayListener(this)
        requestWindowHdr(false, Long.MAX_VALUE)
    }

    override fun onDisplayAdded(displayId: Int) = changed()
    override fun onDisplayRemoved(displayId: Int) = changed()
    override fun onDisplayChanged(displayId: Int) = changed()
}

internal data class MpvColorDiagnostics(
    val sourceGamma: String?,
    val sourcePrimaries: String?,
    val sourcePixelFormat: String?,
    val sourceHardwarePixelFormat: String?,
    val dolbyVisionProfile: Int?,
    val targetGamma: String?,
    val targetPrimaries: String?,
    val targetPixelFormat: String?,
    val outputMode: String,
    val gpuContext: String?,
    val displayHdrTypes: List<String>?,
    val reason: String?,
)
