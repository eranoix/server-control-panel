package dev.servercontrolpanel.feature.notifications.fcm

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context

/**
 * The notification channels every push this app posts to must already exist under, created at
 * [PanelApplication][dev.servercontrolpanel.app.PanelApplication] startup — before any FCM
 * message can possibly arrive. If the FCM SDK auto-creates the first channel on a background
 * push receipt instead, Android silently defers the `POST_NOTIFICATIONS` prompt to the next
 * foreground open and the app's very first pushes are lost (the documented STACK.md gotcha this
 * ordering avoids).
 */
object NotificationChannels {
    const val CHANNEL_DEPLOY = "panel_deploy"
    const val CHANNEL_ALERTS = "panel_alerts"
    const val CHANNEL_INBOX = "panel_inbox"

    /**
     * Creates every channel this app posts to. Safe to call more than once per process and
     * across process restarts: [NotificationManager.createNotificationChannel] is itself a
     * no-op when called again with an unchanged channel definition, so this function needs no
     * extra guard to be idempotent.
     */
    fun ensureChannels(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_DEPLOY, "Deploys and jobs", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Failures, cancellations and completion of deploys/jobs."
            },
        )
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_ALERTS, "Metric alerts", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Metric alerts fired and resolved."
            },
        )
        manager.createNotificationChannel(
            NotificationChannel(CHANNEL_INBOX, "Notices", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "Non-critical notices."
            },
        )
    }

    /**
     * Maps a real (dot-separated) `notify.Event.Type` prefix — exactly as emitted by this
     * project's own `buildPushPayload` (`internal/notify/pushchannel.go`) under the `event_type`
     * payload key, never the underscore-separated strings under a `type` key an earlier draft of
     * this plan's `<interfaces>` incorrectly cited — to the channel it must post on.
     */
    fun channelFor(eventType: String): String = when {
        eventType.startsWith("job.") -> CHANNEL_DEPLOY
        eventType.startsWith("metric.") -> CHANNEL_ALERTS
        else -> CHANNEL_INBOX
    }
}
