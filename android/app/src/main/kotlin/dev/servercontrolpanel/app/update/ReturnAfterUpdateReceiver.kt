package dev.servercontrolpanel.app.update

import android.app.ActivityOptions
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import dev.servercontrolpanel.app.MainActivity
import dev.servercontrolpanel.app.R

class ReturnAfterUpdateReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return

        val openAppIntent = Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)

        tryReopen(context, openAppIntent)

        postNotice(context, openAppIntent)
    }

    private fun tryReopen(context: Context, openAppIntent: Intent) {
        val creationOptions = ActivityOptions.makeBasic().apply {
            pendingIntentCreatorBackgroundActivityStartMode =
                ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED
        }
        val pending = PendingIntent.getActivity(
            context,
            REOPEN_CODE,
            openAppIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
            creationOptions.toBundle(),
        )
        val sendOptions = ActivityOptions.makeBasic().apply {
            pendingIntentBackgroundActivityStartMode =
                ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED
        }
        try {
            pending.send(sendOptions.toBundle())
        } catch (e: PendingIntent.CanceledException) {
            Log.i(TAG, "reopen: PendingIntent cancelled: ${e.message}")
        }
    }

    private fun postNotice(context: Context, openAppIntent: Intent) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        manager.createNotificationChannel(
            NotificationChannel(
                CHANNEL,
                "App updates",
                NotificationManager.IMPORTANCE_LOW,
            ),
        )

        val tap = PendingIntent.getActivity(
            context,
            0,
            openAppIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

        val notification = Notification.Builder(context, CHANNEL)
            .setSmallIcon(R.mipmap.ic_launcher)
            .setContentTitle("Server Control Panel updated")
            .setContentText("Tap to return to the screen you were on.")
            .setContentIntent(tap)
            .setAutoCancel(true)
            .build()

        manager.notify(NOTICE_ID, notification)
    }

    private companion object {
        const val TAG = "ReturnAfterUpdate"
        const val CHANNEL = "update_finished"
        const val NOTICE_ID = 4711
        const val REOPEN_CODE = 4712
    }
}
