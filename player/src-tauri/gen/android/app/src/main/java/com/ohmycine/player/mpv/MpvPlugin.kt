package com.ohmycine.player.mpv

import android.Manifest
import android.app.Activity
import android.content.pm.ActivityInfo
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.view.WindowManager
import android.webkit.WebView
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin

@InvokeArg
class LoadArgs {
    lateinit var path: String
    var headers: List<HeaderArgs>? = null
    var title: String? = null
}

@InvokeArg
class HeaderArgs {
    lateinit var name: String
    lateinit var value: String
}

@InvokeArg
class SubtitleArgs {
    lateinit var url: String
    var title: String? = null
    var language: String? = null
}

@InvokeArg
class SeekArgs {
    var position: Double = 0.0
}

@InvokeArg
class PropertyArgs {
    lateinit var prop: String
    var value: String? = null
}

@InvokeArg
class OrientationArgs {
    lateinit var mode: String
}

@InvokeArg
class EngineSettingsArgs {
    lateinit var videoOutput: String
    lateinit var hardwareDecoder: String
    lateinit var cacheMode: String
    var demuxerMaxBytesMb: Int = 64
    lateinit var videoSync: String
    var backgroundPlaybackEnabled: Boolean = true
    var fsrMode: String = "auto"
    var fsrSharpness: Double = 35.0
    var fsrDenoise: Boolean = true
    var fsrTarget: String = "auto"
}

@InvokeArg
class BrightnessArgs {
    var level: Double = 50.0
}

@TauriPlugin
class MpvPlugin(private val activity: Activity) : Plugin(activity) {
    private var playbackActive = false
    private var orientationMode = "auto"
    private var backgroundPlaybackEnabled = true
    private var currentMediaTitle = "正在播放"
    private var displayBrightnessLevel: Double? = null
    private var originalWindowBrightness: Float? = null

    override fun load(webView: WebView) {
        MpvSurfaceHost.install(activity, webView)
    }

    override fun onDestroy(activity: androidx.appcompat.app.AppCompatActivity) {
        PlaybackService.stop(activity)
        exitPlaybackMode()
        MpvSurfaceHost.destroy()
    }

    override fun onPause() {
        if (playbackActive && !backgroundPlaybackEnabled)
            runCatching { MpvSurfaceHost.pause(true) }
    }

    @Command
    fun load(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(LoadArgs::class.java)
        enterPlaybackMode()
        try {
            val playablePath = preparePlayablePath(args.path)
            MpvSurfaceHost.load(playablePath, args.headers.orEmpty().map { MpvHeader(it.name, it.value) })
            currentMediaTitle = args.title?.trim()?.take(160).takeUnless { it.isNullOrEmpty() } ?: "正在播放"
            syncBackgroundPlaybackService()
        } catch (error: Exception) {
            exitPlaybackMode()
            throw error
        }
    }

    @Command
    fun applyEngineSettings(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(EngineSettingsArgs::class.java)
        MpvSurfaceHost.applyEngineSettings(MpvEngineSettings(
            videoOutput = args.videoOutput,
            hardwareDecoder = args.hardwareDecoder,
            cacheMode = args.cacheMode,
            demuxerMaxBytesMb = args.demuxerMaxBytesMb,
            videoSync = args.videoSync,
            fsrMode = args.fsrMode,
            fsrSharpness = args.fsrSharpness,
            fsrDenoise = args.fsrDenoise,
            fsrTarget = args.fsrTarget,
        ))
        backgroundPlaybackEnabled = args.backgroundPlaybackEnabled
        if (backgroundPlaybackEnabled)
            requestNotificationPermission()
        syncBackgroundPlaybackService()
    }

    @Command
    fun addSubtitle(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(SubtitleArgs::class.java)
        MpvSurfaceHost.addSubtitle(preparePlayablePath(args.url), args.title, args.language)
    }

    @Command
    fun pause(invoke: Invoke) = resolve(invoke) { MpvSurfaceHost.pause(true) }

    @Command
    fun resume(invoke: Invoke) = resolve(invoke) { MpvSurfaceHost.pause(false) }

    @Command
    fun stop(invoke: Invoke) = resolve(invoke) {
        MpvSurfaceHost.stop()
        PlaybackService.stop(activity)
        exitPlaybackMode()
    }

    @Command
    fun seek(invoke: Invoke) = resolve(invoke) {
        MpvSurfaceHost.seek(invoke.parseArgs(SeekArgs::class.java).position)
    }

    @Command
    fun getProperty(invoke: Invoke) = resolveObject(invoke) {
        MpvSurfaceHost.getProperty(invoke.parseArgs(PropertyArgs::class.java).prop)
    }

    @Command
    fun setProperty(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(PropertyArgs::class.java)
        MpvSurfaceHost.setProperty(args.prop, args.value ?: "")
    }

    @Command
    fun snapshot(invoke: Invoke) = resolveObject(invoke) { MpvSurfaceHost.snapshot() }

