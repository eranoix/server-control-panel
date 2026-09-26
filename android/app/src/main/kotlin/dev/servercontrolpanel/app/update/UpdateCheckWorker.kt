package dev.servercontrolpanel.app.update

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import dev.servercontrolpanel.app.PanelApplication
import java.util.concurrent.TimeUnit

/**
 * The periodic update check. It only fetches `GET /app/update` (a small JSON); the download
 * starts only when the user taps the banner. The system can defer periodic work for a long
 * time, so [dev.servercontrolpanel.app.MainActivity] also checks at launch.
 */
class UpdateCheckWorker(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val app = applicationContext as? PanelApplication ?: return Result.success()
        // `check` swallows its own errors; the next periodic window is the retry.
        app.updateCoordinator.check()
        return Result.success()
    }

    companion object {
        private const val WORK_NAME = "panel-update-check"

        /** Idempotent: `KEEP` preserves the existing schedule across app restarts. */
        fun enqueue(context: Context) {
            val request = PeriodicWorkRequestBuilder<UpdateCheckWorker>(6, TimeUnit.HOURS)
                .setConstraints(
                    Constraints.Builder()
                        .setRequiredNetworkType(NetworkType.CONNECTED)
                        .build(),
                )
                .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                WORK_NAME,
                ExistingPeriodicWorkPolicy.KEEP,
                request,
            )
        }
    }
}
