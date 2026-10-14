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
    external fun commandChecked(cmd: Array<out String>): Int
    // Positive immutable playlist entry ID, or the negative native command error.
    external fun loadFileChecked(cmd: Array<out String>): Long
    external fun addLoadHook(): Int
    external fun continueHook(id: Long): Int
    external fun stopAsync(): Int
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
    external fun setPropertyStringChecked(property: String, value: String): Int
    external fun observeProperty(property: String, format: Int)

    private val observers = mutableListOf<EventObserver>()
    private val logObservers = mutableListOf<LogObserver>()
    @Volatile
    private var activeEntryId = -1L

    @JvmStatic
    fun addObserver(observer: EventObserver) = synchronized(observers) { observers.add(observer) }

    @JvmStatic
    fun removeObserver(observer: EventObserver) = synchronized(observers) { observers.remove(observer) }

    @JvmStatic
    fun eventProperty(property: String, value: Long) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value, activeEntryId) }
    }

    @JvmStatic
    fun eventProperty(property: String, value: Boolean) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value, activeEntryId) }
    }

    @JvmStatic
    fun eventProperty(property: String, value: Double) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value, activeEntryId) }
    }

    @JvmStatic
    fun eventProperty(property: String, value: String) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, value, activeEntryId) }
    }

    @JvmStatic
    fun eventProperty(property: String) = synchronized(observers) {
        observers.forEach { it.eventProperty(property, activeEntryId) }
    }

    @JvmStatic
    fun event(eventId: Int) = synchronized(observers) {
        observers.forEach { it.event(eventId, activeEntryId) }
    }

    @JvmStatic
    fun eventStartFile(entryId: Long) = synchronized(observers) {
        // The native event thread delivers START before file properties/hooks.
        // Tag callbacks at receipt, even if their consumer temporarily buffers.
        activeEntryId = entryId
        observers.forEach { it.event(MpvEvent.START_FILE, entryId) }
    }

    @JvmStatic
    fun eventEndFile(reason: Int, error: Int, entryId: Long, insertId: Long, insertCount: Int) = synchronized(observers) {
        observers.forEach { it.eventEndFile(reason, error, entryId, insertId, insertCount) }
        if (activeEntryId == entryId) activeEntryId = -1L
    }

    @JvmStatic
    fun eventHook(id: Long) = synchronized(observers) {
        observers.forEach { it.eventHook(id, activeEntryId) }
    }

    @JvmStatic
    fun addLogObserver(observer: LogObserver) = synchronized(logObservers) { logObservers.add(observer) }

    @JvmStatic
    fun removeLogObserver(observer: LogObserver) = synchronized(logObservers) { logObservers.remove(observer) }

    @JvmStatic
    fun logMessage(prefix: String, level: Int, text: String) = synchronized(logObservers) {
        logObservers.forEach { it.logMessage(prefix, level, text, activeEntryId) }
    }

    interface EventObserver {
        fun eventProperty(property: String, entryId: Long)
        fun eventProperty(property: String, value: Long, entryId: Long)
        fun eventProperty(property: String, value: Boolean, entryId: Long)
        fun eventProperty(property: String, value: String, entryId: Long)
        fun eventProperty(property: String, value: Double, entryId: Long)
        fun event(eventId: Int, entryId: Long)
        fun eventEndFile(reason: Int, error: Int, entryId: Long, insertId: Long, insertCount: Int) = Unit
        fun eventHook(id: Long, entryId: Long) = Unit
    }

    interface LogObserver {
        fun logMessage(prefix: String, level: Int, text: String, entryId: Long)
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

    object MpvEndReason {
        const val EOF = 0
        const val STOP = 2
        const val QUIT = 3
        const val ERROR = 4
        const val REDIRECT = 5
    }
}
