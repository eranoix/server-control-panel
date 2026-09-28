package dev.servercontrolpanel.feature.videocall.call

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import dev.servercontrolpanel.feature.videocall.R

private const val CHANNEL_ID = "panel_videocall_ongoing"
private const val NOTIFICATION_ID = 4201

class CallForegroundService : Service() {

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        ensureChannel()
        startForeground(
            NOTIFICATION_ID,
            buildOngoingCallNotification(),
            ServiceInfo.FOREGROUND_SERVICE_TYPE_PHONE_CALL or
                ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA or
                ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE,
        )
        isRunning = true
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        isRunning = false
        super.onDestroy()
    }

    private fun buildOngoingCallNotification(): Notification =
        Notification.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.videocall_ongoing_call_title))
            .setSmallIcon(R.drawable.ic_call_ongoing)
            .setOngoing(true)
            .setCategory(Notification.CATEGORY_CALL)
            .build()

    private fun ensureChannel() {
        val manager = getSystemService(NotificationManager::class.java) ?: return
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, getString(R.string.videocall_ongoing_channel_name), NotificationManager.IMPORTANCE_LOW),
        )
    }

    companion object {
        @Volatile
        var isRunning: Boolean = false
            private set

        fun start(context: Context) {
            context.startForegroundService(Intent(context, CallForegroundService::class.java))
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, CallForegroundService::class.java))
        }
    }
}

interface CallForegroundServiceController {
    fun start()
    fun stop()
}

class AndroidCallForegroundServiceController(context: Context) : CallForegroundServiceController {
    private val appContext = context.applicationContext

    override fun start() = CallForegroundService.start(appContext)
    override fun stop() = CallForegroundService.stop(appContext)
}
