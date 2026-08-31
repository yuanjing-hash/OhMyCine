package `is`.xyz.mpv

import android.content.Context
import android.graphics.Bitmap
import android.view.Surface

// JNI contract adapted from mpv-android 2026-04-25 (GPL-3.0).
@Suppress("unused")
object MPVLib {
    init {
        for (library in arrayOf("mpv", "player")) {
            System.loadLibrary(library)
        }
    }

    external fun create(appctx: Context)
    external fun init()
    external fun destroy()
    external fun attachSurface(surface: Surface)
    external fun detachSurface()
    external fun command(cmd: Array<out String>)
    external fun setOptionString(name: String, value: String): Int
    external fun grabThumbnail(dimension: Int): Bitmap?
    external fun getPropertyInt(property: String): Int?
    external fun setPropertyInt(property: String, value: Int)
    external fun getPropertyDouble(property: String): Double?
    external fun setPropertyDouble(property: String, value: Double)
    external fun getPropertyBoolean(property: String): Boolean?
    external fun setPropertyBoolean(property: String, value: Boolean)
    external fun getPropertyString(property: String): String?
    external fun setPropertyString(property: String, value: String)
    external fun observeProperty(property: String, format: Int)

    private val observers = mutableListOf<EventObserver>()
    private val logObservers = mutableListOf<LogObserver>()

    @JvmStatic
    fun addObserver(observer: EventObserver) = synchronized(observers) { observers.add(observer) }

    @JvmStatic
    fun removeObserver(observer: EventObserver) = synchronized(observers) { observers.remove(observer) }

    @JvmStatic
    fun eventProperty(property: String, value: Long) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value) }
    }

    @JvmStatic
    fun eventProperty(property: String, value: Boolean) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value) }
    }

    @JvmStatic
    fun eventProperty(property: String, value: Double) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value) }
    }

    @JvmStatic
    fun eventProperty(property: String, value: String) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value) }
    }

    @JvmStatic
    fun eventProperty(property: String) = synchronized(observers) {
        observers.forEach { it.eventProperty(property) }
    }

    @JvmStatic
    fun event(eventId: Int) = synchronized(observers) {
        observers.forEach { it.event(eventId) }
    }

    @JvmStatic
    fun addLogObserver(observer: LogObserver) = synchronized(logObservers) { logObservers.add(observer) }

    @JvmStatic
    fun removeLogObserver(observer: LogObserver) = synchronized(logObservers) { logObservers.remove(observer) }

    @JvmStatic
    fun logMessage(prefix: String, level: Int, text: String) = synchronized(logObservers) {
        logObservers.forEach { it.logMessage(prefix, level, text) }
    }

    interface EventObserver {
        fun eventProperty(property: String)
        fun eventProperty(property: String, value: Long)
        fun eventProperty(property: String, value: Boolean)
        fun eventProperty(property: String, value: String)
        fun eventProperty(property: String, value: Double)
        fun event(eventId: Int)
    }

    interface LogObserver {
        fun logMessage(prefix: String, level: Int, text: String)
    }

    object MpvFormat {
        const val NONE = 0
        const val STRING = 1
        const val FLAG = 3
        const val INT64 = 4
        const val DOUBLE = 5
    }

    object MpvEvent {
        const val START_FILE = 6
        const val END_FILE = 7
        const val FILE_LOADED = 8
        const val VIDEO_RECONFIG = 17
        const val AUDIO_RECONFIG = 18
        const val PLAYBACK_RESTART = 21
    }
}
