package com.ohmycine.player.mpv

internal enum class FrameInterpolationMediaEvent {
    START_FILE,
    FILE_LOADED,
    VIDEO_RECONFIG,
    SEEK,
    TRACK_SWITCH,
    SURFACE_LOST,
    SURFACE_READY,
    END_FILE,
}

internal data class FrameInterpolationControllerSnapshot(
    val generation: Long,
    val state: String,
    val reason: String?,
)

internal class FrameInterpolationController {
    private var requested = false
    private var generation = 0L
    private var fileLoaded = false
    private var surfaceReady = false
    private var hardwareDecodeReady = false
    private var hdrPathReady = false
    private var backendAvailable = false
    private var graphicSubtitleActive = false
    private var backendFirstFrameReady = false
    private var state = "disabled"
    private var reason: String? = null

    @Synchronized
    fun setRequested(value: Boolean) {
        requested = value
        if (!value)
            backendFirstFrameReady = false
        reevaluate()
    }

    @Synchronized
    fun setGates(hardwareDecode: Boolean, hdrPath: Boolean, backend: Boolean) {
        hardwareDecodeReady = hardwareDecode
        hdrPathReady = hdrPath
        backendAvailable = backend
        if (!backend)
            backendFirstFrameReady = false
        reevaluate()
    }

    @Synchronized
    fun setGraphicSubtitleActive(value: Boolean) {
        graphicSubtitleActive = value
        if (value)
            backendFirstFrameReady = false
        reevaluate()
    }

    @Synchronized
    fun onMediaEvent(event: FrameInterpolationMediaEvent) {
        when (event) {
            FrameInterpolationMediaEvent.START_FILE -> {
                bumpGeneration()
                fileLoaded = false
                temporaryBypass("正在切换媒体，已清空旧帧。")
            }
            FrameInterpolationMediaEvent.FILE_LOADED -> {
                fileLoaded = true
                reevaluate()
            }
            FrameInterpolationMediaEvent.VIDEO_RECONFIG,
            FrameInterpolationMediaEvent.SEEK,
            FrameInterpolationMediaEvent.TRACK_SWITCH -> {
                bumpGeneration()
                temporaryBypass("视频时序已变化，正在等待新一代真实帧。")
            }
            FrameInterpolationMediaEvent.SURFACE_LOST -> {
                bumpGeneration()
                surfaceReady = false
                temporaryBypass("输出 Surface 已重建，暂时旁路插帧。")
            }
            FrameInterpolationMediaEvent.SURFACE_READY -> {
                surfaceReady = true
                reevaluate()
            }
            FrameInterpolationMediaEvent.END_FILE -> {
                bumpGeneration()
                fileLoaded = false
                temporaryBypass(null)
            }
        }
    }

    @Synchronized
    fun backendFirstFrame(frameGeneration: Long): Boolean {
        if (frameGeneration != generation || !allGatesReady())
            return false
        backendFirstFrameReady = true
        reevaluate()
        return state == "active"
    }

    @Synchronized
    fun backendFailed(message: String) {
        backendFirstFrameReady = false
        state = "backend-error"
        reason = message
    }

    @Synchronized
    fun snapshot(): FrameInterpolationControllerSnapshot = FrameInterpolationControllerSnapshot(
        generation = generation,
        state = state,
        reason = reason,
    )

    private fun bumpGeneration() {
        generation = if (generation == Long.MAX_VALUE) 0L else generation + 1L
        backendFirstFrameReady = false
    }

    private fun temporaryBypass(message: String?) {
        state = if (requested) "temporary-bypass" else "disabled"
        reason = if (requested) message else null
    }

    private fun allGatesReady(): Boolean = requested && fileLoaded && surfaceReady &&
        hardwareDecodeReady && hdrPathReady && backendAvailable && !graphicSubtitleActive

    private fun reevaluate() {
        when {
            !requested -> {
                state = "disabled"
                reason = null
            }
            !hardwareDecodeReady -> {
                state = "unavailable-no-hwdec"
                reason = "当前媒体未使用硬件解码，视频插帧已自动关闭。"
            }
            !hdrPathReady -> {
                state = "unavailable-hdr-path"
                reason = "当前显示链无法保持 FP16 HDR，视频插帧已自动关闭。"
            }
            !backendAvailable -> {
                state = "backend-unavailable"
                reason = "Android 视频插帧原生后端尚未通过自检。"
            }
            graphicSubtitleActive -> {
                state = "unavailable-graphic-subtitle"
                reason = "当前图形字幕需要在视频层合成，已自动旁路插帧。"
            }
            !fileLoaded || !surfaceReady || !backendFirstFrameReady -> {
                state = "probing"
                reason = "正在准备 GPU 插帧管线。"
            }
            else -> {
                state = "active"
                reason = null
            }
        }
    }
}
