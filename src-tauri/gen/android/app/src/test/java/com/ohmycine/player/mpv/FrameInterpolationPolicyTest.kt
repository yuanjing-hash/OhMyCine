package com.ohmycine.player.mpv

import org.junit.Assert.assertEquals
import org.junit.Test

class FrameInterpolationPolicyTest {
    @Test
    fun invalidSettingsNormalizeToSafeDefaults() {
        assertEquals("off", FrameInterpolationPolicy.normalizeMode("force"))
        assertEquals("auto", FrameInterpolationPolicy.normalizeTarget("240"))
        assertEquals("auto", FrameInterpolationPolicy.normalizeQuality("cinematic"))
    }

    @Test
    fun interpolationNeverArmsWithoutMediaCodecOrBackend() {
        assertEquals("disabled", FrameInterpolationPolicy.effectiveState("off", "mediacodec", false))
        assertEquals("unavailable-no-hwdec", FrameInterpolationPolicy.effectiveState("auto", null, false))
        assertEquals("unavailable-no-hwdec", FrameInterpolationPolicy.effectiveState("auto", "ffmpeg", false))
        assertEquals("backend-unavailable", FrameInterpolationPolicy.effectiveState("auto", "mediacodec", false))
        assertEquals("probing", FrameInterpolationPolicy.effectiveState("auto", "mediacodec-copy", true))
    }
}
