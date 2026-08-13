package com.ohmycine.player.downloads

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.ohmycine.player.MainActivity

class DownloadService : Service() {
    companion object {
        private const val CHANNEL_ID = "ohmycine_downloads"
        private const val ACTION_UPDATE = "com.ohmycine.player.action.UPDATE_DOWNLOAD"
        private const val ACTION_FINISH = "com.ohmycine.player.action.FINISH_DOWNLOAD"
        private const val EXTRA_TASK = "task"
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_DOWNLOADED = "downloaded"
        private const val EXTRA_TOTAL = "total"
        private const val EXTRA_STATE = "state"

        fun update(context: Context, taskId: String, title: String, downloaded: Long, total: Long?) {
            val intent = Intent(context, DownloadService::class.java).apply {
                action = ACTION_UPDATE
                putExtra(EXTRA_TASK, taskId)
                putExtra(EXTRA_TITLE, title.take(160))
                putExtra(EXTRA_DOWNLOADED, downloaded.coerceAtLeast(0))
                if (total != null) putExtra(EXTRA_TOTAL, total.coerceAtLeast(0))
            }
            ContextCompat.startForegroundService(context, intent)
        }

        fun finish(context: Context, taskId: String, title: String, state: String) {
            val intent = Intent(context, DownloadService::class.java).apply {
                action = ACTION_FINISH
                putExtra(EXTRA_TASK, taskId)
                putExtra(EXTRA_TITLE, title.take(160))
                putExtra(EXTRA_STATE, state)
            }
            ContextCompat.startForegroundService(context, intent)
        }

        private fun notificationId(taskId: String) = 3100 + (taskId.hashCode() and 0x0fff)
    }

    private var foregroundId: Int? = null
    private val activeNotifications = linkedMapOf<Int, android.app.Notification>()

    override fun onCreate() {
        super.onCreate()
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val taskId = intent?.getStringExtra(EXTRA_TASK).orEmpty()
        val title = intent?.getStringExtra(EXTRA_TITLE)?.ifBlank { "媒体下载" } ?: "媒体下载"
        if (taskId.isBlank()) {
            stopSelf(startId)
            return START_NOT_STICKY
        }
        val id = notificationId(taskId)
        when (intent?.action) {
            ACTION_FINISH -> {
                val state = intent.getStringExtra(EXTRA_STATE).orEmpty()
                val text = when (state) {
                    "completed" -> "下载完成"
                    "cancelled" -> "下载已取消"
                    else -> "下载失败，请打开应用查看"
                }
                val notification = baseNotification(title)
                    .setContentText(text)
                    .setOngoing(false)
                    .setAutoCancel(true)
                    .build()
                activeNotifications.remove(id)
                if (foregroundId == id) {
                    val replacement = activeNotifications.entries.firstOrNull()
                    if (replacement == null)
                        stopForegroundCompat()
                    else {
                        startForeground(replacement.key, replacement.value)
                        foregroundId = replacement.key
                    }
                }
                NotificationManagerCompat.from(this).notify(id, notification)
                if (activeNotifications.isEmpty())
                    stopSelf(startId)
            }
            else -> {
                val downloaded = intent?.getLongExtra(EXTRA_DOWNLOADED, 0) ?: 0
                val hasTotal = intent?.hasExtra(EXTRA_TOTAL) == true
                val total = if (hasTotal) intent?.getLongExtra(EXTRA_TOTAL, 0) ?: 0 else 0
                val notification = baseNotification(title)
                    .setContentText(if (hasTotal && total > 0) "已下载 ${percent(downloaded, total)}%" else "正在下载")
                    .setProgress(if (hasTotal) 100 else 0, if (hasTotal) percent(downloaded, total) else 0, !hasTotal)
                    .setOngoing(true)
                    .build()
                activeNotifications[id] = notification
                ensureForeground(id, notification)
                NotificationManagerCompat.from(this).notify(id, notification)
            }
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun baseNotification(title: String) = NotificationCompat.Builder(this, CHANNEL_ID)
        .setSmallIcon(android.R.drawable.stat_sys_download)
        .setContentTitle(title)
        .setOnlyAlertOnce(true)
        .setShowWhen(false)
        .setCategory(NotificationCompat.CATEGORY_PROGRESS)
        .setContentIntent(PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        ))

    private fun ensureForeground(id: Int, notification: android.app.Notification) {
        if (foregroundId == null) {
            startForeground(id, notification)
            foregroundId = id
        }
    }

    private fun createChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL_ID, "媒体下载", NotificationManager.IMPORTANCE_LOW).apply {
                description = "显示 OhMyCine 媒体下载进度"
                setSound(null, null)
            },
        )
    }

    private fun stopForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) stopForeground(STOP_FOREGROUND_DETACH)
        else @Suppress("DEPRECATION") stopForeground(false)
        foregroundId = null
    }

    private fun percent(downloaded: Long, total: Long): Int =
        if (total <= 0) 0 else ((downloaded.coerceIn(0, total) * 100) / total).toInt()
}
