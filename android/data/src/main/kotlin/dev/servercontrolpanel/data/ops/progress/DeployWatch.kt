package dev.servercontrolpanel.data.ops.progress

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
import dev.servercontrolpanel.data.ops.DeployStatusResult
import dev.servercontrolpanel.data.ops.OpsRepository
import kotlinx.coroutines.delay

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

        private const val INTERVAL_MS = 3_000L

        private const val MAX_READS = 300

        private const val FAILED_READS_BEFORE_GIVING_UP = 3

        private const val WORK_NAME = "deploy-watch"

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

        fun finished(state: String): Boolean =
            state.lowercase() in setOf("ok", "success", "succeeded", "done", "failed", "error", "rolled_back", "cancelled", "canceled")

        fun succeeded(state: String): Boolean =
            state.lowercase() in setOf("ok", "success", "succeeded", "done")
    }
}

private const val CHANNEL = "panel_progress"
private const val END_ID = 0x0DEB

private fun ensureChannel(context: Context) {
    val manager = context.getSystemService(NotificationManager::class.java) ?: return
    manager.createNotificationChannel(
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
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build(),
    )
}

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
