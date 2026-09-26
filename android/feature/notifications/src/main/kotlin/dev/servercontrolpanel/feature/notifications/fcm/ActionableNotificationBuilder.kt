package dev.servercontrolpanel.feature.notifications.fcm

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import dev.servercontrolpanel.feature.notifications.R

/**
 * Extras on a tapped notification's content [Intent] so the nav host opens the target screen.
 * Only an opaque route and entity id travel here, never a token or capability flag; the target
 * screen re-authorises on load.
 */
object NotificationDeepLink {
    const val EXTRA_ROUTE = "panel_notification_route"
    const val EXTRA_ENTITY_ID = "panel_notification_entity_id"
    const val ROUTE_DEPLOY_JOB = "deploy_job"
    const val ROUTE_ALERT = "alert"
}

/** Metric severities eligible for the inline Dismiss action; `critical` is deliberately excluded. */
private val ACK_ELIGIBLE_SEVERITIES = setOf("info", "warning")

private const val REQUEST_CODE_CONTENT = 100
private const val REQUEST_CODE_VIEW_LOG = 200
private const val REQUEST_CODE_DISMISS = 300

/**
 * A whitelisted inline action. Never state-mutating (redeploy, restart): those need the
 * confirmation gate in `DeployTriggerScreen` and are never offered from a notification.
 */
internal sealed interface InlineAction {
    val label: String

    /** Opens the same target screen as the content tap; read-only, safe at any severity. */
    data class OpenScreen(override val label: String) : InlineAction

    /** Cancels the local notification only, with no network call. */
    data class Dismiss(override val label: String) : InlineAction
}

/**
 * Turns an FCM data payload (keyed on `event_type`, `severity` and `job_id`, as sent by the
 * server's `buildPushPayload`) into a notification builder with at most one safe inline action.
 */
object ActionableNotificationBuilder {

    fun build(context: Context, data: Map<String, String>): NotificationCompat.Builder {
        val eventType = data["event_type"].orEmpty()
        val jobId = data["job_id"]
        val severity = data["severity"]
        val notificationId = notificationIdFor(eventType, jobId)

        val builder = NotificationCompat.Builder(context, NotificationChannels.channelFor(eventType))
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(data["title"] ?: defaultTitleFor(eventType))
            .setContentText(data["body"].orEmpty())
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .setContentIntent(contentPendingIntent(context, eventType, jobId))

        when (val action = inlineActionFor(eventType, severity)) {
            is InlineAction.OpenScreen ->
                builder.addAction(0, action.label, viewLogPendingIntent(context, eventType, jobId))
            is InlineAction.Dismiss ->
                builder.addAction(0, action.label, dismissPendingIntent(context, notificationId))
            null -> Unit
        }

        return builder
    }

    /** Id to post [build]'s notification under, so [InlineAction.Dismiss] cancels the right one. */
    fun notificationIdFor(eventType: String, jobId: String?): Int = (jobId ?: eventType).hashCode()

    internal fun defaultTitleFor(eventType: String): String = when {
        eventType.startsWith("job.") -> "Job"
        eventType.startsWith("metric.") -> "Alert"
        else -> "Server Control Panel"
    }

    internal fun routeFor(eventType: String): String =
        if (eventType.startsWith("job.")) NotificationDeepLink.ROUTE_DEPLOY_JOB else NotificationDeepLink.ROUTE_ALERT

    internal fun contentIntent(context: Context, eventType: String, jobId: String?): Intent {
        val base = context.packageManager.getLaunchIntentForPackage(context.packageName) ?: Intent()
        return base.apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
            putExtra(NotificationDeepLink.EXTRA_ROUTE, routeFor(eventType))
            jobId?.let { putExtra(NotificationDeepLink.EXTRA_ENTITY_ID, it) }
        }
    }

    /**
     * Inline action whitelist: `job.*` gets a read-only "View log"; `metric.*` gets "Dismiss"
     * only for [ACK_ELIGIBLE_SEVERITIES]. Nothing here ever mutates server state.
     */
    internal fun inlineActionFor(eventType: String, severity: String?): InlineAction? = when {
        eventType.startsWith("job.") -> InlineAction.OpenScreen("View log")
        eventType.startsWith("metric.") && severity in ACK_ELIGIBLE_SEVERITIES -> InlineAction.Dismiss("Dismiss")
        else -> null
    }

    private fun contentPendingIntent(context: Context, eventType: String, jobId: String?): PendingIntent =
        PendingIntent.getActivity(
            context,
            REQUEST_CODE_CONTENT + notificationIdFor(eventType, jobId),
            contentIntent(context, eventType, jobId),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

    private fun viewLogPendingIntent(context: Context, eventType: String, jobId: String?): PendingIntent =
        PendingIntent.getActivity(
            context,
            REQUEST_CODE_VIEW_LOG + notificationIdFor(eventType, jobId),
            contentIntent(context, eventType, jobId),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

    private fun dismissPendingIntent(context: Context, notificationId: Int): PendingIntent {
        val intent = Intent(context, NotificationActionReceiver::class.java).apply {
            putExtra(NotificationActionReceiver.EXTRA_NOTIFICATION_ID, notificationId)
        }
        return PendingIntent.getBroadcast(
            context,
            REQUEST_CODE_DISMISS + notificationId,
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }
}
