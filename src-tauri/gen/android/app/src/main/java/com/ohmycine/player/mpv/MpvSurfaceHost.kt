package com.ohmycine.player.mpv

import android.app.Activity
import android.content.Context
import android.graphics.Color
import android.graphics.PixelFormat
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.ViewGroup
import android.webkit.WebView
import android.widget.FrameLayout
import `is`.xyz.mpv.MPVLib
import java.io.File

internal object MpvSurfaceHost : MPVLib.EventObserver, MPVLib.LogObserver {
    private const val PRIMARY_ANDROID_VIDEO_OUTPUT = "gpu-next"
    private const val FALLBACK_ANDROID_VIDEO_OUTPUT = "gpu"
    private const val MAX_DIAGNOSTIC_LOGS = 24
    private const val FSR_SHADER_ASSET = "mpv/ohmycine-fsr-v1.glsl"
    private const val RENDERER_TRANSITION_TIMEOUT_MS = 10_000L

    @Volatile
    private var initialized = false

    @Volatile
    private var surfaceAttached = false

    @Volatile
    private var initializationError: String? = null

    private var surfaceView: OhMyCineMpvSurfaceView? = null
    private var surfaceContainer: FrameLayout? = null
    private var hdrCapabilities: AndroidHdrCapabilities? = null
    @Volatile
    private var sourceMetadataReady = false
    @Volatile
    private var activeGpuContext = "android"
    @Volatile
    private var rendererFallbackReason: String? = null
    @Volatile
    private var vulkanFallbackUsed = false
    @Volatile
    private var legacyFallbackUsed = false
    @Volatile
    private var rendererTransitionPending = false
    @Volatile
    private var rendererTransition: RendererTransition? = null
    @Volatile
    private var nativeFileActive = false
    private var currentLoad: PendingLoad? = null
    private val externalSubtitles = mutableListOf<ExternalSubtitle>()
    private val rendererFailureLogs = ArrayDeque<String>()
    @Volatile
    private var endReason: String? = null
    @Volatile
    private var lastPosition = 0.0
    @Volatile
    private var lastDuration = 0.0
    private var pendingRendererResume: Double? = null
    private var lastSourceColor: MpvColorDiagnostics? = null
    private var lastVideoFormat: String? = null
    private var lastAudioCodec: String? = null
    private var lastHardwareDecoder: String? = null
    @Volatile
    private var hdrHintRequested = false
    @Volatile
    private var playbackGeneration = 0L
    @Volatile
    private var loadPrepared = false
    @Volatile
    private var waitingForStartFile = false
    @Volatile
    private var rendererNeedsConfiguration = false
    private var managedFsrAttached = false
    private var managedFsrOptions: String? = null
    @Volatile
    private var fsrFailedForPlayback = false
    @Volatile
    private var pendingLoad: PendingLoad? = null
    @Volatile
    private var playbackState = "idle"
    @Volatile
    private var lastPlaybackEvent = "not-started"
    @Volatile
    private var lastPlaybackError: String? = null
    @Volatile
    private var fileLoaded = false
    @Volatile
    private var stopRequested = false
    @Volatile
    private var activeVideoOutput = PRIMARY_ANDROID_VIDEO_OUTPUT
    @Volatile
    private var preferredVideoOutput = PRIMARY_ANDROID_VIDEO_OUTPUT
    @Volatile
    private var hardwareDecoder = "mediacodec,mediacodec-copy"
    @Volatile
    private var cacheMode = "auto"
    @Volatile
    private var demuxerMaxBytes = 64L * 1024L * 1024L
    @Volatile
    private var videoSync = "audio"
    @Volatile
    private var videoOutputFallbackUsed = false
    @Volatile
    private var playbackTransport = "none"
    @Volatile
    private var fsrMode = "auto"
    @Volatile
    private var fsrSharpness = 35.0
    @Volatile
    private var fsrDenoise = true
    @Volatile
    private var fsrTarget = "auto"
    @Volatile
    private var fsrShaderFile: File? = null
    @Volatile
    private var fsrStatus = "not-configured"
    @Volatile
    private var fsrReason: String? = null
    @Volatile
    private var surfaceWidth = 0
    @Volatile
    private var surfaceHeight = 0
    private val diagnosticLogs = ArrayDeque<String>()

