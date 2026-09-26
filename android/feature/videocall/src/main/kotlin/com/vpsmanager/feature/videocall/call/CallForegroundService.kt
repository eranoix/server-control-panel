package com.vpsmanager.feature.videocall.call

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import com.vpsmanager.feature.videocall.R

private const val CHANNEL_ID = "vpsm_videocall_ongoing"
private const val NOTIFICATION_ID = 4201

/**
 * Keeps one call's camera/microphone/audio-routing alive across backgrounding.
 * Started by [VpsmConnection.onAnswer] and by an outgoing-call join
 * ([com.vpsmanager.feature.videocall.CallViewModel]'s own call site, once it dials out);
 * stopped by whichever of those two paths ends the call first — [stop] is safe to call twice.
 *
 * The layered `phoneCall|camera|microphone` type (bitwise-OR here, matching the manifest's
 * pipe-separated declaration) requires `CAMERA`/`RECORD_AUDIO` to already be GRANTED at
 * [startForeground] time or the call throws `SecurityException` — this service does not request
 * them itself (that happens proactively on the lobby's first open);
 * it assumes the caller already confirmed grant, exactly as [VpsmConnection.onAnswer] does
 * before starting this service.
 */
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
        /**
         * True while this process's [CallForegroundService] instance is between
         * [onStartCommand] and [onDestroy] — a lighter-weight, OEM-independent alternative to
         * querying `ActivityManager.getRunningServices` for this plan's own instrumented tests
         * (11-05) and any future caller that needs to know without touching system services.
         */
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

/**
 * Seam over [CallForegroundService]'s static [CallForegroundService.start]/[CallForegroundService.stop]
 * so [com.vpsmanager.feature.videocall.CallViewModel] never needs an Android [Context] wired
 * through it just to reach a `Context.startForegroundService` call — mirrors
 * [com.vpsmanager.data.videocall.LocalMediaTrackControl]'s narrow-seam pattern. The real join path
 * ([com.vpsmanager.feature.videocall.CallViewModel.handleJoined]) previously never started this
 * service at all (only [VpsmConnection.onAnswer], the answered-incoming-call path, did) — a call
 * joined from [com.vpsmanager.feature.videocall.RoomLobbyScreen] had zero foreground-service
 * protection. [start]/[stop] must both tolerate being called more than once (the answered-call
 * path and the ordinary join path can each independently call them for the same physical call).
 */
interface CallForegroundServiceController {
    fun start()
    fun stop()
}

/** The only production [CallForegroundServiceController]: wires the real [CallForegroundService]. */
class AndroidCallForegroundServiceController(context: Context) : CallForegroundServiceController {
    private val appContext = context.applicationContext

    override fun start() = CallForegroundService.start(appContext)
    override fun stop() = CallForegroundService.stop(appContext)
}
