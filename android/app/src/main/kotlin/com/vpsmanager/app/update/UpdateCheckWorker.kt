package com.vpsmanager.app.update

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.vpsmanager.app.VpsManagerApplication
import java.util.concurrent.TimeUnit

/**
 * The periodic update check.
 *
 * ### What this worker does NOT do
 * It downloads nothing. All that runs here is `GET /app/update`, a JSON of
 * a few hundred bytes — the 1.4 or 10 MB download only starts when the
 * owner TAPS the banner. On a bad connection, an app that downloads on its
 * own is an app that uninstalls itself.
 *
 * ### Why periodic AND at launch
 * `WorkManager`'s minimum period is 15 minutes and the system stretches it
 * at will to save battery: a device that goes days without network can
 * spend a long time without a run. The check at launch (see
 * [com.vpsmanager.app.MainActivity]) covers exactly that hole, and the two
 * together cost one small JSON.
 */
class UpdateCheckWorker(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val app = applicationContext as? VpsManagerApplication ?: return Result.success()
        // `check` swallows its own errors (a "could not check" banner is noise,
        // not information), so there is no Result.retry to give here: the next
        // periodic window is the retry.
        app.updateCoordinator.check()
        return Result.success()
    }

    companion object {
        private const val WORK_NAME = "vpsm-verificacao-de-atualizacao"

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
