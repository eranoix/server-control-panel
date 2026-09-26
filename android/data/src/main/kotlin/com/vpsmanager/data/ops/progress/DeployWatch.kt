package com.vpsmanager.data.ops.progress

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import androidx.core.app.NotificationCompat
import androidx.work.CoroutineWorker
import androidx.work.Data
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.vpsmanager.data.ops.DeployStatusResult
import com.vpsmanager.data.ops.OpsRepository
import kotlinx.coroutines.delay

/**
 * Follows a deploy through to the end and shows its progress in a notification.
 *
 * ## The debt this pays
 *
 * Triggering a deploy from the phone left the person **with no signal at all
 * for up to ten minutes**: the screen showed "sent" and then nothing, because
 * leaving the app kills the socket and Android 15 cuts the process's network in
 * the background after ~5.7 s. Whoever triggers a deploy from a phone almost
 * always triggers it and puts the phone away — which is exactly the gesture the
 * app did not cover.
 *
 * ## Why a notification and not a screen
 *
 * The value of a long deploy is in **not having to watch**. A screen that has
 * to stay open to follow along follows nothing — it holds the person captive. A
 * progress notification is the opposite: it seeks the person out when the state
 * changes, and disappears by itself when it is over.
 *
 * ## Why this is the foreground-service use that survived
 *
 * Android 13/14/15 closed almost every door to background work; `dataSync` with
 * a foreground service is capped at 6 h a day on Android 15 and is being
 * deprecated. A deploy lasts minutes and has a **beginning, visible progress
 * and an end** — it is the case the platform still accepts readily, and
 * `WorkManager` handles the promotion to the foreground by itself.
 *
 * ## The end is always stated
 *
 * Success, failure and "I gave up asking" produce different notifications. A
 * progress bar that simply disappears is indistinguishable from an app that
 * died — and after a deploy, "I don't know what happened" is the worst possible
 * state.
 */
