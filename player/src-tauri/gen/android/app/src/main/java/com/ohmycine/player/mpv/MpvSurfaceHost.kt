package com.ohmycine.player.mpv

import android.app.Activity
import android.content.Context
import android.graphics.Color
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.ViewGroup
import android.webkit.WebView
import android.widget.FrameLayout
import `is`.xyz.mpv.MPVLib
import java.io.File

internal object MpvSurfaceHost {
    @Volatile
    private var initialized = false

    @Volatile
    private var surfaceAttached = false

    private var surfaceView: OhMyCineMpvSurfaceView? = null

    fun install(activity: Activity, webView: WebView) {
        webView.post {
            if (surfaceView != null)
                return@post

            val surface = OhMyCineMpvSurfaceView(activity)
            surface.initialize()
            val container = FrameLayout(activity).apply {
                setBackgroundColor(Color.BLACK)
            }
            (webView.parent as? ViewGroup)?.removeView(webView)
            webView.setBackgroundColor(Color.TRANSPARENT)
            container.addView(surface, FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ))
            container.addView(webView, FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ))
            activity.setContentView(container)
            surfaceView = surface
        }
    }

    fun destroy() {
        surfaceView?.destroy()
        surfaceView = null
        surfaceAttached = false
        initialized = false
    }

    fun isReady(): Boolean = initialized && surfaceAttached

    fun load(path: String, headers: List<MpvHeader>) {
        requireInitialized()
        val headerFields = headers.joinToString(",") { "${it.name}: ${it.value}" }
        MPVLib.setPropertyString("http-header-fields", headerFields)
        MPVLib.command(arrayOf("loadfile", path, "replace"))
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
        if (initialized)
            MPVLib.command(arrayOf("stop"))
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
            "pause" -> (MPVLib.getPropertyBoolean(name) ?: true).toString()
            "volume", "time-pos", "duration", "speed", "panscan", "video-zoom",
            "brightness", "sub-delay" -> (MPVLib.getPropertyDouble(name) ?: 0.0).toString()
            else -> MPVLib.getPropertyString(name) ?: ""
        }
    }

    fun snapshot(): MpvSnapshot = MpvSnapshot(
        time = if (initialized) MPVLib.getPropertyDouble("time-pos") ?: 0.0 else 0.0,
        duration = if (initialized) MPVLib.getPropertyDouble("duration") ?: 0.0 else 0.0,
        paused = if (initialized) MPVLib.getPropertyBoolean("pause") ?: true else true,
    )

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

    private fun requireInitialized() {
        check(isReady()) { "Android 播放器表面尚未准备完成。" }
    }

    private class OhMyCineMpvSurfaceView(context: Context) : SurfaceView(context), SurfaceHolder.Callback {
        private var destroyed = false

        fun initialize() {
            if (!initialized) {
                val caFile = installCaCertificate(context)
                MPVLib.create(context.applicationContext)
                MPVLib.setOptionString("config", "no")
                MPVLib.setOptionString("profile", "fast")
                MPVLib.setOptionString("vo", "gpu-next")
                MPVLib.setOptionString("gpu-context", "android")
                MPVLib.setOptionString("opengl-es", "yes")
                MPVLib.setOptionString("hwdec", "mediacodec,mediacodec-copy")
                MPVLib.setOptionString("hwdec-codecs", "h264,hevc,mpeg4,mpeg2video,vp8,vp9,av1")
                MPVLib.setOptionString("ao", "audiotrack,opensles")
                MPVLib.setOptionString("osc", "no")
                MPVLib.setOptionString("keep-open", "yes")
                MPVLib.setOptionString("idle", "yes")
                MPVLib.setOptionString("force-window", "no")
                MPVLib.setOptionString("demuxer-max-bytes", (64 * 1024 * 1024).toString())
                MPVLib.setOptionString("demuxer-max-back-bytes", (32 * 1024 * 1024).toString())
                MPVLib.setOptionString("gpu-shader-cache-dir", context.cacheDir.path)
                MPVLib.setOptionString("icc-cache-dir", context.cacheDir.path)
                MPVLib.setOptionString("tls-verify", "yes")
                MPVLib.setOptionString("tls-ca-file", caFile.path)
                MPVLib.init()
                initialized = true
            }
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
            if (initialized)
                MPVLib.destroy()
        }

        override fun surfaceCreated(holder: SurfaceHolder) {
            MPVLib.attachSurface(holder.surface)
            MPVLib.setOptionString("force-window", "yes")
            MPVLib.setPropertyString("vo", "gpu-next")
            surfaceAttached = true
        }

        override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
            MPVLib.setPropertyString("android-surface-size", "${width}x$height")
        }

        override fun surfaceDestroyed(holder: SurfaceHolder) {
            if (!surfaceAttached)
                return
            MPVLib.setPropertyString("vo", "null")
            MPVLib.setOptionString("force-window", "no")
            MPVLib.detachSurface()
            surfaceAttached = false
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
}

internal data class MpvHeader(val name: String, val value: String)
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
