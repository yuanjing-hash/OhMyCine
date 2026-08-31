package com.ohmycine.player.mpv

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
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.media.app.NotificationCompat.MediaStyle
import androidx.media.session.MediaButtonReceiver
import android.support.v4.media.MediaMetadataCompat
import android.support.v4.media.session.MediaSessionCompat
import android.support.v4.media.session.PlaybackStateCompat
import com.ohmycine.player.MainActivity

class PlaybackService : Service() {
    companion object {
        private const val CHANNEL_ID = "ohmycine_playback"
        private const val NOTIFICATION_ID = 2201
        private const val ACTION_START = "com.ohmycine.player.action.START_PLAYBACK_SERVICE"
        private const val EXTRA_TITLE = "title"

        fun start(context: Context, title: String) {
            val intent = Intent(context, PlaybackService::class.java).apply {
                action = ACTION_START
                putExtra(EXTRA_TITLE, title.take(160))
            }
            ContextCompat.startForegroundService(context, intent)
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, PlaybackService::class.java))
        }
    }

    private val handler = Handler(Looper.getMainLooper())
    private lateinit var mediaSession: MediaSessionCompat
    private var mediaTitle = "正在播放"
    private var foregroundStarted = false

    private val refreshTask = object : Runnable {
        override fun run() {
            refreshSessionAndNotification()
            handler.postDelayed(this, 1000)
        }
    }

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        mediaSession = MediaSessionCompat(this, "OhMyCinePlayback").apply {
            setCallback(object : MediaSessionCompat.Callback() {
                override fun onPlay() = runCatching { MpvSurfaceHost.pause(false) }.let { Unit }
                override fun onPause() = runCatching { MpvSurfaceHost.pause(true) }.let { Unit }
                override fun onSeekTo(pos: Long) = runCatching { MpvSurfaceHost.seek(pos / 1000.0) }.let { Unit }
                override fun onFastForward() = seekRelative(10.0)
                override fun onRewind() = seekRelative(-10.0)
            })
            isActive = true
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        MediaButtonReceiver.handleIntent(mediaSession, intent)
        intent?.getStringExtra(EXTRA_TITLE)?.trim()?.takeIf { it.isNotEmpty() }?.let { mediaTitle = it.take(160) }
        refreshSessionAndNotification()
        handler.removeCallbacks(refreshTask)
        handler.post(refreshTask)
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        handler.removeCallbacks(refreshTask)
        mediaSession.isActive = false
        mediaSession.release()
        stopForegroundCompat()
        super.onDestroy()
    }

    private fun seekRelative(offset: Double) {
        val snapshot = MpvSurfaceHost.snapshot()
        val target = (snapshot.time + offset).coerceIn(0.0, snapshot.duration.takeIf { it > 0 } ?: Double.MAX_VALUE)
        runCatching { MpvSurfaceHost.seek(target) }
    }

    private fun refreshSessionAndNotification() {
        if (!MpvSurfaceHost.hasActivePlayback()) {
            stopSelf()
            return
        }
        val snapshot = MpvSurfaceHost.snapshot()
        val state = if (snapshot.paused) PlaybackStateCompat.STATE_PAUSED else PlaybackStateCompat.STATE_PLAYING
        val actions = PlaybackStateCompat.ACTION_PLAY or
            PlaybackStateCompat.ACTION_PAUSE or
            PlaybackStateCompat.ACTION_PLAY_PAUSE or
            PlaybackStateCompat.ACTION_SEEK_TO or
            PlaybackStateCompat.ACTION_FAST_FORWARD or
            PlaybackStateCompat.ACTION_REWIND
        mediaSession.setPlaybackState(
            PlaybackStateCompat.Builder()
                .setActions(actions)
                .setState(state, (snapshot.time * 1000).toLong(), if (snapshot.paused) 0f else 1f)
                .build(),
        )
        mediaSession.setMetadata(
            MediaMetadataCompat.Builder()
                .putString(MediaMetadataCompat.METADATA_KEY_TITLE, mediaTitle)
                .putString(MediaMetadataCompat.METADATA_KEY_ARTIST, "OhMyCine Player")
                .putLong(MediaMetadataCompat.METADATA_KEY_DURATION, (snapshot.duration * 1000).toLong().coerceAtLeast(0))
                .build(),
        )
        val notification = buildNotification(snapshot.paused)
        if (!foregroundStarted) {
            startForeground(NOTIFICATION_ID, notification)
            foregroundStarted = true
        } else {
            runCatching { NotificationManagerCompat.from(this).notify(NOTIFICATION_ID, notification) }
        }
    }

    private fun buildNotification(paused: Boolean) = NotificationCompat.Builder(this, CHANNEL_ID)
        .setSmallIcon(android.R.drawable.ic_media_play)
        .setContentTitle(mediaTitle)
        .setContentText("OhMyCine Player")
        .setContentIntent(
            PendingIntent.getActivity(
                this,
                0,
                Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
            ),
        )
        .addAction(
            android.R.drawable.ic_media_rew,
            "后退 10 秒",
            MediaButtonReceiver.buildMediaButtonPendingIntent(this, PlaybackStateCompat.ACTION_REWIND),
        )
        .addAction(
            if (paused) android.R.drawable.ic_media_play else android.R.drawable.ic_media_pause,
            if (paused) "播放" else "暂停",
            MediaButtonReceiver.buildMediaButtonPendingIntent(
                this,
                if (paused) PlaybackStateCompat.ACTION_PLAY else PlaybackStateCompat.ACTION_PAUSE,
            ),
        )
        .addAction(
            android.R.drawable.ic_media_ff,
            "前进 10 秒",
            MediaButtonReceiver.buildMediaButtonPendingIntent(this, PlaybackStateCompat.ACTION_FAST_FORWARD),
        )
        .setStyle(MediaStyle().setMediaSession(mediaSession.sessionToken).setShowActionsInCompactView(0, 1, 2))
        .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
        .setCategory(NotificationCompat.CATEGORY_TRANSPORT)
        .setOnlyAlertOnce(true)
        .setOngoing(true)
        .setShowWhen(false)
        .build()

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O)
            return
        val channel = NotificationChannel(CHANNEL_ID, "后台播放", NotificationManager.IMPORTANCE_LOW).apply {
            description = "显示 OhMyCine 播放进度和媒体控制"
            setSound(null, null)
        }
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    private fun stopForegroundCompat() {
        if (!foregroundStarted)
            return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N)
            stopForeground(STOP_FOREGROUND_REMOVE)
        else
            @Suppress("DEPRECATION") stopForeground(true)
        foregroundStarted = false
    }
}
