package dev.servercontrolpanel.feature.notifications.fcm

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context

object NotificationChannels {
    const val CHANNEL_DEPLOY = "panel_deploy"
    const val CHANNEL_ALERTS = "panel_alerts"
    const val CHANNEL_INBOX = "panel_inbox"

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

    fun channelFor(eventType: String): String = when {
        eventType.startsWith("job.") -> CHANNEL_DEPLOY
        eventType.startsWith("metric.") -> CHANNEL_ALERTS
        else -> CHANNEL_INBOX
    }
}
