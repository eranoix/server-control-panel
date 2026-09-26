package com.vpsmanager.feature.notifications.fcm

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import com.vpsmanager.feature.notifications.R

/**
 * Route/entity-id extras a tapped notification's content [Intent] carries so the `:app`-level
 * nav host opens directly to the already-subscribed target screen instead of the home screen.
 * Only an opaque route id + entity id travel here — never a screen-bypassing auth token or
 * capability flag (accepted: the target screen re-authorizes on load regardless of how it was
 * reached).
 */
object NotificationDeepLink {
    const val EXTRA_ROUTE = "vpsm_notification_route"
    const val EXTRA_ENTITY_ID = "vpsm_notification_entity_id"
    const val ROUTE_DEPLOY_JOB = "deploy_job"
    const val ROUTE_ALERT = "alert"
}

/** Metric severities eligible for the inline "Dispensar" action. `critical` is deliberately
 * excluded — it only ever gets the notification's own content-tap "Abrir". */
private val ACK_ELIGIBLE_SEVERITIES = setOf("info", "warning")

private const val REQUEST_CODE_CONTENT = 100
private const val REQUEST_CODE_VIEW_LOG = 200
private const val REQUEST_CODE_DISMISS = 300

/** The safe-inline-action whitelist result for one notification. Never a state-mutating
 * action ("Refazer deploy"/"Reiniciar") — those require `DeployTriggerScreen`'s own confirmation
 * gate and are never offered from a notification. */
internal sealed interface InlineAction {
    val label: String

    /** Opens the same already-subscribed target screen the content tap opens — read-only,
     * safe regardless of severity (e.g. "Ver log" for a failed job). */
    data class OpenScreen(override val label: String) : InlineAction

    /** Cancels the local notification only; fires no network call (see
     * [NotificationActionReceiver]'s doc for why no real ack endpoint exists yet). */
    data class Dismiss(override val label: String) : InlineAction
}

/**
 * Shapes an FCM data payload — the real `buildPushPayload` contract from
 * `internal/notify/pushchannel.go` (keyed on `event_type`/`severity`/`job_id`, NOT the
 * underscore-separated `type` field an earlier draft of the interface assumed) — into a
 * `NotificationCompat.Builder` plus at most one safe inline action.
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

    /** The id [build]'s notification must be posted under so [InlineAction.Dismiss] cancels the
     * exact same notification it was attached to. */
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
     * The safe-inline-action whitelist: `job.*` events always get a read-only "Ver log"
     * action (opens the target screen, no server mutation — safe regardless of severity).
     * `metric.*` events only get an inline "Dispensar" action when severity is in
     * [ACK_ELIGIBLE_SEVERITIES]; `critical` never does. No combination here ever produces a
     * state-mutating action.
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
