package com.ohmycine.player.updater

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import com.ohmycine.player.MainActivity

/** A foreground lease for the Rust updater. It never owns or launches installs. */
class UpdaterService : Service() {
    companion object {
        private const val CHANNEL_ID = "ohmycine_updates"
        // Separate from the media download service's 3100..7195 ID range.
        private const val NOTIFICATION_ID = 8201
        @Volatile private var current: UpdaterService? = null
        private val callbacks = mutableListOf<(Boolean) -> Unit>()

        fun begin(context: Context, version: String, downloaded: Long, total: Long?, ready: (Boolean) -> Unit) {
            val running = current
            if (running != null) {
                ready(running.updateProgress(downloaded, total))
                return
            }
            synchronized(callbacks) { callbacks.add(ready) }
            try {
                ContextCompat.startForegroundService(context, Intent(context, UpdaterService::class.java).apply {
                    putExtra("version", version.take(32))
                    putExtra("downloaded", downloaded)
                    if (total != null) putExtra("total", total)
                })
            } catch (error: Exception) {
                completeStart(false)
                throw error
            }
            // A failed platform start cannot strand the native command forever.
            Handler(Looper.getMainLooper()).postDelayed({ if (current == null) completeStart(false) }, 5000)
        }

        fun progress(downloaded: Long, total: Long?): Boolean = current?.updateProgress(downloaded, total) ?: false
        fun finish(ready: Boolean) { current?.finishDownload(ready) }

        private fun completeStart(active: Boolean) {
            val waiting = synchronized(callbacks) { callbacks.toList().also { callbacks.clear() } }
            waiting.forEach { it(active) }
        }
    }

    @Volatile private var active = false
    private var version = ""
    private var lastNotification = 0L

    override fun onCreate() {
        super.onCreate()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            getSystemService(NotificationManager::class.java).createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "应用更新", NotificationManager.IMPORTANCE_LOW).apply {
                    description = "显示 OhMyCine 更新下载进度"
                    setSound(null, null)
                },
            )
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent == null) {
            completeStart(false)
            stopSelf(startId)
            return START_NOT_STICKY
        }
        version = intent.getStringExtra("version").orEmpty().take(32)
        val downloaded = intent.getLongExtra("downloaded", 0)
        val total = if (intent.hasExtra("total")) intent.getLongExtra("total", 0) else null
        try {
            // Exactly one foreground start; progress uses the existing instance.
            startForeground(NOTIFICATION_ID, notification(downloaded, total))
            active = true
            current = this
            completeStart(true)
        } catch (_: Exception) {
            completeStart(false)
            stopSelf(startId)
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onTimeout(startId: Int, fgsType: Int) {
        // The next native progress/lease read pauses and syncs the prefix.
        // Do not recreate a service to evade Android's dataSync budget.
        active = false
        if (current === this) current = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf(startId)
    }

    override fun onDestroy() {
        active = false
        if (current === this) current = null
        completeStart(false)
        super.onDestroy()
    }

    private fun updateProgress(downloaded: Long, total: Long?): Boolean {
        if (!active) return false
        val now = SystemClock.elapsedRealtime()
        if (now - lastNotification >= 1000) {
            notifySafely(notification(downloaded, total))
            lastNotification = now
        }
        return active
    }

    private fun finishDownload(ready: Boolean) {
        active = false
        if (current === this) current = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        notifySafely(baseNotification()
            .setContentText(if (ready) "下载完成，打开应用后点击安装" else "下载已暂停，打开应用后可继续")
            .setOngoing(false).setAutoCancel(true).build())
        stopSelf()
    }

    private fun notifySafely(notification: Notification) {
        try {
            getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, notification)
        } catch (_: SecurityException) {
            // Denying POST_NOTIFICATIONS does not invalidate the download lease.
        }
    }

    private fun notification(downloaded: Long, total: Long?): Notification {
        val known = total != null && total > 0
        val percent = if (known) ((downloaded.coerceIn(0, total!!) * 100) / total).toInt() else 0
        return baseNotification()
            .setContentText(if (known) "已下载 $percent%" else "正在下载更新")
            .setProgress(if (known) 100 else 0, percent, !known)
            .setOngoing(true).build()
    }

    private fun baseNotification() = NotificationCompat.Builder(this, CHANNEL_ID)
        .setSmallIcon(android.R.drawable.stat_sys_download)
        .setContentTitle("OhMyCine 更新 $version")
        .setOnlyAlertOnce(true).setShowWhen(false)
        .setCategory(NotificationCompat.CATEGORY_PROGRESS)
        .setContentIntent(PendingIntent.getActivity(
            this, 8201, Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        ))
}
