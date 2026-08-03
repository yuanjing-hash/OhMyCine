package com.ohmycine.player.mpv

import android.app.Activity
import android.content.pm.ActivityInfo
import android.view.WindowManager
import android.webkit.WebView
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

@TauriPlugin
class MpvPlugin(private val activity: Activity) : Plugin(activity) {
    private var playbackActive = false
    private var orientationMode = "auto"

    override fun load(webView: WebView) {
        MpvSurfaceHost.install(activity, webView)
    }

    override fun onDestroy(activity: androidx.appcompat.app.AppCompatActivity) {
        exitPlaybackMode()
        MpvSurfaceHost.destroy()
    }

    @Command
    fun load(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(LoadArgs::class.java)
        enterPlaybackMode()
        try {
            MpvSurfaceHost.load(args.path, args.headers.orEmpty().map { MpvHeader(it.name, it.value) })
        } catch (error: Exception) {
            exitPlaybackMode()
            throw error
        }
    }

    @Command
    fun addSubtitle(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(SubtitleArgs::class.java)
        MpvSurfaceHost.addSubtitle(args.url, args.title, args.language)
    }

    @Command
    fun pause(invoke: Invoke) = resolve(invoke) { MpvSurfaceHost.pause(true) }

    @Command
    fun resume(invoke: Invoke) = resolve(invoke) { MpvSurfaceHost.pause(false) }

    @Command
    fun stop(invoke: Invoke) = resolve(invoke) {
        MpvSurfaceHost.stop()
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
        activity.runOnUiThread {
            WindowInsetsControllerCompat(activity.window, activity.window.decorView)
                .show(WindowInsetsCompat.Type.systemBars())
            WindowCompat.setDecorFitsSystemWindows(activity.window, true)
            activity.window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
        }
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
