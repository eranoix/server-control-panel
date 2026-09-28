package dev.servercontrolpanel.feature.notifications.fcm

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationManagerCompat

class NotificationActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val notificationId = intent.getIntExtra(EXTRA_NOTIFICATION_ID, NO_NOTIFICATION_ID)
        if (notificationId == NO_NOTIFICATION_ID) return
        NotificationManagerCompat.from(context).cancel(notificationId)
    }

    companion object {
        const val EXTRA_NOTIFICATION_ID = "panel_notification_id"
        private const val NO_NOTIFICATION_ID = -1
    }
}
