package dev.servercontrolpanel.feature.files.transfer

class TransferGarbageCollector(private val stateStore: TransferStateStore) {

    fun cleanupCancelled(workName: String, deletePendingDownload: (mediaUri: String) -> Unit) {
        removeWorkNameGarbage(workName, deletePendingDownload)
    }

    fun sweep(isWorkActive: (workName: String) -> Boolean, deletePendingDownload: (mediaUri: String) -> Unit): Set<String> {
        val removed = mutableSetOf<String>()
        for (workName in stateStore.allTrackedWorkNames()) {
            if (isWorkActive(workName)) continue
            removeWorkNameGarbage(workName, deletePendingDownload)
            removed += workName
        }
        return removed
    }

    private fun removeWorkNameGarbage(workName: String, deletePendingDownload: (mediaUri: String) -> Unit) {
        stateStore.downloadState(workName)?.let { deletePendingDownload(it.mediaUri) }
        stateStore.clearDownload(workName)
        stateStore.clearUpload(workName)
    }
}
