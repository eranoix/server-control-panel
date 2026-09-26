package com.vpsmanager.app.storage

import android.content.Context
import android.util.Log
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.vpsmanager.data.storage.AppStorage
import com.vpsmanager.data.storage.DeviceStorageAreas
import java.util.concurrent.TimeUnit

/**
 * Daily storage cleanup (old temporary files and packages of installed versions). It runs in
 * WorkManager rather than at launch, when temporary files are most likely in use and the user
 * is waiting. The sweep is cheap, so the only constraint is a battery that is not low.
 * It never retries: a file still open today is picked up by tomorrow's pass.
 */
class MaintenanceWorker(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val result = runCatching {
            // No files are marked in use: update downloads run in the foreground, never
            // alongside this worker. If that changes, this call must be told.
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
        private const val TAG = "VPSMManutencao"
        private const val WORK_NAME = "manutencao-de-armazenamento"

        fun enqueue(context: Context) {
            val request = PeriodicWorkRequestBuilder<MaintenanceWorker>(1, TimeUnit.DAYS)
                .setConstraints(Constraints.Builder().setRequiresBatteryNotLow(true).build())
                .build()
            WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                WORK_NAME,
                // KEEP, never REPLACE: replacing on every app open restarts the period,
                // so a daily-used app would never run the cleanup.
                ExistingPeriodicWorkPolicy.KEEP,
                request,
            )
        }
    }
}
