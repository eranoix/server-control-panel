package dev.servercontrolpanel.feature.files.transfer

/**
 * Cleans up what a cancelled transfer leaves behind. Cancellation throws out of `doWork()` before
 * the workers' own cleanup runs, leaving a MediaStore row stuck at `IS_PENDING=1` or orphaned
 * upload state in [TransferStateStore].
 *
 * Free of Android types so it is unit-testable: callers inject the work-activity check and the
 * pending-download deletion.
 */
class TransferGarbageCollector(private val stateStore: TransferStateStore) {

    /**
     * Cleans up one transfer unconditionally, for an explicit cancel. Once the work is
     * `CANCELLED` the worker has stopped, so there is no writer to race with.
     */
    fun cleanupCancelled(workName: String, deletePendingDownload: (mediaUri: String) -> Unit) {
        removeWorkNameGarbage(workName, deletePendingDownload)
    }

    /**
     * At app start, removes state for every tracked transfer that is no longer active, covering
     * process death or force-stop before a terminal state was observed.
     *
     * @param isWorkActive `true` for work still enqueued, running or blocked, which must not be touched.
     * @return the work names cleaned up.
     */
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
        // Clearing the absent kind is a no-op, so no need to branch on the prefix.
        stateStore.downloadState(workName)?.let { deletePendingDownload(it.mediaUri) }
        stateStore.clearDownload(workName)
        stateStore.clearUpload(workName)
    }
}
