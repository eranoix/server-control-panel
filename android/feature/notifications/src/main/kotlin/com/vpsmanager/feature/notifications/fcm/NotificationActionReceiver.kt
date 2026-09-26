package com.vpsmanager.feature.notifications.fcm

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationManagerCompat

/**
 * Fires for the one inline action whitelisted for non-critical (info/warning) metric alerts —
 * "Dispensar" — without ever opening an Activity. No server endpoint exists yet for a
 * real acknowledge call (the generated `MobileApi` exposes no such operation —
 * a documented gap in the client/server contract, not an oversight here): this receiver's only
 * effect is cancelling the local notification, so it never needs network access and can never
 * be escalated into a destructive/state-mutating call even if a malformed or spoofed broadcast
 * is sent to it — the safe-action whitelist in [ActionableNotificationBuilder], not this
 * receiver's own logic, is what keeps state-mutating actions ("Refazer deploy"/"Reiniciar")
 * out of every notification's inline actions; those exist exclusively behind
 * `DeployTriggerScreen`'s in-app confirmation.
 */
class NotificationActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val notificationId = intent.getIntExtra(EXTRA_NOTIFICATION_ID, NO_NOTIFICATION_ID)
        if (notificationId == NO_NOTIFICATION_ID) return
        NotificationManagerCompat.from(context).cancel(notificationId)
    }

    companion object {
        const val EXTRA_NOTIFICATION_ID = "vpsm_notification_id"
        private const val NO_NOTIFICATION_ID = -1
    }
}
