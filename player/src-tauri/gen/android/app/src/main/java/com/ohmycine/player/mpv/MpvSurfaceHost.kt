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

    @Volatile
    private var initialized = false

    @Volatile
    private var surfaceAttached = false

    @Volatile
    private var initializationError: String? = null

    private var surfaceView: OhMyCineMpvSurfaceView? = null
    private var surfaceContainer: FrameLayout? = null
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
                surface.initialize()
                initializationError = null
            } catch (error: Exception) {
                initializationError = error.message ?: "Android 播放器初始化失败。"
            }
        }
    }

    fun destroy() {
        surfaceView?.destroy()
        surfaceView = null
        surfaceContainer = null
        pendingLoad = null
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
        surfaceWidth = 0
        surfaceHeight = 0
        synchronized(diagnosticLogs) { diagnosticLogs.clear() }
    }

    fun isReady(): Boolean = initialized && surfaceAttached
    fun initializationError(): String? = initializationError

    @Synchronized
    fun load(path: String, headers: List<MpvHeader>) {
        initializationError?.let { error(it) }
        val request = PendingLoad(path, headers)
        pendingLoad = request
        if (!isReady())
            return
        play(request)
    }

    @Synchronized
    private fun play(request: PendingLoad) {
        pendingLoad = null
        playbackState = "loading"
        lastPlaybackEvent = "load-command"
        lastPlaybackError = null
        fileLoaded = false
        stopRequested = false
        if (activeVideoOutput != preferredVideoOutput) {
            MPVLib.setPropertyString("vo", preferredVideoOutput)
            activeVideoOutput = preferredVideoOutput
        }
        videoOutputFallbackUsed = false
        playbackTransport = if (request.path.startsWith("http://127.0.0.1:")) "rust-loopback" else "direct"
        synchronized(diagnosticLogs) { diagnosticLogs.clear() }
        val headers = request.headers
        val headerFields = headers.joinToString(",") { "${it.name}: ${it.value}" }
        MPVLib.setPropertyString("http-header-fields", headerFields)
        MPVLib.command(arrayOf("loadfile", request.path, "replace"))
    }

    @Synchronized
    fun applyEngineSettings(settings: MpvEngineSettings) {
        require(settings.videoOutput == "gpu-next" || settings.videoOutput == "gpu")
        require(settings.hardwareDecoder == "auto-safe" || settings.hardwareDecoder == "auto" || settings.hardwareDecoder == "software")
        require(settings.cacheMode == "auto" || settings.cacheMode == "enabled" || settings.cacheMode == "disabled")
        require(settings.demuxerMaxBytesMb == 64 || settings.demuxerMaxBytesMb == 128 || settings.demuxerMaxBytesMb == 256 || settings.demuxerMaxBytesMb == 512)
        require(settings.videoSync == "audio" || settings.videoSync == "display-resample" || settings.videoSync == "display-vdrop")
        preferredVideoOutput = settings.videoOutput
        activeVideoOutput = settings.videoOutput
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
        videoOutputFallbackUsed = false

        if (initialized) {
            MPVLib.setPropertyString("vo", activeVideoOutput)
            MPVLib.setPropertyString("hwdec", hardwareDecoder)
            MPVLib.setPropertyString("cache", cacheMode)
            MPVLib.setPropertyString("demuxer-max-bytes", demuxerMaxBytes.toString())
            MPVLib.setPropertyString("video-sync", videoSync)
            applyManagedFsrShader()
        }
    }

    fun addSubtitle(url: String, title: String?, language: String?) {
        requireInitialized()
        MPVLib.command(arrayOf("sub-add", url, "select", title ?: "", language ?: ""))
    }

    fun pause(paused: Boolean) {
        requireInitialized()
        MPVLib.setPropertyBoolean("pause", paused)
    }

    fun stop() {
        pendingLoad = null
        if (initialized) {
            stopRequested = true
            MPVLib.command(arrayOf("stop"))
            playbackState = "idle"
            lastPlaybackEvent = "stopped"
            fileLoaded = false
        }
    }

    fun seek(position: Double) {
        requireInitialized()
        MPVLib.command(arrayOf("seek", position.toString(), "absolute+exact"))
    }

    fun setProperty(name: String, value: String) {
        requireInitialized()
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
            "volume", "time-pos", "duration", "speed", "panscan", "video-zoom",
            "brightness", "sub-delay", "cache-speed" -> (MPVLib.getPropertyDouble(name) ?: 0.0).toString()
            else -> MPVLib.getPropertyString(name) ?: ""
        }
    }

    fun snapshot(): MpvSnapshot = MpvSnapshot(
        time = if (initialized) MPVLib.getPropertyDouble("time-pos") ?: 0.0 else 0.0,
        duration = if (initialized) MPVLib.getPropertyDouble("duration") ?: 0.0 else 0.0,
        paused = if (initialized) MPVLib.getPropertyBoolean("pause") ?: true else true,
    )

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
        val logs = synchronized(diagnosticLogs) { diagnosticLogs.toList() }
        return MpvPlaybackDiagnostics(
            state = playbackState,
            lastEvent = lastPlaybackEvent,
            lastError = lastPlaybackError,
            fileLoaded = fileLoaded,
            videoFormat = safePropertyString("video-format"),
            audioCodec = safePropertyString("audio-codec-name"),
            voConfigured = safePropertyBoolean("vo-configured") ?: false,
            hardwareDecoder = safePropertyString("hwdec-current"),
            videoOutput = activeVideoOutput,
            videoOutputFallbackUsed = videoOutputFallbackUsed,
            playbackTransport = playbackTransport,
            fsrStatus = fsrStatus,
            fsrReason = fsrReason,
            logs = logs,
        )
    }

    override fun eventProperty(property: String) = Unit

    override fun eventProperty(property: String, value: Long) = Unit

    override fun eventProperty(property: String, value: Boolean) {
        if (property == "vo-configured" && value)
            lastPlaybackEvent = "video-output-ready"
    }

    override fun eventProperty(property: String, value: String) {
        when (property) {
            "video-format" -> if (value.isNotBlank()) lastPlaybackEvent = "video-format-ready"
            "audio-codec-name" -> if (value.isNotBlank()) lastPlaybackEvent = "audio-format-ready"
        }
    }

    override fun eventProperty(property: String, value: Double) = Unit

    override fun event(eventId: Int) {
        when (eventId) {
            MPVLib.MpvEvent.START_FILE -> {
                stopRequested = false
                playbackState = "loading"
                lastPlaybackEvent = "start-file"
                lastPlaybackError = null
                fileLoaded = false
            }
            MPVLib.MpvEvent.FILE_LOADED -> {
                playbackState = "playing"
                lastPlaybackEvent = "file-loaded"
                lastPlaybackError = null
                fileLoaded = true
            }
            MPVLib.MpvEvent.VIDEO_RECONFIG -> lastPlaybackEvent = "video-reconfig"
            MPVLib.MpvEvent.AUDIO_RECONFIG -> lastPlaybackEvent = "audio-reconfig"
            MPVLib.MpvEvent.PLAYBACK_RESTART -> {
                playbackState = "playing"
                lastPlaybackEvent = "playback-restart"
            }
            MPVLib.MpvEvent.END_FILE -> {
                if (stopRequested) {
                    playbackState = "idle"
                    lastPlaybackEvent = "stopped"
                    stopRequested = false
                    return
                }
                val error = safePropertyString("error")
                if (!fileLoaded || (!error.isNullOrBlank() && error != "success")) {
                    playbackState = "error"
                    lastPlaybackEvent = "end-file-error"
                    lastPlaybackError = error?.takeUnless { it == "success" }
                        ?: "媒体文件未能完成加载，请打开播放诊断查看原因。"
                } else {
                    playbackState = "ended"
                    lastPlaybackEvent = "end-file"
                }
            }
        }
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
        if (shouldFallbackVideoOutput(level, line)) {
            activeVideoOutput = FALLBACK_ANDROID_VIDEO_OUTPUT
            videoOutputFallbackUsed = true
            lastPlaybackEvent = "video-output-fallback"
            MPVLib.setPropertyString("vo", FALLBACK_ANDROID_VIDEO_OUTPUT)
        }
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
            clearManagedFsrShader()
            fsrStatus = "disabled"
            fsrReason = null
            return
        }
        val shader = fsrShaderFile
        if (shader == null || !shader.isFile) {
            recordFsrFallback("应用内置 FSR Shader 文件不可用。")
            return
        }

        runCatching {
            clearManagedFsrShader()
            MPVLib.setPropertyString("glsl-shader-opts", fsrShaderOptions())
            MPVLib.command(arrayOf("change-list", "glsl-shaders", "append", shader.absolutePath))
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
        runCatching { MPVLib.setPropertyString("glsl-shader-opts", fsrShaderOptions()) }
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

    private fun clearManagedFsrShader() {
        if (initialized)
            runCatching { MPVLib.command(arrayOf("change-list", "glsl-shaders", "clr", "")) }
    }

    private fun recordFsrFallback(reason: String) {
        clearManagedFsrShader()
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

    private fun shouldFallbackVideoOutput(level: Int, line: String): Boolean {
        if (level > 20 || playbackState != "loading" || fileLoaded || videoOutputFallbackUsed)
            return false
        val normalized = line.lowercase()
        return preferredVideoOutput == PRIMARY_ANDROID_VIDEO_OUTPUT
            && activeVideoOutput == PRIMARY_ANDROID_VIDEO_OUTPUT
            && !normalized.contains("shader")
            && !normalized.contains("glsl")
            && !normalized.contains("hook")
            && (normalized.contains("gpu") || normalized.contains("vo"))
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
                MPVLib.setOptionString("vo", preferredVideoOutput)
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
                MPVLib.setOptionString("gpu-shader-cache-dir", context.cacheDir.path)
                MPVLib.setOptionString("icc-cache-dir", context.cacheDir.path)
                MPVLib.setOptionString("tls-verify", "yes")
                MPVLib.setOptionString("tls-ca-file", caFile.path)
                MPVLib.init()
                MPVLib.observeProperty("video-format", MPVLib.MpvFormat.STRING)
                MPVLib.observeProperty("audio-codec-name", MPVLib.MpvFormat.STRING)
                MPVLib.observeProperty("vo-configured", MPVLib.MpvFormat.FLAG)
                MPVLib.observeProperty("hwdec-current", MPVLib.MpvFormat.STRING)
                initialized = true
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
            MPVLib.setPropertyString("vo", activeVideoOutput)
            surfaceAttached = true
            lastPlaybackEvent = "surface-attached"
            pendingLoad?.let { play(it) }
        }

        override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
            surfaceWidth = width.coerceAtLeast(0)
            surfaceHeight = height.coerceAtLeast(0)
            MPVLib.setPropertyString("android-surface-size", "${width}x$height")
            refreshFsrTargetParameters()
        }

        override fun surfaceDestroyed(holder: SurfaceHolder) {
            if (!surfaceAttached)
                return
            MPVLib.setPropertyString("vo", "null")
            MPVLib.setPropertyString("force-window", "no")
            MPVLib.detachSurface()
            surfaceAttached = false
            lastPlaybackEvent = "surface-detached"
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
internal data class PendingLoad(val path: String, val headers: List<MpvHeader>)
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
    val logs: List<String>,
)
