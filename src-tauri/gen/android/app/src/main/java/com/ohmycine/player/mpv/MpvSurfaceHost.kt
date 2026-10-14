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
    private const val LEGACY_ANDROID_VIDEO_OUTPUT = "gpu"
    private const val MAX_DIAGNOSTIC_LOGS = 24
    private const val MAX_STARTUP_DIAGNOSTIC_LOGS = 12
    private const val MAX_PENDING_NATIVE_CALLBACKS = 128
    private const val FSR_SHADER_ASSET = "mpv/ohmycine-fsr-v1.glsl"
    private const val RENDERER_TRANSITION_TIMEOUT_MS = 10_000L
    private const val VIDEO_OUTPUT_READY_TIMEOUT_MS = 10_000L
    private const val HDR_TARGET_SETTLE_MS = 3_000L
    private const val HDR_TARGET_OBSERVATIONS = 4

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
    private var rendererTransitionPending = false
    @Volatile
    private var rendererTransition: RendererTransition? = null
    @Volatile
    private var nativeFileActive = false
    private var currentLoad: PendingLoad? = null
    private val externalSubtitles = mutableListOf<ExternalSubtitle>()
    private val startupDiagnosticLogs = ArrayDeque<String>()
    private val attemptStateLock = Any()
    @Volatile
    private var nativeEntryId: Long? = null
    @Volatile
    private var closingEntryId: Long? = null
    private var bindingEntryGeneration: Long? = null
    private val pendingNativeCallbacks = ArrayDeque<NativeCallback>()
    private var nativeCallbacksDraining = false
    private var nativeCallbackOverflowGeneration: Long? = null
    @Volatile
    private var nativeLoadIssued = false
    @Volatile
    private var nativeAttemptStarted = false
    @Volatile
    private var lastEglFailure: String? = null
    @Volatile
    private var nativeVideoPresented = false
    @Volatile
    private var expectsVideo = false
    @Volatile
    private var videoOutputReadinessGeneration = 0L
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
    private var requestedTargetTransfer: String? = null
    @Volatile
    private var hdrTargetObservation: HdrTargetObservation? = null
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
    private var hardwareDecoder = "mediacodec"
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
        synchronized(attemptStateLock) {
            playbackGeneration += 1
            nativeEntryId = null
            closingEntryId = null
            bindingEntryGeneration = null
            pendingNativeCallbacks.clear()
            nativeCallbackOverflowGeneration = null
            nativeLoadIssued = false
            nativeAttemptStarted = false
        }
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
        hardwareDecoder = "mediacodec"
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
        val replacingNativeFile = synchronized(attemptStateLock) {
            val replacing = nativeFileActive
            closingEntryId = nativeEntryId.takeIf { replacing }
            nativeEntryId = null
            bindingEntryGeneration = null
            pendingNativeCallbacks.clear()
            nativeCallbackOverflowGeneration = null
            currentLoad = request
            nativeLoadIssued = false
            nativeAttemptStarted = false
            waitingForStartFile = true
            stopRequested = false
            playbackGeneration += 1
            replacing
        }
        externalSubtitles.clear()
        clearPlaybackFacts()
        playbackState = "loading"
        lastPlaybackEvent = "load-command"
        lastPlaybackError = null
        fileLoaded = false
        stopRequested = false
        resetColorSession()
        loadPrepared = true
        waitingForStartFile = true
        check(clearManagedFsrShader(true)) { "无法移除旧的 FSR 配置。" }
        fsrStatus = if (fsrMode == "off") "disabled" else "bypassed"
        fsrReason = if (fsrMode == "off") null else "等待确认 SDR 片源；HDR 与杜比视界不使用当前 FSR。"
        val context = "android"
        if (replacingNativeFile) {
            beginRendererTransition(context, PRIMARY_ANDROID_VIDEO_OUTPUT, 0.0, false, preserveTracks = false)
            return
        }
        // Unknown/Dolby sources retain gpu-next even when the saved legacy preference is gpu.
        runCatching { configureRenderer(context, PRIMARY_ANDROID_VIDEO_OUTPUT) }
            .onFailure { failRenderer("${rendererName(context)} 渲染配置被拒绝，请打开播放诊断查看原因。") }
        check(playbackState != "error") { lastPlaybackError ?: "Android 渲染配置失败。" }
        playbackTransport = if (request.path.startsWith("http://127.0.0.1:")) "rust-loopback" else "direct"
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
        val generation = synchronized(attemptStateLock) {
            check(currentLoad === request) { "播放请求已经被替换。" }
            resetAttemptDiagnostics()
            nativeEntryId = null
            closingEntryId = null
            bindingEntryGeneration = playbackGeneration
            nativeLoadIssued = true
            nativeAttemptStarted = false
            waitingForStartFile = true
            lastPlaybackError = null
            playbackGeneration
        }
        // The command result owns the identity. Never infer it from whichever
        // START callback/property happens to arrive first while loadfile returns.
        // Do not hold attemptStateLock across a synchronous native call: on_load
        // must remain able to clear FSR and continue on the native event thread.
        val result = runCatching {
            MPVLib.loadFileChecked(arrayOf("loadfile", request.path, "replace", "-1", options.joinToString(",")))
        }
        val entryId = result.getOrNull()
        val bound = synchronized(attemptStateLock) {
            if (generation != playbackGeneration || currentLoad !== request
                || bindingEntryGeneration != generation) {
                false
            } else {
                bindingEntryGeneration = null
                if (entryId != null && entryId > 0) {
                    nativeEntryId = entryId
                    true
                } else {
                    nativeLoadIssued = false
                    false
                }
            }
        }
        if (!bound && generation == playbackGeneration)
            failRenderer("媒体加载命令被拒绝（mpv 错误 ${entryId ?: "unknown"}）。", generation)
        resumeNativeCallbacks()
        check(bound) { "媒体加载命令被拒绝。" }
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
            "software" -> "no"
            else -> "mediacodec"
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
            configureDecoder()
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
        pendingLoad = null
        synchronized(attemptStateLock) {
            playbackGeneration += 1
            currentLoad = null
            closingEntryId = nativeEntryId
            nativeEntryId = null
            bindingEntryGeneration = null
            pendingNativeCallbacks.clear()
            nativeCallbackOverflowGeneration = null
            nativeLoadIssued = false
            nativeAttemptStarted = false
        }
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
        nativeVideoPresented = false
        expectsVideo = false
        hdrTargetObservation = null
        videoOutputReadinessGeneration += 1
    }

    private fun resetAttemptDiagnostics() {
        synchronized(diagnosticLogs) { diagnosticLogs.clear() }
        synchronized(startupDiagnosticLogs) { startupDiagnosticLogs.clear() }
        lastEglFailure = null
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
        val logs = if (currentLoad != null && waitingForStartFile && !nativeAttemptStarted) emptyList() else
            (synchronized(startupDiagnosticLogs) { startupDiagnosticLogs.toList() } +
                synchronized(diagnosticLogs) { diagnosticLogs.toList() }).distinct()
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

    private data class NativeCallback(val entryId: Long, val apply: (Long) -> Unit)

    private fun dispatchNativeCallback(entryId: Long, apply: (Long) -> Unit) {
        if (entryId <= 0) return
        synchronized(attemptStateLock) {
            if (nativeCallbackOverflowGeneration != null) return
            if (pendingNativeCallbacks.size >= MAX_PENDING_NATIVE_CALLBACKS) {
                // Losing START/END silently would corrupt the attempt identity.
                // Abort the current bounded handoff instead; hooks still continue
                // exactly once in native code without waiting for this queue.
                pendingNativeCallbacks.clear()
                nativeCallbackOverflowGeneration = playbackGeneration
            } else {
                pendingNativeCallbacks.addLast(NativeCallback(entryId, apply))
            }
        }
        resumeNativeCallbacks()
    }

    private fun resumeNativeCallbacks() {
        val drain = synchronized(attemptStateLock) {
            if (bindingEntryGeneration != null || nativeCallbacksDraining
                || (pendingNativeCallbacks.isEmpty() && nativeCallbackOverflowGeneration == null)) false
            else { nativeCallbacksDraining = true; true }
        }
        if (!drain) return
        while (true) {
            val next = synchronized(attemptStateLock) {
                if (bindingEntryGeneration != null) {
                    nativeCallbacksDraining = false
                    return@synchronized null
                }
                nativeCallbackOverflowGeneration?.let { generation ->
                    nativeCallbackOverflowGeneration = null
                    return@synchronized NativeCallback(-1L) {
                        failRenderer("原生播放回调缓冲超过限制。", generation)
                    } to generation
                }
                while (pendingNativeCallbacks.isNotEmpty()) {
                    val callback = pendingNativeCallbacks.removeFirst()
                    if (callback.entryId == nativeEntryId || callback.entryId == closingEntryId)
                        return@synchronized callback to playbackGeneration
                }
                nativeCallbacksDraining = false
                null
            } ?: return
            runCatching { next.first.apply(next.second) }
                .onFailure { failRenderer("原生播放事件处理失败。", next.second) }
        }
    }

    private fun ownsNativeEntry(entryId: Long, generation: Long): Boolean =
        generation == playbackGeneration && entryId == nativeEntryId && nativeLoadIssued

    override fun eventProperty(property: String, entryId: Long) = Unit

    override fun eventProperty(property: String, value: Long, entryId: Long) = Unit

    override fun eventProperty(property: String, value: Boolean, entryId: Long) =
        dispatchNativeCallback(entryId) { handleBooleanProperty(property, value, entryId, it) }

    private fun handleBooleanProperty(property: String, value: Boolean, entryId: Long, generation: Long) {
        if (!ownsNativeEntry(entryId, generation) || !nativeAttemptStarted) return
        if (property == "eof-reached" && !value && fileLoaded && endReason == "eof") {
            synchronized(attemptStateLock) {
                if (!ownsNativeEntry(entryId, generation)) return
                endReason = null
                playbackState = "playing"
                lastPlaybackEvent = "playback-resumed-from-eof"
            }
            refreshColorPolicy()
        }
        if (property == "eof-reached" && value && fileLoaded && playbackState == "playing"
            && !stopRequested && !rendererTransitionPending && lastPlaybackError == null
            && (!expectsVideo || nativeVideoPresented)
            && safePropertyBoolean("vo-configured") == true) {
            capturePlaybackFacts()
            // keep-open retains the final frame; it does not issue END_FILE until stopped.
            synchronized(attemptStateLock) {
                if (!ownsNativeEntry(entryId, generation)) return
                endReason = "eof"
                playbackState = "ended"
                lastPlaybackEvent = "natural-eof"
            }
        }
        if (property == "vo-configured" && value) {
            confirmRendererTransition()
            synchronized(attemptStateLock) {
                if (!ownsNativeEntry(entryId, generation)) return
                if (fileLoaded && !rendererTransitionPending && playbackState != "error" && endReason == null) {
                    playbackState = "playing"
                    lastPlaybackEvent = "video-output-ready"
                }
            }
            refreshColorPolicy()
        }
        if (property == "vo-configured" && !value && fileLoaded
            && hasActivePlayback() && !stopRequested && !rendererTransitionPending)
            monitorVideoOutputReadiness()
    }

    override fun eventProperty(property: String, value: String, entryId: Long) =
        dispatchNativeCallback(entryId) { handleStringProperty(property, value, entryId, it) }

    private fun handleStringProperty(property: String, value: String, entryId: Long, generation: Long) {
        synchronized(attemptStateLock) {
            if (!ownsNativeEntry(entryId, generation) || !nativeAttemptStarted) return
            when (property) {
                "video-format" -> if (value.isNotBlank() && playbackState != "error" && endReason == null)
                    lastPlaybackEvent = "video-format-ready"
                "audio-codec-name" -> if (value.isNotBlank() && playbackState != "error" && endReason == null)
                    lastPlaybackEvent = "audio-format-ready"
                "video-target-params/gamma" -> hdrTargetObservation = null
            }
        }
        when (property) {
            "video-params/gamma", "current-tracks/video/dolby-vision-profile" -> {
                captureSourceMetadata()
                if (sourceMetadataReady && fileLoaded) refreshColorPolicy()
            }
            "video-target-params/gamma" -> {
                monitorHdrOutputTarget()
            }
        }
    }

    override fun eventProperty(property: String, value: Double, entryId: Long) = Unit

    override fun eventHook(id: Long, entryId: Long) =
        dispatchNativeCallback(entryId) { handleLoadHook(entryId, it) }

    private fun handleLoadHook(entryId: Long, generation: Long) {
        val hookGeneration = synchronized(attemptStateLock) {
            if (!ownsNativeEntry(entryId, generation) || !nativeAttemptStarted) return
            if (!loadPrepared) playbackGeneration += 1
            loadPrepared = false
            sourceMetadataReady = false
            fileLoaded = false
            playbackGeneration
        }
        try {
            if (hookGeneration != generation) resetColorSession()
            if (activeVideoOutput == LEGACY_ANDROID_VIDEO_OUTPUT) {
                // An explicit SDR renderer selection reloads the already-confirmed source.
                // START_FILE precedes on_load, so changing GPU options here would tear
                // down an active media chain. New/unknown sources must start gpu-next
                // through play()'s idle configuration or fail safely at this barrier.
                val transition = rendererTransition
                check(transition?.stage == "reloading"
                    && transition.generation == playbackGeneration
                    && transition.request === currentLoad
                    && transition.videoOutput == LEGACY_ANDROID_VIDEO_OUTPUT
                    && confirmedSdrSource()) { "当前片源尚未确认可使用兼容 GPU 渲染。" }
            }
            // Native already clears FSR before forwarding the hook and always
            // continues once, including while command-result binding buffers us.
            check(clearManagedFsrShader(true))
            synchronized(attemptStateLock) {
                if (!ownsNativeEntry(entryId, hookGeneration)) return
                fsrStatus = if (fsrMode == "off") "disabled" else "bypassed"
                fsrReason = if (fsrMode == "off") null else "等待确认当前片源的色彩信息。"
            }
        } catch (_: Throwable) {
            failRenderer("播放前无法清除旧的画面处理配置。", hookGeneration)
        }
    }

    override fun event(eventId: Int, entryId: Long) =
        dispatchNativeCallback(entryId) { handleNativeEvent(eventId, entryId, it) }

    private fun handleNativeEvent(eventId: Int, entryId: Long, generation: Long) {
        if (eventId != MPVLib.MpvEvent.START_FILE
            && (!ownsNativeEntry(entryId, generation) || !nativeAttemptStarted)) return
        when (eventId) {
            MPVLib.MpvEvent.START_FILE -> {
                synchronized(attemptStateLock) {
                    if (!ownsNativeEntry(entryId, generation)) {
                        if (generation == playbackGeneration && entryId == closingEntryId)
                            nativeFileActive = true
                        return
                    }
                    if (playbackState == "error") return
                    waitingForStartFile = false
                    nativeFileActive = true
                    nativeAttemptStarted = true
                    resetAttemptDiagnostics()
                    endReason = null
                    stopRequested = false
                    playbackState = "loading"
                    lastPlaybackEvent = "start-file"
                    lastPlaybackError = null
                    fileLoaded = false
                    sourceMetadataReady = false
                    nativeVideoPresented = false
                    hdrTargetObservation = null
                    hdrHintRequested = false
                }
                clearManagedFsrShader()
                synchronized(attemptStateLock) {
                    if (!ownsNativeEntry(entryId, generation)) return
                    fsrStatus = if (fsrMode == "off") "disabled" else "bypassed"
                    fsrReason = if (fsrMode == "off") null else "等待确认当前片源的色彩信息。"
                }
            }
            MPVLib.MpvEvent.FILE_LOADED -> {
                val videoTrack = nativeHasVideoTrack()
                val voReady = safePropertyBoolean("vo-configured") == true
                synchronized(attemptStateLock) {
                    if (!ownsNativeEntry(entryId, generation) || waitingForStartFile || playbackState == "error") return
                    lastPlaybackEvent = "file-loaded"
                    lastPlaybackError = null
                    fileLoaded = true
                    expectsVideo = videoTrack
                    playbackState = if (rendererTransitionPending || (expectsVideo && !voReady)) "loading" else "playing"
                    sourceMetadataReady = false
                }
                // VIDEO_RECONFIG supplies current decoded-frame metadata; old VO metadata
                // can still be unavailable or stale while FILE_LOADED is being delivered.
                captureSourceMetadata()
                confirmRendererTransition()
                refreshColorPolicy()
                monitorVideoOutputReadiness()
            }
            MPVLib.MpvEvent.VIDEO_RECONFIG -> {
                synchronized(attemptStateLock) {
                    if (!ownsNativeEntry(entryId, generation)) return
                    if (playbackState != "error" && endReason == null) lastPlaybackEvent = "video-reconfig"
                }
                captureSourceMetadata()
                if (fileLoaded && !waitingForStartFile && hasActivePlayback()) {
                    confirmRendererTransition()
                    capturePlaybackFacts()
                    refreshColorPolicy()
                    monitorVideoOutputReadiness()
                }
            }
            MPVLib.MpvEvent.AUDIO_RECONFIG -> {
                val codec = safePropertyString("audio-codec-name")
                synchronized(attemptStateLock) {
                    if (!ownsNativeEntry(entryId, generation) || playbackState == "error" || endReason != null) return
                    lastPlaybackEvent = "audio-reconfig"
                    codec?.let { lastAudioCodec = it }
                }
            }
            MPVLib.MpvEvent.PLAYBACK_RESTART -> {
                val voReady = safePropertyBoolean("vo-configured") == true
                val actualVo = safePropertyString("current-vo")
                synchronized(attemptStateLock) {
                    if (!ownsNativeEntry(entryId, generation) || !fileLoaded || waitingForStartFile
                        || playbackState == "error" || endReason == "eof") return
                    playbackState = if (rendererTransitionPending || (expectsVideo && !voReady)) "loading" else "playing"
                    lastPlaybackEvent = "playback-restart"
                    if (voReady && actualVo == activeVideoOutput) nativeVideoPresented = true
                }
                if (ownsNativeEntry(entryId, generation)) {
                    captureSourceMetadata()
                    confirmRendererTransition()
                    capturePlaybackFacts()
                    refreshColorPolicy()
                }
            }
        }
    }

    override fun eventEndFile(reason: Int, error: Int, entryId: Long, insertId: Long, insertCount: Int) =
        dispatchNativeCallback(entryId) { handleNativeEnd(reason, error, entryId, insertId, insertCount, it) }

    private fun handleNativeEnd(reason: Int, error: Int, entryId: Long, insertId: Long,
        insertCount: Int, generation: Long) {
        var closingTransition: RendererTransition? = null
        var endedGeneration = 0L
        val closingPrevious = synchronized(attemptStateLock) {
            if (generation != playbackGeneration) return
            if (entryId == closingEntryId && entryId != nativeEntryId) {
                // Identity, not the sign of the error or arrival order, owns
                // the old chain's terminal fact. It cannot terminate a new load.
                nativeFileActive = false
                closingEntryId = null
                if (!nativeLoadIssued && currentLoad != null && waitingForStartFile && !stopRequested) {
                    closingTransition = rendererTransition?.takeIf { it.stage == "waiting-stop" }
                    fileLoaded = false
                    sourceMetadataReady = false
                }
                true
            } else {
                if (!ownsNativeEntry(entryId, generation)) return
                nativeFileActive = false
                nativeAttemptStarted = false
                endedGeneration = playbackGeneration
                val redirected = reason == MPVLib.MpvEndReason.REDIRECT && error >= 0
                    && insertId > 0 && insertCount > 0
                if (playbackState == "error") {
                    endReason = "error"
                } else if (stopRequested || reason == MPVLib.MpvEndReason.STOP || reason == MPVLib.MpvEndReason.QUIT) {
                    endReason = if (reason == MPVLib.MpvEndReason.QUIT) "quit" else "stop"
                    playbackState = "idle"
                    lastPlaybackEvent = "stopped"
                } else if (redirected) {
                    endReason = "redirect"
                    playbackState = "loading"
                    lastPlaybackEvent = "playlist-redirect"
                } else if (reason == MPVLib.MpvEndReason.EOF && error >= 0 && fileLoaded && lastPlaybackError == null
                    && (!expectsVideo || nativeVideoPresented)) {
                    endReason = "eof"
                    playbackState = "ended"
                    lastPlaybackEvent = "natural-eof"
                } else {
                    endReason = "error"
                    playbackState = "error"
                    lastPlaybackEvent = "end-file-error"
                }
                if (endReason == "error") {
                    val explanation = lastEglFailure ?: lastPlaybackError ?: "媒体加载失败"
                    lastPlaybackError = "$explanation（mpv 结束原因 $reason，错误 $error）。"
                }
                stopRequested = false
                fileLoaded = false
                sourceMetadataReady = false
                loadPrepared = false
                // The paired native END supplies the first inserted identity.
                // An unrelated later START must never be adopted blindly.
                nativeEntryId = insertId.takeIf { redirected }
                nativeLoadIssued = redirected
                waitingForStartFile = redirected
                rendererTransition = null
                rendererTransitionPending = false
                hdrHintRequested = false
                hdrTargetObservation = null
                false
            }
        }
        if (closingPrevious) {
            closingTransition?.let { transition ->
                surfaceView?.post { finishRendererTransition(transition) }
            }
            return
        }
        hdrCapabilities?.requestWindowHdr(false, endedGeneration)
        if (playbackGeneration == endedGeneration)
            clearManagedFsrShader()
    }

    override fun logMessage(prefix: String, level: Int, text: String, entryId: Long) =
        dispatchNativeCallback(entryId) { handleLogMessage(prefix, level, text, entryId, it) }

    private fun handleLogMessage(prefix: String, level: Int, text: String, entryId: Long, generation: Long) {
        // mpv logs have no file ID. Accept current-attempt evidence only after
        // START_FILE and freeze it at END_FILE; closing/pre-start logs are not
        // proof of a failure of the next requested media.
        val line = sanitizeDiagnosticLine(prefix, text)
        if (line.isBlank())
            return
        synchronized(attemptStateLock) {
            if (!nativeAttemptStarted || !ownsNativeEntry(entryId, generation)) return
            synchronized(diagnosticLogs) {
                diagnosticLogs.addLast(line)
                while (diagnosticLogs.size > MAX_DIAGNOSTIC_LOGS)
                    diagnosticLogs.removeFirst()
            }
            // Recoverable probes remain evidence; only native terminal facts
            // select an error disposition and its safe EGL explanation.
            if (level <= 30 && hasActivePlayback() && !nativeVideoPresented) {
                synchronized(startupDiagnosticLogs) {
                    if (startupDiagnosticLogs.size < MAX_STARTUP_DIAGNOSTIC_LOGS
                        && line !in startupDiagnosticLogs)
                        startupDiagnosticLogs.addLast(line)
                }
            }
            val renderPrefix = prefix.lowercase().replace(Regex("[^a-z0-9]"), "")
            if (renderPrefix == "vogpunextopengl" || renderPrefix == "vogpuopengl")
                parseEglFailure(text)?.let { lastEglFailure = it }
        }
        if (ownsNativeEntry(entryId, generation) && nativeAttemptStarted && shouldFallbackFsr(level, line))
            recordFsrFallback("FSR Shader 编译失败，已恢复普通缩放。")
    }

    private fun parseEglFailure(text: String): String? {
        if (text.length > 512) return null
        val match = Regex("^OHMYCINE_EGL_FAILURE_V2 stage=([a-z-]{1,40}) egl=(0x[0-9a-fA-F]{1,8}) native=(-?\\d{1,10}) request=(0x[0-9a-fA-F]{1,8}) rgb=(\\d{1,2}) gles=([023])$")
            .matchEntire(text.trim()) ?: return null
        val stage = match.groupValues[1]
        if (stage !in setOf("window-acquire", "window-missing", "display-get", "display-initialize",
                "api-bind", "config-count", "config-list", "config-match", "config-refine", "context-create",
                "context-version-query", "context-version-mismatch", "gl-version-mismatch", "renderer-init",
                "surface-color-query", "surface-color-mismatch", "surface-color-unsupported", "colorspace-extension",
                "surface-unbind", "surface-destroy", "window-format-query", "window-format-apply",
                "surface-create", "context-bind", "hdr-capability", "swap-buffers", "geometry-update")) return null
        val code = match.groupValues[2]
        val native = match.groupValues[3]
        return "原生 EGL 初始化失败（阶段 $stage，EGL $code，原生状态 $native）"
    }

    private fun resetColorSession() {
        sourceMetadataReady = false
        rendererTransitionPending = false
        rendererTransition = null
        hdrHintRequested = false
        requestedTargetTransfer = null
        hdrTargetObservation = null
        fsrFailedForPlayback = false
        videoOutputFallbackUsed = false
        hdrCapabilities?.requestWindowHdr(false, playbackGeneration)
    }

    private fun sourceGamma(): String? = if (sourceMetadataReady)
        MpvColorPolicy.metadata(safePropertyString("video-params/gamma")) else lastSourceColor?.sourceGamma

    private fun sourceDolbyProfile(): Int? = if (sourceMetadataReady)
        MpvColorPolicy.dolbyProfile(safePropertyString("current-tracks/video/dolby-vision-profile")) else lastSourceColor?.dolbyVisionProfile

    private fun confirmedSdrSource(): Boolean = (sourceMetadataReady || lastSourceColor != null)
        && MpvColorPolicy.confirmedSdr(sourceGamma(), sourceDolbyProfile())

    private fun configureDecoder() {
        // mpv also checks this policy when no hardware method initializes.
        // Explicit software must permit its selected decoder, while hardware
        // requests must never silently turn into CPU decoding after an error.
        setRenderProperty("hwdec-software-fallback", if (hardwareDecoder == "no") "yes" else "no")
        setRenderProperty("hwdec", hardwareDecoder)
    }

    private fun rendererName(context: String): String = if (context == "android") "GLES" else context

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
            setRenderProperty("gpu-api", "opengl")
            setRenderProperty("gpu-context", context)
            activeGpuContext = context
            activeVideoOutput = videoOutput
        }
        // Set the decoder and source-driven HDR hint before loading a frame or
        // creating a new swapchain, including a same-context Surface rebuild.
        configureDecoder()
        applyHdrOutputRequest(hdrCapabilities?.canRequestHdrEgl() == true && !confirmedSdrSource())
        if (rendererNeedsConfiguration && surfaceAttached) {
            setRenderProperty("vo", videoOutput)
            rendererNeedsConfiguration = !surfaceAttached
        }
    }

    private fun failRenderer(reason: String, generation: Long = playbackGeneration) {
        if (generation != playbackGeneration) return
        captureSourceMetadata()
        capturePlaybackFacts()
        synchronized(attemptStateLock) {
            if (generation != playbackGeneration) return
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
            hdrTargetObservation = null
            rendererNeedsConfiguration = true
            managedFsrAttached = false
            managedFsrOptions = null
        }
        hdrCapabilities?.requestWindowHdr(false, generation)
        if (initialized && generation == playbackGeneration) {
            runCatching { MPVLib.stopAsync() }
        }
    }

    private fun requestRendererTransition(context: String, videoOutput: String) {
        if (rendererTransition?.stage == "waiting-stop" || !hasActivePlayback())
            return
        capturePlaybackFacts()
        val oldTransition = rendererTransition
        beginRendererTransition(context, videoOutput,
            oldTransition?.position ?: lastPosition,
            oldTransition?.paused ?: (safePropertyBoolean("pause") ?: false), oldTransition)
    }

    private fun beginRendererTransition(context: String, videoOutput: String, position: Double, paused: Boolean,
        previous: RendererTransition? = null, preserveTracks: Boolean = true) {
        val surface = surfaceView ?: return
        val request = currentLoad ?: return
        val transition = RendererTransition(context, videoOutput, request, position, paused,
            if (preserveTracks) previous?.selection ?: currentTrackSelection() else RendererTrackSelection(null, null, null),
            previous?.settings ?: listOf("speed", "volume", "mute", "sub-delay", "video-zoom", "panscan", "brightness")
                .mapNotNull { name -> safePropertyString(name)?.let { name to it } }.toMap().toMutableMap(),
            playbackGeneration)
        rendererTransition = transition
        rendererTransitionPending = true
        synchronized(attemptStateLock) {
            if (nativeEntryId != null) closingEntryId = nativeEntryId
            nativeEntryId = null
            bindingEntryGeneration = null
            nativeLoadIssued = false
            nativeAttemptStarted = false
            waitingForStartFile = true
        }
        playbackState = "loading"
        endReason = null
        lastPlaybackError = null
        lastPlaybackEvent = "renderer-transition"
        nativeVideoPresented = false
        videoOutputReadinessGeneration += 1
        hdrHintRequested = false
        requestedTargetTransfer = null
        hdrTargetObservation = null
        hdrCapabilities?.requestWindowHdr(false, playbackGeneration)
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
            .onFailure { failRenderer("${rendererName(transition.context)} 渲染配置被拒绝，请打开播放诊断查看原因。") }
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
        lastPlaybackEvent = "video-output-selected"
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
        if (!initialized || !nativeFileActive || waitingForStartFile
            || rendererTransitionPending || playbackState == "error")
            return
        val gamma = sourceGamma()
        val sdr = confirmedSdrSource()
        val hdrSource = sourceMetadataReady && (MpvColorPolicy.hdrTransfer(gamma) || sourceDolbyProfile() != null)
        if (!sdr && hdrCapabilities?.hdrTypes() == null) {
            // A transient missing display fact during Surface recreation is
            // unknown, not evidence that an HDR screen became SDR.
            applyManagedFsrShader()
            return
        }
        val hdrDisplay = hdrCapabilities?.outputTransfer(gamma) != null
        if (hdrSource && hdrDisplay && hdrCapabilities?.canRequestHdrEgl() != true) {
            failRenderer("当前 HDR 播放需要原生硬件取帧和 HDR EGL 输出能力；应用运行时尚未满足。")
            return
        }
        val context = "android"
        val videoOutput = if (sdr && preferredVideoOutput == LEGACY_ANDROID_VIDEO_OUTPUT)
            LEGACY_ANDROID_VIDEO_OUTPUT else PRIMARY_ANDROID_VIDEO_OUTPUT
        val decoder = hardwareDecoder
        if (activeGpuContext != context || activeVideoOutput != videoOutput
            || safePropertyString("hwdec") != decoder) {
            // A deliberate SDR preference or a display/source change is a
            // bounded reconfiguration; native errors never select another VO.
            requestRendererTransition(context, videoOutput)
            return
        }
        runCatching { applyHdrOutputRequest(hdrCapabilities?.canRequestHdrEgl() == true && !sdr) }
            .onFailure { failRenderer("HDR 色彩配置被拒绝，请打开播放诊断查看原生错误。") }
        if (playbackState == "error") return
        applyManagedFsrShader()
        monitorHdrOutputTarget()
    }

    private fun applyHdrOutputRequest(enabled: Boolean) {
        val inputGamma = sourceGamma()
        val hdrSource = sourceMetadataReady && (MpvColorPolicy.hdrTransfer(inputGamma) || sourceDolbyProfile() != null)
        if (enabled && hdrSource && hdrCapabilities?.hdrTypes() == null)
            return
        val outputTransfer = if (enabled && hdrSource) hdrCapabilities?.outputTransfer(inputGamma) else null
        // Map to another declared HDR transfer when needed. The native EGL
        // callback must create and query the matching Surface before accepting
        // this request; an option value alone never proves HDR presentation.
        val targetTransfer = outputTransfer?.takeIf { it != MpvColorPolicy.transfer(inputGamma) }
        if (hdrHintRequested == enabled && requestedTargetTransfer == targetTransfer) {
            hdrCapabilities?.requestWindowHdr(enabled && hasActivePlayback(), playbackGeneration)
            return
        }
        setRenderProperty("target-colorspace-hint-mode", if (targetTransfer == null) "source" else "target")
        setRenderProperty("target-colorspace-hint-strict", "yes")
        setRenderProperty("target-trc", targetTransfer ?: "auto")
        setRenderProperty("target-prim", if (targetTransfer == null) "auto" else "bt.2020")
        setRenderProperty("target-colorspace-hint", if (enabled) "yes" else "no")
        hdrHintRequested = enabled
        requestedTargetTransfer = targetTransfer
        hdrCapabilities?.requestWindowHdr(enabled && hasActivePlayback(), playbackGeneration)
    }

    private fun nativeHasVideoTrack(): Boolean {
        val count = (MPVLib.getPropertyInt("track-list/count") ?: 0).coerceIn(0, 1024)
        return (0 until count).any { index ->
            safePropertyString("track-list/$index/type") == "video"
                && safePropertyBoolean("track-list/$index/albumart") != true
        }
    }

    private fun monitorVideoOutputReadiness() {
        val generation = ++videoOutputReadinessGeneration
        if (!fileLoaded || !expectsVideo || rendererTransitionPending
            || safePropertyBoolean("vo-configured") == true)
            return
        val request = currentLoad ?: return
        val playback = playbackGeneration
        surfaceView?.postDelayed({
            if (generation == videoOutputReadinessGeneration && playback == playbackGeneration
                && currentLoad === request && fileLoaded && expectsVideo
                && hasActivePlayback() && !stopRequested && !rendererTransitionPending
                && surfaceAttached && safePropertyBoolean("vo-configured") != true)
                failRenderer("视频输出未能完成初始化，请打开播放诊断查看原生错误。")
        }, VIDEO_OUTPUT_READY_TIMEOUT_MS)
    }

    private fun monitorHdrOutputTarget() {
        val sourceIsHdr = sourceMetadataReady
            && (MpvColorPolicy.hdrTransfer(sourceGamma()) || sourceDolbyProfile() != null)
        val expectedTransfer = hdrCapabilities?.outputTransfer(sourceGamma())
        if (!fileLoaded || !surfaceAttached || !nativeVideoPresented || !sourceIsHdr
            || activeGpuContext != "android" || !hdrHintRequested
            || rendererTransitionPending || playbackState != "playing"
            || expectedTransfer == null || safePropertyBoolean("vo-configured") != true) {
            hdrTargetObservation = null
            return
        }
        val request = currentLoad ?: return
        val existing = hdrTargetObservation
        if (existing != null && existing.request === request
            && existing.generation == playbackGeneration && existing.transfer == expectedTransfer)
            return
        val observation = HdrTargetObservation(request, playbackGeneration, expectedTransfer)
        hdrTargetObservation = observation
        observeHdrOutputTarget(observation, 0)
    }

    private fun observeHdrOutputTarget(observation: HdrTargetObservation, attempt: Int) {
        surfaceView?.postDelayed({
            if (hdrTargetObservation !== observation || observation.generation != playbackGeneration
                || currentLoad !== observation.request || !surfaceAttached || !fileLoaded
                || !nativeVideoPresented || stopRequested || rendererTransitionPending
                || playbackState != "playing" || activeGpuContext != "android"
                || !hdrHintRequested || safePropertyBoolean("vo-configured") != true
                || !sourceMetadataReady || hdrCapabilities?.hdrTypes() == null
                || hdrCapabilities?.outputTransfer(sourceGamma()) != observation.transfer)
                return@postDelayed
            val gamma = MpvColorPolicy.metadata(safePropertyString("video-target-params/gamma"))
            val supportedHdr = MpvColorPolicy.hdrTransfer(gamma) && hdrCapabilities?.supports(gamma) == true
            if (supportedHdr)
                return@postDelayed
            if (MpvColorPolicy.sdrTarget(gamma)
                || (MpvColorPolicy.hdrTransfer(gamma) && hdrCapabilities?.supports(gamma) == false)) {
                failRenderer("屏幕支持 HDR，但原生渲染目标未协商到支持的 HDR 色彩空间；请打开播放诊断查看原因。")
                return@postDelayed
            }
            // Unavailable target metadata cannot prove SDR or successful HDR.
            // Keep it unknown and await a new native target-property event.
            if (attempt + 1 < HDR_TARGET_OBSERVATIONS)
                observeHdrOutputTarget(observation, attempt + 1)
        }, if (attempt == 0) HDR_TARGET_SETTLE_MS else 2_000L)
    }

    private data class HdrTargetObservation(val request: PendingLoad, val generation: Long, val transfer: String)

    private fun colorDiagnostics(): MpvColorDiagnostics {
        val retainedSource = lastSourceColor
        if (!sourceMetadataReady && retainedSource != null) {
            return retainedSource.copy(targetGamma = null, targetPrimaries = null, targetPixelFormat = null,
                outputMode = "unknown", gpuContext = null,
                reason = if (playbackState == "error")
                    "保留故障前确认的片源信息；渲染目标当前不可用。" else "渲染目标正在重新建立。")
        }
        fun metadata(name: String): String? = MpvColorPolicy.metadata(safePropertyString(name))
        val targetReady = hasActivePlayback() && sourceMetadataReady && surfaceAttached && nativeVideoPresented && !rendererTransitionPending
            && safePropertyBoolean("vo-configured") == true
        val gamma = if (targetReady) metadata("video-target-params/gamma") else null
        val transfer = MpvColorPolicy.transfer(gamma)
        val outputMode = when {
            !targetReady || transfer == null -> "unknown"
            MpvColorPolicy.hdrTransfer(gamma) -> if (activeGpuContext == "android"
                && hdrCapabilities?.supports(gamma) == true) "hdr" else "unknown"
            MpvColorPolicy.sdrTarget(gamma) -> "sdr"
            else -> "unknown"
        }
        val reason = when {
            !hasActivePlayback() -> null
            outputMode == "hdr" ->
                "已观察到原生 EGL HDR 渲染目标；屏幕实际 HDR 呈现仍需设备验证。"
            (MpvColorPolicy.hdrTransfer(sourceGamma()) || sourceDolbyProfile() != null)
                && hdrCapabilities?.hdrTypes() == null ->
                "尚未获取当前屏幕的 HDR 能力；保持当前色彩处理配置。"
            (MpvColorPolicy.hdrTransfer(sourceGamma()) || sourceDolbyProfile() != null)
                && hdrCapabilities?.outputTransfer(sourceGamma()) != null ->
                "屏幕声明支持 HDR，当前尚未确认原生 HDR 渲染目标。"
            MpvColorPolicy.hdrTransfer(sourceGamma()) || sourceDolbyProfile() != null ->
                "当前屏幕未声明可用的 HDR 输出能力；按 SDR 屏幕映射。"
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
                val initialContext = "android"
                activeGpuContext = initialContext
                MPVLib.setOptionString("gpu-api", "opengl")
                MPVLib.setOptionString("gpu-context", initialContext)
                MPVLib.setOptionString("opengl-es", "yes")
                MPVLib.setOptionString("hwdec", hardwareDecoder)
                MPVLib.setOptionString("hwdec-software-fallback", if (hardwareDecoder == "no") "yes" else "no")
                MPVLib.setOptionString("hwdec-codecs", "all")
                MPVLib.setOptionString("ao", "audiotrack,opensles")
                MPVLib.setOptionString("audio-set-media-role", "yes")
                MPVLib.setOptionString("osc", "no")
                MPVLib.setOptionString("ytdl", "no")
                // A rejected video Surface must end in native ERROR even when audio remains selected.
                MPVLib.setOptionString("stop-playback-on-init-failure", "yes")
                MPVLib.setOptionString("keep-open", "yes")
                MPVLib.setOptionString("idle", "yes")
                MPVLib.setOptionString("force-window", "no")
                MPVLib.setOptionString("input-default-bindings", "no")
                MPVLib.setOptionString("cache", cacheMode)
                MPVLib.setOptionString("demuxer-max-bytes", demuxerMaxBytes.toString())
                MPVLib.setOptionString("demuxer-max-back-bytes", (32 * 1024 * 1024).toString())
                MPVLib.setOptionString("video-sync", videoSync)
                MPVLib.setOptionString("target-colorspace-hint", if (hdrCapabilities?.canRequestHdrEgl() == true) "yes" else "no")
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
                MPVLib.observeProperty("video-target-params/gamma", MPVLib.MpvFormat.STRING)
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
                .onFailure { failRenderer("${rendererName(activeGpuContext)} 表面配置被拒绝，请打开播放诊断查看原因。") }
            if (playbackState != "error") lastPlaybackEvent = "surface-attached"
            pendingLoad?.let { play(it) }
            refreshColorPolicy()
            monitorVideoOutputReadiness()
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
            nativeVideoPresented = false
            videoOutputReadinessGeneration += 1
            hdrTargetObservation = null
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
    val request: PendingLoad,
    val position: Double,
    var paused: Boolean,
    val selection: RendererTrackSelection,
    val settings: MutableMap<String, String>,
    val generation: Long,
    var stage: String = "waiting-stop",
)