class DeployWatchWorker(
    context: Context,
    params: WorkerParameters,
    private val repository: OpsRepository = OpsRepository(),
) : CoroutineWorker(context, params) {

    override suspend fun getForegroundInfo(): ForegroundInfo =
        progressInfo(applicationContext, id.hashCode(), "Deploy in progress", null, null)

    override suspend fun doWork(): Result {
        val jobId = inputData.getString(KEY_JOB) ?: return Result.failure()
        val app = inputData.getString(KEY_APP).orEmpty()

        setForeground(getForegroundInfo())

        var attemptsWithoutResponse = 0
        repeat(MAX_READS) {
            when (val r = repository.fetchDeployStatus(jobId)) {
                is DeployStatusResult.Success -> {
                    attemptsWithoutResponse = 0
                    val s = r.status
                    if (finished(s.status)) {
                        notifyFinish(applicationContext, app, s.status, s.error)
                        return Result.success()
                    }
                    setForeground(
                        progressInfo(
                            applicationContext,
                            id.hashCode(),
                            titleOf(app),
                            s.step,
                            s.progress.toInt(),
                        ),
                    )
                }
                is DeployStatusResult.Error -> {
                    // A READ failure is not a deploy failure: the device's
                    // network may have blinked while the server carries on
                    // working. Giving up on the first one would report the
                    // wrong news about the most expensive thing on the screen.
                    attemptsWithoutResponse++
                    if (attemptsWithoutResponse >= FAILED_READS_BEFORE_GIVING_UP) {
                        notifyLostContact(applicationContext, app)
                        return Result.success()
                    }
                }
            }
            delay(INTERVAL_MS)
        }
        notifyLostContact(applicationContext, app)
        return Result.success()
    }

    private fun titleOf(app: String) = if (app.isBlank()) "Deploy" else "Deploy · $app"

    companion object {
        const val KEY_JOB = "job_id"
        const val KEY_APP = "app"

        /**
         * 3 s. It is the same interval as the server's `/ops/status` cache:
         * asking faster brings back no new number, it only burns radio.
         */
        private const val INTERVAL_MS = 3_000L

        /** ~15 min of following along. A deploy longer than that is news in itself. */
        private const val MAX_READS = 300

        /** Three failures in a row (~9 s) is no longer a network blink. */
        private const val FAILED_READS_BEFORE_GIVING_UP = 3

        private const val WORK_NAME = "acompanha-deploy"

        /**
         * Starts following [jobId].
         *
         * `REPLACE` and not `APPEND`: two deploys followed at once would
         * produce two progress bars competing for the same row of the shade,
         * and the second is always the one that matters.
         */
        fun track(context: Context, jobId: String, app: String) {
            val request = OneTimeWorkRequestBuilder<DeployWatchWorker>()
                .setInputData(workData(jobId, app))
                .build()
            WorkManager.getInstance(context)
                .beginUniqueWork(WORK_NAME, ExistingWorkPolicy.REPLACE, request)
                .enqueue()
        }

        fun workData(jobId: String, app: String): Data =
            workDataOf(KEY_JOB to jobId, KEY_APP to app)

        /**
         * Whether this state is final.
         *
         * An ALLOW LIST of terminal states, with everything else being "still
         * running". The opposite — listing the in-flight states — would make a
         * new server state (`verifying`, say) read as an ending, and the
         * notification would announce a finished deploy that is still mid-way.
         */
        fun finished(state: String): Boolean =
            state.lowercase() in setOf("ok", "success", "succeeded", "done", "failed", "error", "rolled_back", "cancelled", "canceled")

        fun succeeded(state: String): Boolean =
            state.lowercase() in setOf("ok", "success", "succeeded", "done")
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// The notifications
// ─────────────────────────────────────────────────────────────────────────────

private const val CHANNEL = "vpsm_progresso"
private const val END_ID = 0x0DEB

private fun ensureChannel(context: Context) {
    val manager = context.getSystemService(NotificationManager::class.java) ?: return
    manager.createNotificationChannel(
        // IMPORTANCE_LOW: progress neither rings nor vibrates. A ten-minute
        // deploy that beeped at every step would be uninstalled on day two.
        NotificationChannel(CHANNEL, "Task progress", NotificationManager.IMPORTANCE_LOW),
    )
}

private fun progressInfo(
    context: Context,
    id: Int,
    title: String,
    step: String?,
    percent: Int?,
): ForegroundInfo {
    ensureChannel(context)
    val b = NotificationCompat.Builder(context, CHANNEL)
        .setContentTitle(title)
        .setSmallIcon(android.R.drawable.stat_sys_upload)
        .setOngoing(true)
        .setOnlyAlertOnce(true)
    if (percent != null && percent in 0..100) {
        b.setProgress(100, percent, false)
        // The STEP is worth more than the number. "78%" does not tell you
        // whether you can breathe; "restarting the service" does. The
        // percentage goes alongside it, never alone.
        b.setContentText(step?.takeIf { it.isNotBlank() }?.let { "$it · $percent%" } ?: "$percent%")
    } else {
        b.setProgress(0, 0, true)
        b.setContentText(step?.takeIf { it.isNotBlank() } ?: "In progress…")
    }
    return ForegroundInfo(id, b.build(), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
}

private fun notifyFinish(context: Context, app: String, state: String, error: String?) {
    ensureChannel(context)
    val ok = DeployWatchWorker.succeeded(state)
    val manager = context.getSystemService(NotificationManager::class.java) ?: return
    manager.notify(
        END_ID,
        NotificationCompat.Builder(context, CHANNEL)
            .setContentTitle(if (ok) "Deploy finished" else "Deploy failed")
            .setContentText(
                buildString {
                    if (app.isNotBlank()) append("$app · ")
                    append(state)
                    error?.takeIf { it.isNotBlank() }?.let { append(" · $it") }
                },
            )
            .setSmallIcon(android.R.drawable.stat_sys_upload_done)
            .setAutoCancel(true)
            // THE END ALERTS, the progress does not. It is the only news in
            // this series that changes what the person does next.
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build(),
    )
}

/**
 * The most important case of all: the app lost contact and **does not know**
 * how the deploy ended.
 *
 * Saying so is mandatory. A bar that vanishes in silence is read as "it
 * finished fine" — and announcing a success you did not verify is the worst
 * thing a deploy notification can do.
 */
private fun notifyLostContact(context: Context, app: String) {
    ensureChannel(context)
    val manager = context.getSystemService(NotificationManager::class.java) ?: return
    manager.notify(
        END_ID,
        NotificationCompat.Builder(context, CHANNEL)
            .setContentTitle("No news from the deploy")
            .setContentText(
                (if (app.isNotBlank()) "$app · " else "") +
                    "the app can no longer check on it. The deploy may have finished — open to check.",
            )
            .setStyle(NotificationCompat.BigTextStyle())
            .setSmallIcon(android.R.drawable.stat_sys_warning)
            .setAutoCancel(true)
            .build(),
    )
}
