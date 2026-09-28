package dev.servercontrolpanel.feature.files.transfer

import android.content.Context
import android.net.Uri
import androidx.work.WorkInfo
import androidx.work.WorkManager

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
