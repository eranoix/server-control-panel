package dev.servercontrolpanel.app.storage

import android.content.Context
import android.util.Log
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import dev.servercontrolpanel.data.storage.AppStorage
import dev.servercontrolpanel.data.storage.DeviceStorageAreas
import java.util.concurrent.TimeUnit

class MaintenanceWorker(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val result = runCatching {
            DeviceStorageAreas.maintenance(applicationContext)
        }.getOrElse {
            Log.w(TAG, "storage maintenance failed", it)
            return Result.success()
        }

        if (result.filesRemoved > 0) {
            Log.i(
                TAG,
                "maintenance: ${result.filesRemoved} file(s), " +
                    "${AppStorage.formatBytes(result.freedBytes)} freed",
            )
        }
        return Result.success()
    }

    companion object {
        private const val TAG = "PanelMaintenance"
        private const val WORK_NAME = "storage-maintenance"

        fun enqueue(context: Context) {
            val request = PeriodicWorkRequestBuilder<MaintenanceWorker>(1, TimeUnit.DAYS)
                .setConstraints(Constraints.Builder().setRequiresBatteryNotLow(true).build())
                .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                WORK_NAME,
                ExistingPeriodicWorkPolicy.KEEP,
                request,
            )
        }
    }
}
