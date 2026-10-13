package com.ohmycine.player.mpv

import android.app.Activity
import android.content.pm.ActivityInfo
import android.content.pm.PackageManager
import android.hardware.display.DisplayManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.view.Display
import org.json.JSONObject

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
}

/** Display declarations guide requests. Actual renderer target metadata confirms negotiation. */
internal class AndroidHdrCapabilities(
    private val activity: Activity,
    private val changed: () -> Unit,
) : DisplayManager.DisplayListener {
    private val manager = activity.getSystemService(DisplayManager::class.java)
    private var originalColorMode: Int? = null
    val runtimeCompatible: Boolean = runCatching {
        val manifest = activity.assets.open("mpv/runtime-manifest.json").bufferedReader().use {
            JSONObject(it.readText())
        }
        val features = manifest.getJSONObject("features")
        manifest.getInt("schemaVersion") == 1
            && manifest.getString("runtimeId") == "ohmycine-mpv-android-vulkan-2026-04-25-v1"
            && features.getBoolean("vulkan") && features.getBoolean("androidvk")
            && features.getBoolean("gpuNext") && features.getBoolean("opengl")
            && features.getBoolean("checkedJni") && features.getBoolean("loadHook")
    }.getOrDefault(false)
    val runtimeVulkan: Boolean = runtimeCompatible
    val deviceVulkan: Boolean = Build.VERSION.SDK_INT >= Build.VERSION_CODES.N
        && activity.packageManager.hasSystemFeature(PackageManager.FEATURE_VULKAN_HARDWARE_VERSION, 0x00401000)

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

    fun canTryVulkan(): Boolean = runtimeVulkan && deviceVulkan
        && hdrTypes().orEmpty().any { it == "hdr10" || it == "hdr10plus" || it == "hlg" }

    fun requestWindowHdr(enabled: Boolean) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O)
            return
        activity.runOnUiThread {
            if (enabled) {
                if (originalColorMode == null)
                    originalColorMode = activity.window.colorMode
                activity.window.colorMode = ActivityInfo.COLOR_MODE_HDR
            } else {
                originalColorMode?.let { activity.window.colorMode = it }
                originalColorMode = null
            }
        }
    }

    fun close() {
        manager?.unregisterDisplayListener(this)
        requestWindowHdr(false)
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
