package com.vpsmanager.feature.files.transfer

import android.content.Context
import android.net.Uri
import androidx.work.WorkInfo
import androidx.work.WorkManager

/**
 * Wires [TransferGarbageCollector] to the real `WorkManager`/`ContentResolver`
 * -- the seam callers outside this module use instead of depending on
 * `androidx.work` or this feature's internals directly. Meant to be called
 * once per process start, off the main thread (this does a blocking
 * `WorkInfo` lookup per tracked transfer).
 */
object TransferMaintenance {
    fun sweepAbandonedTransfers(context: Context) {
        val appContext = context.applicationContext
        val stateStore = TransferStateStore(appContext)
        val workManager = WorkManager.getInstance(appContext)
        TransferGarbageCollector(stateStore).sweep(
            isWorkActive = { workName ->
                workManager.getWorkInfosForUniqueWork(workName).get().any {
                    it.state == WorkInfo.State.RUNNING ||
                        it.state == WorkInfo.State.ENQUEUED ||
                        it.state == WorkInfo.State.BLOCKED
                }
            },
            deletePendingDownload = { mediaUri ->
                appContext.contentResolver.delete(Uri.parse(mediaUri), null, null)
            },
        )
    }
}
