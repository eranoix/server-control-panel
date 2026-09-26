package com.vpsmanager.feature.files.transfer

/**
 * Cleans up client-side garbage a cancelled transfer leaves behind:
 * WorkManager cancellation throws `CancellationException` out of
 * `doWork()` before it reaches any of [DownloadWorker]/[UploadWorker]'s own
 * terminal-state cleanup (`markComplete` + `clearDownload`, or `clearUpload`)
 * -- so a cancelled download leaves its MediaStore row stuck at
 * `IS_PENDING=1` forever, and a cancelled/abandoned upload leaves its
 * session id and byte count orphaned in [TransferStateStore] forever.
 *
 * Kept free of `Context`/`WorkManager`/`ContentResolver` so it is a plain
 * unit-testable class: callers pass an `isWorkActive` check (backed by
 * `WorkManager.getWorkInfosForUniqueWork` in production) and a
 * `deletePendingDownload` callback (backed by `ContentResolver.delete` in
 * production) instead of this class touching either directly.
 */
class TransferGarbageCollector(private val stateStore: TransferStateStore) {

    /**
     * Cleans up exactly one transfer, unconditionally -- meant for the
     * explicit "Cancelar" action, where the caller already knows the work is
     * being given up on (no activity check needed: by the time WorkManager's
     * `WorkInfo` for `workName` reaches `CANCELLED`, the worker has fully
     * stopped, so there is no live writer left to race against).
     */
    fun cleanupCancelled(workName: String, deletePendingDownload: (mediaUri: String) -> Unit) {
        removeWorkNameGarbage(workName, deletePendingDownload)
    }

    /**
     * Sweeps every work name this device has ever persisted state for and
     * removes any whose transfer is no longer active -- meant for app start,
     * covering the case where the process died (or the app was force-stopped)
     * before an explicit cancel, or before WorkManager ever reached a
     * terminal state the app observed.
     *
     * @param isWorkActive backed by `WorkManager.getWorkInfosForUniqueWork` in
     *   production -- `true` for any work name still enqueued/running/blocked,
     *   which must never be touched.
     * @return the work names that were cleaned up, for logging/testing.
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
        // A work name is either a download or an upload, never both -- but
        // clearing whichever one is absent is a no-op, so there is no need
        // to branch on the "download:"/"upload:" prefix here.
        stateStore.downloadState(workName)?.let { deletePendingDownload(it.mediaUri) }
        stateStore.clearDownload(workName)
        stateStore.clearUpload(workName)
    }
}