    @Command
    fun trackState(invoke: Invoke) = resolveObject(invoke) { MpvSurfaceHost.trackState() }

    @Command
    fun playbackDiagnostics(invoke: Invoke) = resolveObject(invoke) {
        MpvSurfaceHost.playbackDiagnostics()
    }

    @Command
    fun surfaceStatus(invoke: Invoke) = resolveObject(invoke) {
        mapOf(
            "ready" to MpvSurfaceHost.isReady(),
            "error" to MpvSurfaceHost.initializationError(),
        )
    }

    @Command
    fun orientationState(invoke: Invoke) = resolveObject(invoke) {
        orientationState()
    }

    @Command
    fun setOrientation(invoke: Invoke) = resolveObject(invoke) {
        val mode = invoke.parseArgs(OrientationArgs::class.java).mode
        require(mode == "auto" || mode == "landscape" || mode == "portrait") {
            "不支持的屏幕方向模式。"
        }
        orientationMode = mode
        if (playbackActive)
            activity.runOnUiThread { applyOrientationMode() }
        orientationState()
    }

    @Command
    fun displayBrightnessState(invoke: Invoke) = resolveObject(invoke) {
        displayBrightnessState()
    }

    @Command
    fun setDisplayBrightness(invoke: Invoke) = resolveObject(invoke) {
        val level = invoke.parseArgs(BrightnessArgs::class.java).level
        require(level.isFinite() && level in 0.0..100.0) { "屏幕亮度无效。" }
        if (originalWindowBrightness == null)
            originalWindowBrightness = activity.window.attributes.screenBrightness
        displayBrightnessLevel = level
        activity.runOnUiThread {
            val attributes = activity.window.attributes
            attributes.screenBrightness = (level / 100.0).toFloat().coerceIn(0.01f, 1f)
            activity.window.attributes = attributes
        }
        displayBrightnessState()
    }

    private fun enterPlaybackMode() {
        if (!playbackActive)
            orientationMode = "auto"
        playbackActive = true
        activity.runOnUiThread {
            applyOrientationMode()
            activity.window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            WindowCompat.setDecorFitsSystemWindows(activity.window, false)
            WindowInsetsControllerCompat(activity.window, activity.window.decorView).apply {
                systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                hide(WindowInsetsCompat.Type.systemBars())
            }
        }
    }

    private fun exitPlaybackMode() {
        playbackActive = false
        orientationMode = "auto"
        PlaybackService.stop(activity)
        activity.runOnUiThread {
            WindowInsetsControllerCompat(activity.window, activity.window.decorView)
                .show(WindowInsetsCompat.Type.systemBars())
            WindowCompat.setDecorFitsSystemWindows(activity.window, true)
            activity.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
            originalWindowBrightness?.let { brightness ->
                val attributes = activity.window.attributes
                attributes.screenBrightness = brightness
                activity.window.attributes = attributes
            }
        }
        originalWindowBrightness = null
        displayBrightnessLevel = null
    }

    private fun applyOrientationMode() {
        activity.requestedOrientation = when (orientationMode) {
            "landscape" -> ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
            "portrait" -> ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
            else -> ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
        }
    }

    private fun orientationState(): Map<String, Any> = mapOf(
        "supported" to true,
        "mode" to orientationMode,
    )

    private fun displayBrightnessState(): Map<String, Any> = mapOf(
        "supported" to true,
        "level" to (displayBrightnessLevel ?: systemBrightnessLevel()),
    )

    private fun systemBrightnessLevel(): Double {
        val value = runCatching {
            Settings.System.getInt(activity.contentResolver, Settings.System.SCREEN_BRIGHTNESS)
        }.getOrDefault(128)
        return (value / 255.0 * 100.0).coerceIn(0.0, 100.0)
    }

    private fun syncBackgroundPlaybackService() {
        if (playbackActive && backgroundPlaybackEnabled)
            PlaybackService.start(activity, currentMediaTitle)
        else
            PlaybackService.stop(activity)
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU)
            return
        if (ContextCompat.checkSelfPermission(activity, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED)
            return
        ActivityCompat.requestPermissions(activity, arrayOf(Manifest.permission.POST_NOTIFICATIONS), 9415)
    }

    private fun preparePlayablePath(path: String): String {
        if (!path.startsWith("content://", ignoreCase = true))
            return path

        val descriptor = activity.contentResolver.openFileDescriptor(Uri.parse(path), "r")
            ?: error("Android 无法读取所选媒体文件。")
        val fd = descriptor.detachFd()
        return "fdclose://$fd"
    }

    private fun resolve(invoke: Invoke, action: () -> Unit) {
        try {
            action()
            invoke.resolve()
        } catch (error: Exception) {
            invoke.reject(error.message ?: "Android 播放器命令执行失败。")
        }
    }

    private fun resolveObject(invoke: Invoke, action: () -> Any) {
        try {
            invoke.resolveObject(action())
        } catch (error: Exception) {
            invoke.reject(error.message ?: "Android 播放器命令执行失败。")
        }
    }
}
