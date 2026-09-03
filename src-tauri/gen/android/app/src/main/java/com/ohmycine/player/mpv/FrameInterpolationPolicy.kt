package com.ohmycine.player.mpv

internal object FrameInterpolationPolicy {
    fun normalizeMode(value: String): String = value.takeIf { it == "off" || it == "auto" } ?: "off"

    fun normalizeTarget(value: String): String = value.takeIf {
        it == "auto" || it == "48" || it == "60" || it == "120"
    } ?: "auto"

    fun normalizeQuality(value: String): String = value.takeIf {
        it == "auto" || it == "quality" || it == "balanced" || it == "performance"
    } ?: "auto"

    fun effectiveState(requestedMode: String, currentHardwareDecoder: String?, backendAvailable: Boolean): String {
        if (requestedMode == "off")
            return "disabled"
        val hardwareDecodeReady = currentHardwareDecoder
            ?.lowercase()
            ?.startsWith("mediacodec") == true
        if (!hardwareDecodeReady)
            return "unavailable-no-hwdec"
        return if (backendAvailable) "probing" else "backend-unavailable"
    }
}
