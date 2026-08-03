package com.ohmycine.player.mpv

import android.app.Activity
import android.webkit.WebView
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

@TauriPlugin
class MpvPlugin(private val activity: Activity) : Plugin(activity) {
    override fun load(webView: WebView) {
        MpvSurfaceHost.install(activity, webView)
    }

    override fun onDestroy(activity: androidx.appcompat.app.AppCompatActivity) {
        MpvSurfaceHost.destroy()
    }

    @Command
    fun load(invoke: Invoke) = resolve(invoke) {
        val args = invoke.parseArgs(LoadArgs::class.java)
        MpvSurfaceHost.load(args.path, args.headers.orEmpty().map { MpvHeader(it.name, it.value) })
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
    fun stop(invoke: Invoke) = resolve(invoke) { MpvSurfaceHost.stop() }

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
        mapOf("ready" to MpvSurfaceHost.isReady())
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