    fun install(activity: Activity, webView: WebView) {
        webView.post {
            if (surfaceView != null)
                return@post

            try {
                val parent = webView.parent as? ViewGroup
                    ?: error("Android WebView 容器尚未准备完成。")
                val index = parent.indexOfChild(webView)
                val layoutParams = webView.layoutParams
                val surface = OhMyCineMpvSurfaceView(activity)
                val container = FrameLayout(activity).apply {
                    setBackgroundColor(Color.BLACK)
                }

                parent.removeView(webView)
                webView.setBackgroundColor(Color.TRANSPARENT)
                webView.background?.alpha = 0
                container.addView(surface, FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT,
                ))
                container.addView(webView, FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT,
                ))
                parent.addView(container, index, layoutParams)
                surfaceContainer = container
                surfaceView = surface
                hdrCapabilities = AndroidHdrCapabilities(activity) { refreshColorPolicy() }
                surface.initialize()
                initializationError = null
            } catch (error: Exception) {
                initializationError = error.message ?: "Android 播放器初始化失败。"
            }
        }
    }

    fun destroy() {
        playbackGeneration += 1
        hdrCapabilities?.close()
        hdrCapabilities = null
        surfaceView?.destroy()
        surfaceView = null
        surfaceContainer = null
        pendingLoad = null
        currentLoad = null
        nativeFileActive = false
        clearPlaybackFacts()
        surfaceAttached = false
        initialized = false
        initializationError = null
        playbackState = "idle"
        lastPlaybackEvent = "destroyed"
        lastPlaybackError = null
        fileLoaded = false
        stopRequested = false
        activeVideoOutput = PRIMARY_ANDROID_VIDEO_OUTPUT
        preferredVideoOutput = PRIMARY_ANDROID_VIDEO_OUTPUT
        hardwareDecoder = "mediacodec,mediacodec-copy"
        cacheMode = "auto"
        demuxerMaxBytes = 64L * 1024L * 1024L
        videoSync = "audio"
        videoOutputFallbackUsed = false
        playbackTransport = "none"
        fsrMode = "auto"
        fsrSharpness = 35.0
        fsrDenoise = true
        fsrTarget = "auto"
        fsrShaderFile = null
        fsrStatus = "not-configured"
        fsrReason = null
        resetColorSession()
        surfaceWidth = 0
        surfaceHeight = 0
        synchronized(diagnosticLogs) { diagnosticLogs.clear() }
    }

    fun isReady(): Boolean = initialized && surfaceAttached
    fun initializationError(): String? = initializationError

    @Synchronized
    fun load(path: String, audioPath: String?, headers: List<MpvHeader>) {
        initializationError?.let { error(it) }
        val request = PendingLoad(path, audioPath, headers)
        pendingLoad = request
        if (!isReady())
            return
        play(request)
    }

    @Synchronized
    private fun play(request: PendingLoad) {
        pendingLoad = null
        val replacingNativeFile = nativeFileActive
        currentLoad = request
        externalSubtitles.clear()
        clearPlaybackFacts()
        playbackState = "loading"
        lastPlaybackEvent = "load-command"
        lastPlaybackError = null
        fileLoaded = false
        stopRequested = false
        playbackGeneration += 1
        resetColorSession()
        loadPrepared = true
        waitingForStartFile = true
        check(clearManagedFsrShader(true)) { "无法移除旧的 FSR 配置。" }
        fsrStatus = if (fsrMode == "off") "disabled" else "bypassed"
        fsrReason = if (fsrMode == "off") null else "等待确认 SDR 片源；HDR 与杜比视界不使用当前 FSR。"
        val context = if (preferredVideoOutput == PRIMARY_ANDROID_VIDEO_OUTPUT
            && hdrCapabilities?.canTryVulkan() == true) "androidvk" else "android"
        if (replacingNativeFile) {
            beginRendererTransition(context, PRIMARY_ANDROID_VIDEO_OUTPUT, false, 0.0, false, preserveTracks = false)
            return
        }
        // Unknown/Dolby sources retain gpu-next even when the saved legacy preference is gpu.
        runCatching { configureRenderer(context, PRIMARY_ANDROID_VIDEO_OUTPUT) }
            .onFailure { recoverRendererFailure(context) }
        check(playbackState != "error") { lastPlaybackError ?: "Android 渲染配置失败。" }
        playbackTransport = if (request.path.startsWith("http://127.0.0.1:")) "rust-loopback" else "direct"
        synchronized(diagnosticLogs) { diagnosticLogs.clear() }
        loadNativeFile(request)
    }

    private fun loadNativeFile(request: PendingLoad, position: Double? = null, paused: Boolean = false) {
        val headers = request.headers
        val headerFields = headers.joinToString(",") { "${it.name}: ${it.value}" }
        MPVLib.setPropertyString("http-header-fields", headerFields)
        val options = mutableListOf<String>()
        request.audioPath?.let { options.add("audio-file=$it") }
        if (position != null && position.isFinite() && position >= 0.0)
            options.add("start=$position")
        options.add("pause=${if (paused) "yes" else "no"}")
        check(MPVLib.commandChecked(arrayOf("loadfile", request.path, "replace", "-1", options.joinToString(","))) >= 0) {
            "媒体加载命令被拒绝。"
        }
    }

    @Synchronized
    fun applyEngineSettings(settings: MpvEngineSettings) {
        require(settings.videoOutput == "gpu-next" || settings.videoOutput == "gpu")
        require(settings.hardwareDecoder == "auto-safe" || settings.hardwareDecoder == "auto" || settings.hardwareDecoder == "software")
        require(settings.cacheMode == "auto" || settings.cacheMode == "enabled" || settings.cacheMode == "disabled")
        require(settings.demuxerMaxBytesMb == 64 || settings.demuxerMaxBytesMb == 128 || settings.demuxerMaxBytesMb == 256 || settings.demuxerMaxBytesMb == 512)
        require(settings.videoSync == "audio" || settings.videoSync == "display-resample" || settings.videoSync == "display-vdrop")
        preferredVideoOutput = settings.videoOutput
        hardwareDecoder = when (settings.hardwareDecoder) {
            "auto" -> "mediacodec-copy,mediacodec"
            "software" -> "no"
            else -> "mediacodec,mediacodec-copy"
        }
        cacheMode = when (settings.cacheMode) {
            "enabled" -> "yes"
            "disabled" -> "no"
            else -> "auto"
        }
        demuxerMaxBytes = settings.demuxerMaxBytesMb.toLong() * 1024L * 1024L
        videoSync = settings.videoSync
        fsrMode = settings.fsrMode.takeIf { it == "off" || it == "auto" || it == "force" } ?: "auto"
        fsrSharpness = settings.fsrSharpness.takeIf { it.isFinite() }?.coerceIn(0.0, 100.0) ?: 35.0
        fsrDenoise = settings.fsrDenoise
        fsrTarget = settings.fsrTarget.takeIf {
            it == "auto" || it == "1080p" || it == "1440p" || it == "2160p"
        } ?: "auto"
        if (initialized) {
            MPVLib.setPropertyString("hwdec", effectiveHardwareDecoder())
            MPVLib.setPropertyString("cache", cacheMode)
            MPVLib.setPropertyString("demuxer-max-bytes", demuxerMaxBytes.toString())
            MPVLib.setPropertyString("video-sync", videoSync)
            refreshColorPolicy()
        }
    }

    fun addSubtitle(url: String, title: String?, language: String?) {
        requireInitialized()
        externalSubtitles.add(ExternalSubtitle(url, title, language))
        val transition = rendererTransition
        if (transition != null) {
            transition.selection.externalSubtitleUrl = url
            return
        }
        MPVLib.command(arrayOf("sub-add", url, "select", title ?: "", language ?: ""))
    }

    fun pause(paused: Boolean) {
        requireInitialized()
        rendererTransition?.paused = paused
        MPVLib.setPropertyBoolean("pause", paused)
    }

    fun stop() {
        capturePlaybackFacts()
        playbackGeneration += 1
        pendingLoad = null
        currentLoad = null
        endReason = "stop"
        loadPrepared = false
        waitingForStartFile = false
        if (initialized) {
            stopRequested = true
            MPVLib.command(arrayOf("stop"))
            playbackState = "idle"
            lastPlaybackEvent = "stopped"
            fileLoaded = false
        }
        resetColorSession()
        clearManagedFsrShader()
        fsrStatus = if (fsrMode == "off") "disabled" else "bypassed"
        fsrReason = null
    }

    fun seek(position: Double) {
        requireInitialized()
        pendingRendererResume = null
        if (endReason == "eof" && fileLoaded) {
            endReason = null
            playbackState = "playing"
        }
        MPVLib.command(arrayOf("seek", position.toString(), "absolute+exact"))
    }

    fun setProperty(name: String, value: String) {
        requireInitialized()
        rendererTransition?.let { transition ->
            when (name) {
                "aid" -> transition.selection.audio = value
                "sid" -> {
                    transition.selection.subtitle = value
                    transition.selection.externalSubtitleUrl = null
                }
                "pause" -> transition.paused = value == "true" || value == "1" || value == "yes"
                in transition.settings -> transition.settings[name] = value
            }
        }
        when (name) {
            "volume", "time-pos", "duration", "speed", "panscan", "video-zoom",
            "brightness", "sub-delay" -> MPVLib.setPropertyDouble(name, value.toDouble())
            "pause" -> MPVLib.setPropertyBoolean(name, value == "true" || value == "1")
            else -> MPVLib.setPropertyString(name, value)
        }
    }

    fun getProperty(name: String): String {
        requireInitialized()
        return when (name) {
            "pause", "paused-for-cache" -> (MPVLib.getPropertyBoolean(name) ?: false).toString()
            "time-pos" -> snapshot().time.toString()
            "duration" -> snapshot().duration.toString()
            "volume", "speed", "panscan", "video-zoom",
            "brightness", "sub-delay", "cache-speed" -> (MPVLib.getPropertyDouble(name) ?: 0.0).toString()
            else -> MPVLib.getPropertyString(name) ?: ""
        }
    }

    @Synchronized
    fun screenshotToFile(path: String) {
        requireInitialized()
        check(fileLoaded) { "视频画面尚未加载，无法截图。" }
        MPVLib.command(arrayOf("screenshot-to-file", path, "subtitles"))
    }

    fun snapshot(): MpvSnapshot {
        capturePlaybackFacts()
        return MpvSnapshot(
            time = lastPosition,
            duration = lastDuration,
            paused = if (initialized && playbackState == "playing") MPVLib.getPropertyBoolean("pause") ?: true else true,
        )
    }

    private fun clearPlaybackFacts() {
        endReason = null
        lastPosition = 0.0
        lastDuration = 0.0
        pendingRendererResume = null
        lastSourceColor = null
        lastVideoFormat = null
        lastAudioCodec = null
        lastHardwareDecoder = null
        synchronized(rendererFailureLogs) { rendererFailureLogs.clear() }
    }

    private fun capturePlaybackFacts() {
        confirmRendererTransition()
        if (!initialized || !fileLoaded || playbackState != "playing" || rendererTransitionPending || stopRequested)
            return
        MPVLib.getPropertyDouble("time-pos")?.takeIf { it.isFinite() && it >= 0.0 }?.let { position ->
            val resume = pendingRendererResume
            if (resume == null || kotlin.math.abs(position - resume) <= 5.0) {
                lastPosition = position
                pendingRendererResume = null
            }
        }
        MPVLib.getPropertyDouble("duration")?.takeIf { it.isFinite() && it > 0.0 }?.let { lastDuration = it }
        safePropertyString("video-format")?.let { lastVideoFormat = it }
        safePropertyString("audio-codec-name")?.let { lastAudioCodec = it }
        safePropertyString("hwdec-current")?.let { lastHardwareDecoder = it }
        if (sourceMetadataReady)
            lastSourceColor = colorDiagnostics()
    }

    private fun captureSourceMetadata() {
        if (!initialized || !nativeFileActive || waitingForStartFile || playbackState == "error")
            return
        // Decoded-frame metadata may arrive before FILE_LOADED or before a failed VO exists.
        val gamma = MpvColorPolicy.metadata(safePropertyString("video-params/gamma"))
        val profile = MpvColorPolicy.dolbyProfile(safePropertyString("current-tracks/video/dolby-vision-profile"))
        if (gamma == null && profile == null)
            return
        sourceMetadataReady = true
        lastSourceColor = colorDiagnostics()
        safePropertyString("video-format")?.let { lastVideoFormat = it }
        safePropertyString("hwdec-current")?.let { lastHardwareDecoder = it }
    }

    fun hasActivePlayback(): Boolean = pendingLoad != null || playbackState == "loading" || playbackState == "playing"

    fun trackState(): MpvTrackState {
        requireInitialized()
        val tracks = mutableListOf<MpvTrack>()
        val count = MPVLib.getPropertyInt("track-list/count") ?: 0
        for (index in 0 until count) {
            val prefix = "track-list/$index"
            val kind = MPVLib.getPropertyString("$prefix/type") ?: continue
            if (kind != "audio" && kind != "sub")
                continue
            val id = MPVLib.getPropertyInt("$prefix/id") ?: continue
            tracks.add(MpvTrack(
                id = id.toLong(),
                kind = kind,
                language = MPVLib.getPropertyString("$prefix/lang"),
                title = MPVLib.getPropertyString("$prefix/title"),
                codec = MPVLib.getPropertyString("$prefix/codec"),
                channels = MPVLib.getPropertyInt("$prefix/demux-channel-count")?.toLong(),
                isDefault = MPVLib.getPropertyBoolean("$prefix/default") ?: false,
                selected = MPVLib.getPropertyBoolean("$prefix/selected") ?: false,
            ))
        }
        return MpvTrackState(
            tracks = tracks,
            currentSubtitle = tracks.firstOrNull { it.kind == "sub" && it.selected }?.id,
            currentAudio = tracks.firstOrNull { it.kind == "audio" && it.selected }?.id,
        )
    }

    fun playbackDiagnostics(): MpvPlaybackDiagnostics {
        capturePlaybackFacts()
        val logs = synchronized(rendererFailureLogs) { rendererFailureLogs.toList() } +
            synchronized(diagnosticLogs) { diagnosticLogs.toList() }
        return MpvPlaybackDiagnostics(
            state = playbackState,
            endReason = endReason,
            position = lastPosition,
            duration = lastDuration,
            lastEvent = lastPlaybackEvent,
            lastError = lastPlaybackError,
            fileLoaded = fileLoaded,
            videoFormat = lastVideoFormat,
            audioCodec = lastAudioCodec,
            voConfigured = safePropertyBoolean("vo-configured") ?: false,
            hardwareDecoder = lastHardwareDecoder,
            videoOutput = activeVideoOutput,
            videoOutputFallbackUsed = videoOutputFallbackUsed,
            playbackTransport = playbackTransport,
            fsrStatus = fsrStatus,
            fsrReason = fsrReason,
            color = colorDiagnostics(),
            logs = logs,
        )
    }

    override fun eventProperty(property: String) = Unit

    override fun eventProperty(property: String, value: Long) = Unit

    override fun eventProperty(property: String, value: Boolean) {
        if (property == "eof-reached" && !value && fileLoaded && endReason == "eof") {
            endReason = null
            playbackState = "playing"
            lastPlaybackEvent = "playback-resumed-from-eof"
            refreshColorPolicy()
        }
        if (property == "eof-reached" && value && fileLoaded && playbackState == "playing"
            && !stopRequested && !rendererTransitionPending && lastPlaybackError == null
            && safePropertyBoolean("vo-configured") == true) {
            capturePlaybackFacts()
            // keep-open retains the final frame; it does not issue END_FILE until stopped.
            endReason = "eof"
            playbackState = "ended"
            lastPlaybackEvent = "natural-eof"
            hdrCapabilities?.requestWindowHdr(false)
        }
        if (property == "vo-configured" && value) {
            confirmRendererTransition()
            if (playbackState != "error") lastPlaybackEvent = "video-output-ready"
            refreshColorPolicy()
        }
    }

    override fun eventProperty(property: String, value: String) {
        when (property) {
            "video-format" -> if (value.isNotBlank()) lastPlaybackEvent = "video-format-ready"
            "audio-codec-name" -> if (value.isNotBlank()) lastPlaybackEvent = "audio-format-ready"
            "video-params/gamma", "current-tracks/video/dolby-vision-profile" -> {
                captureSourceMetadata()
                if (sourceMetadataReady && fileLoaded) refreshColorPolicy()
            }
        }
    }

    override fun eventProperty(property: String, value: Double) = Unit

    override fun eventHook(id: Long) {
        try {
            if (!loadPrepared) {
                playbackGeneration += 1
                resetColorSession()
            }
            loadPrepared = false
            sourceMetadataReady = false
            fileLoaded = false
            if (activeVideoOutput == FALLBACK_ANDROID_VIDEO_OUTPUT) {
                // A bounded retry reloads the exact same already-confirmed SDR source.
                // START_FILE precedes on_load, so changing GPU options here would tear
                // down an active media chain. New/unknown sources must start gpu-next
                // through play()'s idle configuration or fail safely at this barrier.
                val transition = rendererTransition
                check(transition?.stage == "reloading"
                    && transition.generation == playbackGeneration
                    && transition.request === currentLoad
                    && transition.videoOutput == FALLBACK_ANDROID_VIDEO_OUTPUT
                    && confirmedSdrSource()) { "当前片源尚未确认可使用兼容 GPU 渲染。" }
            }
            // The native hook waits for this callback, then continues exactly once.
            check(clearManagedFsrShader(true))
            fsrStatus = if (fsrMode == "off") "disabled" else "bypassed"
            fsrReason = if (fsrMode == "off") null else "等待确认当前片源的色彩信息。"
        } catch (_: Throwable) {
            failRenderer("播放前无法清除旧的画面处理配置。")
        }
    }

    override fun event(eventId: Int) {
        when (eventId) {
            MPVLib.MpvEvent.START_FILE -> {
                if (playbackState == "error") return
                waitingForStartFile = false
                nativeFileActive = true
                endReason = null
                stopRequested = false
                playbackState = "loading"
                lastPlaybackEvent = "start-file"
                lastPlaybackError = null
                fileLoaded = false
                sourceMetadataReady = false
                hdrHintRequested = false
                clearManagedFsrShader()
                fsrStatus = if (fsrMode == "off") "disabled" else "bypassed"
                fsrReason = if (fsrMode == "off") null else "等待确认当前片源的色彩信息。"
            }
            MPVLib.MpvEvent.FILE_LOADED -> {
                if (waitingForStartFile || playbackState == "error")
                    return
                playbackState = if (rendererTransitionPending) "loading" else "playing"
                lastPlaybackEvent = "file-loaded"
                lastPlaybackError = null
                fileLoaded = true
                // VIDEO_RECONFIG supplies current decoded-frame metadata; old VO metadata
                // can still be unavailable or stale while FILE_LOADED is being delivered.
                sourceMetadataReady = false
                captureSourceMetadata()
                confirmRendererTransition()
                refreshColorPolicy()
            }
            MPVLib.MpvEvent.VIDEO_RECONFIG -> {
                if (playbackState != "error" && endReason == null) lastPlaybackEvent = "video-reconfig"
                captureSourceMetadata()
                if (fileLoaded && !waitingForStartFile && hasActivePlayback()) {
                    confirmRendererTransition()
                    capturePlaybackFacts()
                    refreshColorPolicy()
                }
            }
            MPVLib.MpvEvent.AUDIO_RECONFIG -> if (playbackState != "error" && endReason == null) {
                lastPlaybackEvent = "audio-reconfig"
                safePropertyString("audio-codec-name")?.let { lastAudioCodec = it }
            }
            MPVLib.MpvEvent.PLAYBACK_RESTART -> {
                if (fileLoaded && !waitingForStartFile && playbackState != "error" && endReason != "eof") {
                    playbackState = if (rendererTransitionPending) "loading" else "playing"
                    lastPlaybackEvent = "playback-restart"
                    captureSourceMetadata()
                    confirmRendererTransition()
                    capturePlaybackFacts()
                    refreshColorPolicy()
                }
            }
        }
    }

    override fun eventEndFile(reason: Int, error: Int) {
        nativeFileActive = false
        val transition = rendererTransition
        if (transition != null && transition.stage == "waiting-stop" && !stopRequested
            && playbackState != "error" && reason != MPVLib.MpvEndReason.QUIT) {
            fileLoaded = false
            sourceMetadataReady = false
            surfaceView?.post { finishRendererTransition(transition) }
            return
        }
        if (playbackState == "error") {
            endReason = "error"
        } else if (stopRequested || reason == MPVLib.MpvEndReason.STOP || reason == MPVLib.MpvEndReason.QUIT) {
            endReason = if (reason == MPVLib.MpvEndReason.QUIT) "quit" else "stop"
            playbackState = "idle"
            lastPlaybackEvent = "stopped"
        } else if (reason == MPVLib.MpvEndReason.REDIRECT) {
            endReason = "redirect"
            playbackState = "loading"
            lastPlaybackEvent = "playlist-redirect"
        } else if (reason == MPVLib.MpvEndReason.EOF && fileLoaded && lastPlaybackError == null) {
            endReason = "eof"
            playbackState = "ended"
            lastPlaybackEvent = "natural-eof"
        } else {
            endReason = "error"
            playbackState = "error"
            lastPlaybackEvent = "end-file-error"
            lastPlaybackError = lastPlaybackError ?: "媒体加载失败（mpv 结束原因 $reason，错误 $error）。"
        }
        stopRequested = false
        fileLoaded = false
        sourceMetadataReady = false
        waitingForStartFile = false
        loadPrepared = false
        rendererTransition = null
        rendererTransitionPending = false
        hdrHintRequested = false
        hdrCapabilities?.requestWindowHdr(false)
        clearManagedFsrShader()
    }

    override fun logMessage(prefix: String, level: Int, text: String) {
        val line = sanitizeDiagnosticLine(prefix, text)
        if (line.isBlank())
            return
        synchronized(diagnosticLogs) {
            diagnosticLogs.addLast(line)
            while (diagnosticLogs.size > MAX_DIAGNOSTIC_LOGS)
                diagnosticLogs.removeFirst()
        }
        if (level <= 20 && playbackState == "loading")
            lastPlaybackError = line
        if (shouldFallbackFsr(level, line))
            recordFsrFallback("FSR Shader 编译失败，已恢复普通缩放。")
        if (shouldFallbackVideoOutput(prefix, level, line)) {
            captureSourceMetadata()
            synchronized(rendererFailureLogs) {
                if (line !in rendererFailureLogs) rendererFailureLogs.addLast(line)
                while (rendererFailureLogs.size > 4) rendererFailureLogs.removeFirst()
            }
            if (activeGpuContext == "androidvk" && !vulkanFallbackUsed) {
                vulkanFallbackUsed = true
                rendererFallbackReason = "Vulkan 渲染或 HDR Surface 协商失败，已改用 GLES 的 SDR 映射。"
                requestRendererTransition("android", PRIMARY_ANDROID_VIDEO_OUTPUT)
            } else if (!legacyFallbackUsed && confirmedSdrSource()) {
                legacyFallbackUsed = true
                rendererFallbackReason = "gpu-next 初始化失败；已确认 SDR 片源，改用兼容 GPU 渲染。"
                requestRendererTransition("android", FALLBACK_ANDROID_VIDEO_OUTPUT)
            } else {
                failRenderer("gpu-next 渲染失败；当前片源无法安全切换到兼容 GPU 渲染。")
            }
        }
    }

    private fun resetColorSession() {
        sourceMetadataReady = false
        rendererFallbackReason = null
        vulkanFallbackUsed = false
        legacyFallbackUsed = false
        rendererTransitionPending = false
        rendererTransition = null
        hdrHintRequested = false
        fsrFailedForPlayback = false
        videoOutputFallbackUsed = false
        if (initialized)
            runCatching { setRenderProperty("target-colorspace-hint", "no") }
                .onFailure { rendererNeedsConfiguration = true }
        hdrCapabilities?.requestWindowHdr(false)
    }

    private fun sourceGamma(): String? = if (sourceMetadataReady)
        MpvColorPolicy.metadata(safePropertyString("video-params/gamma")) else lastSourceColor?.sourceGamma

    private fun sourceDolbyProfile(): Int? = if (sourceMetadataReady)
        MpvColorPolicy.dolbyProfile(safePropertyString("current-tracks/video/dolby-vision-profile")) else lastSourceColor?.dolbyVisionProfile

    private fun confirmedSdrSource(): Boolean = (sourceMetadataReady || lastSourceColor != null)
        && MpvColorPolicy.confirmedSdr(sourceGamma(), sourceDolbyProfile())

    private fun effectiveHardwareDecoder(): String =
        if (activeGpuContext == "androidvk" && hardwareDecoder != "no") "mediacodec-copy" else hardwareDecoder

    private fun setRenderProperty(name: String, value: String) {
        check(MPVLib.setPropertyStringChecked(name, value) >= 0) { "原生渲染配置被拒绝：$name" }
    }

    private fun configureRenderer(context: String, videoOutput: String) {
        if (!initialized)
            return
        val changed = activeGpuContext != context || activeVideoOutput != videoOutput
        if (changed) {
            // Both GPU options have UPDATE_VO in pinned mpv. Never change them on a live file.
            check(!nativeFileActive) { "不能在仍活动的媒体链上更换 GPU 上下文。" }
            rendererNeedsConfiguration = true
            setRenderProperty("vo", "null")
            setRenderProperty("gpu-api", if (context == "androidvk") "vulkan" else "opengl")
            setRenderProperty("gpu-context", context)
            setRenderProperty("hwdec", if (context == "androidvk" && hardwareDecoder != "no") "mediacodec-copy" else hardwareDecoder)
            setRenderProperty("target-colorspace-hint", "no")
            setRenderProperty("target-trc", "auto")
            setRenderProperty("target-prim", "auto")
            activeGpuContext = context
            activeVideoOutput = videoOutput
        }
        if (rendererNeedsConfiguration && surfaceAttached) {
            setRenderProperty("vo", videoOutput)
            rendererNeedsConfiguration = !surfaceAttached
        }
    }

    private fun failRenderer(reason: String) {
        captureSourceMetadata()
        capturePlaybackFacts()
        playbackState = "error"
        endReason = "error"
        lastPlaybackEvent = "render-error"
        lastPlaybackError = reason
        pendingLoad = null
        fileLoaded = false
        sourceMetadataReady = false
        rendererTransition = null
        rendererTransitionPending = false
        hdrHintRequested = false
        rendererNeedsConfiguration = true
        managedFsrAttached = false
        managedFsrOptions = null
        hdrCapabilities?.requestWindowHdr(false)
        if (initialized) {
            runCatching { MPVLib.stopAsync() }
        }
    }

    private fun recoverRendererFailure(context: String) {
        if (context == "androidvk" && !vulkanFallbackUsed) {
            vulkanFallbackUsed = true
            videoOutputFallbackUsed = true
            rendererFallbackReason = "Vulkan 初始化被拒绝，已改用 GLES 的 SDR 映射。"
            runCatching { configureRenderer("android", PRIMARY_ANDROID_VIDEO_OUTPUT) }
                .onFailure { failRenderer("GLES 兼容渲染初始化失败。") }
        } else if (confirmedSdrSource() && !legacyFallbackUsed) {
            legacyFallbackUsed = true
            videoOutputFallbackUsed = true
            rendererFallbackReason = "已确认 SDR 片源，改用兼容 GPU 渲染。"
            runCatching { configureRenderer("android", FALLBACK_ANDROID_VIDEO_OUTPUT) }
                .onFailure { failRenderer("兼容 GPU 渲染初始化失败。") }
        } else {
            failRenderer("渲染初始化失败；当前片源无法安全切换到兼容 GPU 渲染。")
        }
    }

    private fun requestRendererTransition(context: String, videoOutput: String, fallback: Boolean = true) {
        if (rendererTransition?.stage == "waiting-stop" || !hasActivePlayback())
            return
        capturePlaybackFacts()
        val oldTransition = rendererTransition
        beginRendererTransition(context, videoOutput, fallback,
            oldTransition?.position ?: lastPosition,
            oldTransition?.paused ?: (safePropertyBoolean("pause") ?: false), oldTransition)
    }

    private fun beginRendererTransition(context: String, videoOutput: String, fallback: Boolean, position: Double, paused: Boolean,
        previous: RendererTransition? = null, preserveTracks: Boolean = true) {
        val surface = surfaceView ?: return
        val request = currentLoad ?: return
        val transition = RendererTransition(context, videoOutput, fallback, request, position, paused,
            if (preserveTracks) previous?.selection ?: currentTrackSelection() else RendererTrackSelection(null, null, null),
            previous?.settings ?: listOf("speed", "volume", "mute", "sub-delay", "video-zoom", "panscan", "brightness")
                .mapNotNull { name -> safePropertyString(name)?.let { name to it } }.toMap().toMutableMap(),
            playbackGeneration)
        rendererTransition = transition
        rendererTransitionPending = true
        playbackState = "loading"
        endReason = null
        lastPlaybackError = null
        lastPlaybackEvent = "renderer-transition"
        hdrHintRequested = false
        hdrCapabilities?.requestWindowHdr(false)
        if (nativeFileActive) {
            if (MPVLib.stopAsync() < 0) failRenderer("渲染切换未能停止旧的媒体链。")
        } else {
            surface.post { finishRendererTransition(transition) }
        }
        surface.postDelayed({
            if (rendererTransition === transition && transition.generation == playbackGeneration)
                failRenderer("渲染切换超时，请打开播放诊断查看原生错误。")
        }, RENDERER_TRANSITION_TIMEOUT_MS)
    }

    @Synchronized
    private fun finishRendererTransition(transition: RendererTransition) {
        if (rendererTransition !== transition || transition.generation != playbackGeneration
            || !initialized || !surfaceAttached || playbackState == "error" || nativeFileActive)
            return
        if (!clearManagedFsrShader()) return
        runCatching { configureRenderer(transition.context, transition.videoOutput) }
            .onFailure { recoverRendererFailure(transition.context) }
        if (playbackState == "error") return
        transition.context = activeGpuContext
        transition.videoOutput = activeVideoOutput
        transition.stage = "reloading"
        pendingRendererResume = transition.position.takeIf { it > 0.0 }
        fileLoaded = false
        sourceMetadataReady = false
        waitingForStartFile = true
        loadPrepared = true
        playbackTransport = if (transition.request.path.startsWith("http://127.0.0.1:")) "rust-loopback" else "direct"
        runCatching { loadNativeFile(transition.request, transition.position, transition.paused) }
            .onFailure { failRenderer("渲染切换后的媒体重新加载失败。") }
    }

    private fun confirmRendererTransition() {
        val transition = rendererTransition ?: return
        if (transition.stage != "reloading" || !fileLoaded || !sourceMetadataReady || !surfaceAttached
            || safePropertyBoolean("vo-configured") != true
            || safePropertyString("current-vo") != transition.videoOutput)
            return
        val restoredPosition = MPVLib.getPropertyDouble("time-pos")
            ?.takeIf { it.isFinite() && it >= 0.0 }
        if (transition.position > 0.0
            && (restoredPosition == null || kotlin.math.abs(restoredPosition - transition.position) > 5.0))
            return
        // FILE_LOADED/VIDEO_RECONFIG may precede the seek. Keep the bounded
        // transition pending until native playback proves the saved position.
        restoredPosition?.let { lastPosition = it }
        pendingRendererResume = null
        rendererTransition = null
        rendererTransitionPending = false
        playbackState = "playing"
        if (transition.fallback) videoOutputFallbackUsed = true
        lastPlaybackEvent = if (transition.fallback) "video-output-fallback" else "video-output-selected"
        for (subtitle in externalSubtitles.toList()) {
            runCatching {
                MPVLib.commandChecked(arrayOf("sub-add", subtitle.url, "auto", subtitle.title ?: "", subtitle.language ?: ""))
            }
        }
        val selected = transition.selection
        selected.audio?.let { MPVLib.setPropertyString("aid", it) }
        val subtitleId = selected.externalSubtitleUrl?.let { trackIdForExternalSubtitle(it) } ?: selected.subtitle
        subtitleId?.let { MPVLib.setPropertyString("sid", it) }
        for ((name, value) in transition.settings) MPVLib.setPropertyString(name, value)
        refreshColorPolicy()
    }

    private fun currentTrackSelection(): RendererTrackSelection {
        val subtitle = safePropertyString("sid")
        val count = MPVLib.getPropertyInt("track-list/count") ?: 0
        val external = (0 until count).firstNotNullOfOrNull { index ->
            val prefix = "track-list/$index"
            if (safePropertyString("$prefix/type") == "sub" && safePropertyString("$prefix/id") == subtitle)
                safePropertyString("$prefix/external-filename") else null
        }
        return RendererTrackSelection(safePropertyString("aid"), subtitle, external)
    }

    private fun trackIdForExternalSubtitle(url: String): String? {
        val count = MPVLib.getPropertyInt("track-list/count") ?: 0
        return (0 until count).firstNotNullOfOrNull { index ->
            val prefix = "track-list/$index"
            if (safePropertyString("$prefix/type") == "sub" && safePropertyString("$prefix/external-filename") == url)
                safePropertyString("$prefix/id") else null
        }
    }

    private fun refreshColorPolicy() {
        if (!initialized || rendererTransitionPending || playbackState == "error")
            return
        val gamma = sourceGamma()
        val sdr = confirmedSdrSource()
        if (sourceMetadataReady && activeVideoOutput == FALLBACK_ANDROID_VIDEO_OUTPUT && !sdr) {
            // Re-identification or stream changes may invalidate an earlier SDR decision.
            requestRendererTransition("android", PRIMARY_ANDROID_VIDEO_OUTPUT, false)
            return
        }
        val canUseHdr = sourceMetadataReady && hdrCapabilities?.canTryVulkan() == true
            && hdrCapabilities?.supports(gamma) == true && !vulkanFallbackUsed
            && preferredVideoOutput == PRIMARY_ANDROID_VIDEO_OUTPUT
        if (canUseHdr && activeGpuContext != "androidvk") {
            requestRendererTransition("androidvk", PRIMARY_ANDROID_VIDEO_OUTPUT, false)
            return
        }
        if (sourceMetadataReady && MpvColorPolicy.hdrTransfer(gamma)
            && activeGpuContext == "androidvk" && !canUseHdr) {
            requestRendererTransition("android", PRIMARY_ANDROID_VIDEO_OUTPUT, false)
            return
        }
        val requestHdr = sourceMetadataReady && activeGpuContext == "androidvk"
            && activeVideoOutput == PRIMARY_ANDROID_VIDEO_OUTPUT && !vulkanFallbackUsed
            && hdrCapabilities?.supports(gamma) == true
        if (requestHdr != hdrHintRequested) {
            hdrHintRequested = requestHdr
            hdrCapabilities?.requestWindowHdr(requestHdr)
            runCatching {
                setRenderProperty("target-colorspace-hint-mode", "source")
                setRenderProperty("target-colorspace-hint-strict", "yes")
                setRenderProperty("target-trc", "auto")
                setRenderProperty("target-prim", "auto")
                setRenderProperty("target-colorspace-hint", if (requestHdr) "yes" else "no")
            }.onFailure {
                vulkanFallbackUsed = true
                rendererFallbackReason = "HDR 色彩协商不可用，已改用 GLES 的 SDR 映射。"
                requestRendererTransition("android", PRIMARY_ANDROID_VIDEO_OUTPUT)
            }
        }
        if (sourceMetadataReady && sdr && !legacyFallbackUsed
            && preferredVideoOutput == FALLBACK_ANDROID_VIDEO_OUTPUT && activeVideoOutput != FALLBACK_ANDROID_VIDEO_OUTPUT) {
            requestRendererTransition("android", FALLBACK_ANDROID_VIDEO_OUTPUT, false)
            return
        }
        if (sdr && activeGpuContext == "androidvk") {
            // Ordinary SDR retains the cheaper zero-copy GLES path after source identification.
            requestRendererTransition("android", activeVideoOutput, false)
            return
        }
        applyManagedFsrShader()
    }

    private fun colorDiagnostics(): MpvColorDiagnostics {
        val retainedSource = lastSourceColor
        if (!sourceMetadataReady && retainedSource != null) {
            return retainedSource.copy(targetGamma = null, targetPrimaries = null, targetPixelFormat = null,
                outputMode = "unknown", gpuContext = null,
                reason = rendererFallbackReason ?: if (playbackState == "error")
                    "保留故障前确认的片源信息；渲染目标当前不可用。" else "渲染目标正在重新建立。")
        }
        fun metadata(name: String): String? = MpvColorPolicy.metadata(safePropertyString(name))
        val targetReady = hasActivePlayback() && sourceMetadataReady && surfaceAttached && !rendererTransitionPending
            && safePropertyBoolean("vo-configured") == true
        val gamma = if (targetReady) metadata("video-target-params/gamma") else null
        val transfer = MpvColorPolicy.transfer(gamma)
        val outputMode = when {
            !targetReady || transfer == null -> "unknown"
            MpvColorPolicy.hdrTransfer(gamma) -> if (activeGpuContext == "androidvk"
                && hdrCapabilities?.supports(gamma) == true) "hdr" else "unknown"
            MpvColorPolicy.confirmedSdr(gamma, null) -> "sdr"
            else -> "unknown"
        }
        val reason = rendererFallbackReason ?: when {
            !hasActivePlayback() -> null
            activeGpuContext == "androidvk" && outputMode == "hdr" ->
                "已观察到 Vulkan HDR 渲染目标；使用 MediaCodec copy 或软件解码，屏幕实际 HDR 呈现仍需设备验证。"
            activeGpuContext == "androidvk" ->
                "Vulkan 使用 MediaCodec copy 或软件解码；当前尚未确认 HDR 渲染目标。"
            MpvColorPolicy.hdrTransfer(sourceGamma()) ->
                "当前使用 GLES SDR 映射；未启用原生 HDR 或杜比视界显示模式。"
            else -> null
        }
        return MpvColorDiagnostics(
            sourceGamma = sourceGamma(),
            sourcePrimaries = if (sourceMetadataReady) metadata("video-params/primaries") else null,
            sourcePixelFormat = if (sourceMetadataReady) metadata("video-params/pixelformat") else null,
            sourceHardwarePixelFormat = if (sourceMetadataReady) metadata("video-params/hw-pixelformat") else null,
            dolbyVisionProfile = sourceDolbyProfile(),
            targetGamma = gamma,
            targetPrimaries = if (targetReady) metadata("video-target-params/primaries") else null,
            targetPixelFormat = if (targetReady) metadata("video-target-params/pixelformat") else null,
            outputMode = outputMode,
            gpuContext = if (targetReady) activeGpuContext else null,
            displayHdrTypes = hdrCapabilities?.hdrTypes(),
            reason = reason,
        )
    }

    private fun safePropertyString(name: String): String? {
        if (!initialized)
            return null
        return runCatching { MPVLib.getPropertyString(name) }
            .getOrNull()
            ?.trim()
            ?.takeIf { it.isNotEmpty() && it != "unknown" }
    }

    private fun applyManagedFsrShader() {
        if (!initialized)
            return
        if (fsrMode == "off") {
            if (!clearManagedFsrShader())
                return
            fsrStatus = "disabled"
            fsrReason = null
            return
        }
        if (!confirmedSdrSource()) {
            if (!clearManagedFsrShader())
                return
            fsrStatus = "bypassed"
            fsrReason = if (!sourceMetadataReady || sourceGamma() == null)
                "片源色彩信息尚未确认；当前 FSR 仅适用于 SDR。"
            else "HDR 与杜比视界使用原生色彩映射，不启用当前 FSR。"
            return
        }
        if (fsrFailedForPlayback)
            return
        val shader = fsrShaderFile
        if (shader == null || !shader.isFile) {
            recordFsrFallback("应用内置 FSR Shader 文件不可用。")
            return
        }

        val options = fsrShaderOptions()
        runCatching {
            if (managedFsrOptions != options) {
                setRenderProperty("glsl-shader-opts", options)
                managedFsrOptions = options
            }
            if (!managedFsrAttached) {
                check(MPVLib.commandChecked(arrayOf("change-list", "glsl-shaders", "append", shader.absolutePath)) >= 0)
                managedFsrAttached = true
            }
        }.onSuccess {
            fsrStatus = if (fsrMode == "force") "armed-force" else "armed-auto"
            fsrReason = "仅在输出尺寸大于源画面时触发。"
        }.onFailure {
            recordFsrFallback("FSR Shader 加载失败，已恢复普通缩放。")
        }
    }

    private fun refreshFsrTargetParameters() {
        if (!initialized || fsrMode == "off" || !fsrStatus.startsWith("armed"))
            return
        val options = fsrShaderOptions()
        if (managedFsrOptions == options)
            return
        runCatching {
            setRenderProperty("glsl-shader-opts", options)
            managedFsrOptions = options
        }
            .onFailure { recordFsrFallback("FSR 目标分辨率更新失败，已恢复普通缩放。") }
    }

    private fun fsrShaderOptions(): String {
        val sharpnessStops = 2.0 * (1.0 - fsrSharpness / 100.0)
        val (targetWidth, targetHeight) = fsrTargetDimensions()
        return "OHMYCINE_SHARPNESS=${"%.3f".format(java.util.Locale.ROOT, sharpnessStops)}," +
            "OHMYCINE_DENOISE=${if (fsrDenoise) 1 else 0}," +
            "OHMYCINE_TARGET_WIDTH=$targetWidth,OHMYCINE_TARGET_HEIGHT=$targetHeight"
    }

    private fun fsrTargetDimensions(): Pair<Int, Int> {
        val shortEdgeCap = when (fsrTarget) {
            "1080p" -> 1080
            "1440p" -> 1440
            "2160p" -> 2160
            else -> return 16384 to 16384
        }
        if (surfaceWidth <= 0 || surfaceHeight <= 0)
            return 16384 to 16384
        val shortEdge = minOf(surfaceWidth, surfaceHeight)
        if (shortEdge <= shortEdgeCap)
            return surfaceWidth to surfaceHeight
        val scale = shortEdgeCap.toDouble() / shortEdge.toDouble()
        return (surfaceWidth * scale).toInt().coerceAtLeast(1) to
            (surfaceHeight * scale).toInt().coerceAtLeast(1)
    }

    private fun clearManagedFsrShader(force: Boolean = false): Boolean {
        if (!initialized || (!force && !managedFsrAttached))
            return true
        if (runCatching { MPVLib.setPropertyStringChecked("glsl-shaders", "") }.getOrDefault(-1) < 0) {
            failRenderer("旧的 FSR 配置无法清除，已停止渲染以保护 HDR 画面。")
            return false
        }
        managedFsrAttached = false
        managedFsrOptions = null
        return true
    }

    private fun recordFsrFallback(reason: String) {
        fsrFailedForPlayback = true
        if (!clearManagedFsrShader())
            return
        fsrStatus = "fallback"
        fsrReason = reason
        lastPlaybackEvent = "fsr-fallback"
    }

    private fun shouldFallbackFsr(level: Int, line: String): Boolean {
        if (level > 20 || !fsrStatus.startsWith("armed"))
            return false
        val normalized = line.lowercase()
        val namesShader = normalized.contains("shader") || normalized.contains("glsl") || normalized.contains("hook")
        val reportsFailure = normalized.contains("error") || normalized.contains("failed") || normalized.contains("compile") || normalized.contains("invalid")
        return namesShader && reportsFailure
    }

    private fun safePropertyBoolean(name: String): Boolean? {
        if (!initialized)
            return null
        return runCatching { MPVLib.getPropertyBoolean(name) }.getOrNull()
    }

    private fun sanitizeDiagnosticLine(prefix: String, text: String): String {
        val cleanPrefix = prefix.replace(Regex("[^A-Za-z0-9_.-]"), "").take(32)
        val cleanText = text
            .replace(Regex("(?i)https?://\\S+"), "[remote-media]")
            .replace(Regex("(?i)(?:file|content)://\\S+"), "[local-media]")
            .replace(Regex("(?i)(?<![A-Za-z0-9])/(?:storage|sdcard|data|mnt)/\\S+"), "[local-media]")
            .replace(Regex("(?i)(authorization|cookie|api[_-]?key|token|x-emby-token)\\s*[:=]\\s*\\S+"), "$1=[redacted]")
            .replace(Regex("(?i)([?&](?:api[_-]?key|token|auth|signature|sig)=)[^&\\s]+"), "$1[redacted]")
            .replace(Regex("[\\r\\n\\t]+"), " ")
            .trim()
            .take(360)
        return if (cleanPrefix.isBlank()) cleanText else "$cleanPrefix: $cleanText"
    }

    private fun shouldFallbackVideoOutput(prefix: String, level: Int, line: String): Boolean {
        if (level > 20 || !surfaceAttached || rendererTransition?.stage == "waiting-stop" || !hasActivePlayback())
            return false
        val normalized = line.lowercase()
        val owner = prefix.lowercase()
        val rendererLog = owner == "vo" || owner.startsWith("vo/") || owner == "libplacebo"
            || owner == "vulkan" || normalized.contains("error opening/initializing the selected video_out")
        return activeVideoOutput == PRIMARY_ANDROID_VIDEO_OUTPUT
            && rendererLog && !shouldFallbackFsr(level, line)
            && (normalized.contains("fail") || normalized.contains("error") || normalized.contains("not supported"))
    }

    private fun registerMpvObservers() {
        MPVLib.addObserver(this)
        MPVLib.addLogObserver(this)
    }

    private fun unregisterMpvObservers() {
        MPVLib.removeObserver(this)
        MPVLib.removeLogObserver(this)
    }

    private fun requireInitialized() {
        check(isReady()) { "Android 播放器表面尚未准备完成。" }
    }

    private class OhMyCineMpvSurfaceView(context: Context) : SurfaceView(context), SurfaceHolder.Callback {
        private var destroyed = false

        fun initialize() {
            if (!initialized) {
                check(hdrCapabilities?.runtimeCompatible == true) { "Android 播放运行时缺少色彩处理屏障，请重新打包应用。" }
                val caFile = installCaCertificate(context)
                fsrShaderFile = runCatching { installFsrShader(context) }
                    .onFailure {
                        fsrStatus = "fallback"
                        fsrReason = "应用内置 FSR Shader 安装失败。"
                    }
                    .getOrNull()
                MPVLib.create(context.applicationContext)
                MpvSurfaceHost.registerMpvObservers()
                MPVLib.setOptionString("config", "no")
                MPVLib.setOptionString("profile", "fast")
                // Keep fast scaling, but restore HDR peak analysis and output dithering.
                MPVLib.setOptionString("hdr-compute-peak", "auto")
                MPVLib.setOptionString("dither-depth", "auto")
                MPVLib.setOptionString("dither", "fruit")
                MPVLib.setOptionString("vo", PRIMARY_ANDROID_VIDEO_OUTPUT)
                MPVLib.setOptionString("gpu-api", "opengl")
                MPVLib.setOptionString("gpu-context", "android")
                MPVLib.setOptionString("opengl-es", "yes")
                MPVLib.setOptionString("hwdec", hardwareDecoder)
                MPVLib.setOptionString("hwdec-codecs", "h264,hevc,mpeg4,mpeg2video,vp8,vp9,av1")
                MPVLib.setOptionString("ao", "audiotrack,opensles")
                MPVLib.setOptionString("audio-set-media-role", "yes")
                MPVLib.setOptionString("osc", "no")
                MPVLib.setOptionString("keep-open", "yes")
                MPVLib.setOptionString("idle", "yes")
                MPVLib.setOptionString("force-window", "no")
                MPVLib.setOptionString("input-default-bindings", "no")
                MPVLib.setOptionString("cache", cacheMode)
                MPVLib.setOptionString("demuxer-max-bytes", demuxerMaxBytes.toString())
                MPVLib.setOptionString("demuxer-max-back-bytes", (32 * 1024 * 1024).toString())
                MPVLib.setOptionString("video-sync", videoSync)
                MPVLib.setOptionString("target-colorspace-hint", "no")
                MPVLib.setOptionString("target-colorspace-hint-mode", "source")
                MPVLib.setOptionString("target-colorspace-hint-strict", "yes")
                MPVLib.setOptionString("gpu-shader-cache-dir", context.cacheDir.path)
                MPVLib.setOptionString("icc-cache-dir", context.cacheDir.path)
                MPVLib.setOptionString("tls-verify", "yes")
                MPVLib.setOptionString("tls-ca-file", caFile.path)
                MPVLib.init()
                MPVLib.observeProperty("video-format", MPVLib.MpvFormat.STRING)
                MPVLib.observeProperty("audio-codec-name", MPVLib.MpvFormat.STRING)
                MPVLib.observeProperty("vo-configured", MPVLib.MpvFormat.FLAG)
                MPVLib.observeProperty("eof-reached", MPVLib.MpvFormat.FLAG)
                MPVLib.observeProperty("hwdec-current", MPVLib.MpvFormat.STRING)
                MPVLib.observeProperty("video-params/gamma", MPVLib.MpvFormat.STRING)
                MPVLib.observeProperty("current-tracks/video/dolby-vision-profile", MPVLib.MpvFormat.STRING)
                initialized = true
                check(MPVLib.addLoadHook() >= 0) { "播放前画面处理屏障不可用。" }
                lastPlaybackEvent = "mpv-initialized"
                applyManagedFsrShader()
            }
            setZOrderMediaOverlay(false)
            holder.setFormat(PixelFormat.OPAQUE)
            holder.addCallback(this)
        }

        fun destroy() {
            if (destroyed)
                return
            destroyed = true
            holder.removeCallback(this)
            if (surfaceAttached) {
                MPVLib.setPropertyString("vo", "null")
                MPVLib.detachSurface()
                surfaceAttached = false
            }
            if (initialized) {
                MpvSurfaceHost.unregisterMpvObservers()
                MPVLib.destroy()
                initialized = false
            }
        }

        override fun surfaceCreated(holder: SurfaceHolder) {
            MPVLib.attachSurface(holder.surface)
            MPVLib.setOptionString("force-window", "yes")
            surfaceAttached = true
            rendererNeedsConfiguration = true
            rendererTransition?.takeIf { it.stage == "waiting-stop" && !nativeFileActive }?.let {
                finishRendererTransition(it)
                return
            }
            runCatching { configureRenderer(activeGpuContext, activeVideoOutput) }
                .onFailure { recoverRendererFailure(activeGpuContext) }
            lastPlaybackEvent = "surface-attached"
            pendingLoad?.let { play(it) }
            refreshColorPolicy()
        }

        override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
            surfaceWidth = width.coerceAtLeast(0)
            surfaceHeight = height.coerceAtLeast(0)
            MPVLib.setPropertyString("android-surface-size", "${width}x$height")
            refreshFsrTargetParameters()
            refreshColorPolicy()
        }

        override fun surfaceDestroyed(holder: SurfaceHolder) {
            if (!surfaceAttached)
                return
            capturePlaybackFacts()
            surfaceAttached = false
            MPVLib.setPropertyString("vo", "null")
            MPVLib.setPropertyString("force-window", "no")
            MPVLib.detachSurface()
            rendererNeedsConfiguration = true
            lastPlaybackEvent = "surface-detached"
            hdrHintRequested = false
            clearManagedFsrShader()
        }
    }

    private fun installCaCertificate(context: Context): File {
        val directory = File(context.filesDir, "mpv")
        directory.mkdirs()
        val target = File(directory, "cacert.pem")
        if (!target.exists()) {
            context.assets.open("mpv/cacert.pem").use { input ->
                target.outputStream().use { output -> input.copyTo(output) }
            }
        }
        return target
    }

    private fun installFsrShader(context: Context): File {
        val directory = File(context.filesDir, "mpv")
        directory.mkdirs()
        val target = File(directory, "ohmycine-fsr-v1.glsl")
        context.assets.open(FSR_SHADER_ASSET).use { input ->
            target.outputStream().use { output -> input.copyTo(output) }
        }
        return target
    }
}

