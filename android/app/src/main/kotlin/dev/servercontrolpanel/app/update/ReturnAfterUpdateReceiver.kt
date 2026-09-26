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

/**
 * Brings the user back after the app updates itself, which kills the process. The previous
 * route was saved by [dev.servercontrolpanel.data.update.ResumePoint], so reopening restores the screen.
 *
 * Two paths: (1) a best-effort reopen, which Android 16 blocks silently as a background
 * activity start (measured), kept because it may work elsewhere; (2) a one-tap notification,
 * always posted, which needs no special permission. A foreground service would be
 * disproportionate for an event that happens once per version.
 */
class ReturnAfterUpdateReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return

        val openAppIntent = Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)

        // (1) Best effort. A refusal is silent and indistinguishable from success, so never
        // assume it worked.
        tryReopen(context, openAppIntent)

        // (2) The reliable path, always posted.
        postNotice(context, openAppIntent)
    }

    /**
     * Tries to bring the app back through a `PendingIntent`, since a plain `startActivity`
     * from a receiver is always blocked. From targetSdk 35 the creator must opt in to
     * background activity starts; both creator and sender opt in, as each covers a different
     * path. It only works when the app qualifies for an exemption (SYSTEM_ALERT_WINDOW).
     */
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
                // LOW on purpose: no sound or vibration, it is an invitation, not an alert.
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
            // Says what the tap does: the interrupted user wants to know how to get back.
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
