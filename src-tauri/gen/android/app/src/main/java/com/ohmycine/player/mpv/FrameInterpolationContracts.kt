package com.ohmycine.player.mpv

internal data class FrameInterpolationCapability(
    val supported: Boolean,
    val backend: String?,
    val reason: String?,
    val apiLevel: Int?,
    val gpuName: String?,
    val gpuAdapterId: String?,
    val fp16: Boolean,
    val hdrKinds: List<String>,
    val maxTargetFps: Int?,
)