internal data class MpvHeader(val name: String, val value: String)
internal data class MpvEngineSettings(
    val videoOutput: String,
    val hardwareDecoder: String,
    val cacheMode: String,
    val demuxerMaxBytesMb: Int,
    val videoSync: String,
    val fsrMode: String,
    val fsrSharpness: Double,
    val fsrDenoise: Boolean,
    val fsrTarget: String,
)
internal data class PendingLoad(val path: String, val audioPath: String?, val headers: List<MpvHeader>)
internal data class MpvSnapshot(val time: Double, val duration: Double, val paused: Boolean)
internal data class MpvTrack(
    val id: Long,
    val kind: String,
    val language: String?,
    val title: String?,
    val codec: String?,
    val channels: Long?,
    val isDefault: Boolean,
    val selected: Boolean,
)
internal data class MpvTrackState(
    val tracks: List<MpvTrack>,
    val currentSubtitle: Long?,
    val currentAudio: Long?,
)
internal data class MpvPlaybackDiagnostics(
    val state: String,
    val endReason: String?,
    val position: Double,
    val duration: Double,
    val lastEvent: String,
    val lastError: String?,
    val fileLoaded: Boolean,
    val videoFormat: String?,
    val audioCodec: String?,
    val voConfigured: Boolean,
    val hardwareDecoder: String?,
    val videoOutput: String,
    val videoOutputFallbackUsed: Boolean,
    val playbackTransport: String,
    val fsrStatus: String,
    val fsrReason: String?,
    val color: MpvColorDiagnostics?,
    val logs: List<String>,
)

private data class ExternalSubtitle(val url: String, val title: String?, val language: String?)
private data class RendererTrackSelection(var audio: String?, var subtitle: String?, var externalSubtitleUrl: String?)
private data class RendererTransition(
    var context: String,
    var videoOutput: String,
    val fallback: Boolean,
    val request: PendingLoad,
    val position: Double,
    var paused: Boolean,
    val selection: RendererTrackSelection,
    val settings: MutableMap<String, String>,
    val generation: Long,
    var stage: String = "waiting-stop",
)
