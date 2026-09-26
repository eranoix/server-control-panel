package com.vpsmanager.feature.files.transfer

import android.content.Context
import android.content.SharedPreferences
import com.vpsmanager.data.files.UploadResumeStore

/**
 * Durable per-transfer resume state, keyed by the same unique work name used
 * for `WorkManager.enqueueUniqueWork`.
 *
 * WorkManager's own `Data` mechanism cannot hold this: input `Data` is fixed
 * at enqueue time (it cannot be mutated while a worker is running or across
 * an automatic retry), and output `Data` only becomes durable once a worker
 * finishes -- neither fits a value that must be updated *during* a
 * long-running, possibly-retried attempt. `SharedPreferences` is the plain,
 * process-death-safe place for the small amount of state each transfer
 * needs (a URI or session id string, a byte count) -- no separate database
 * is warranted for values this small.
 *
 * Downloads deliberately do NOT persist a byte counter here: [DownloadWorker]
 * derives its resume offset from the MediaStore entry's actual on-disk size,
 * which is self-healing by construction. Only the MediaStore [android.net.Uri]
 * itself needs to survive across attempts.
 */
class TransferStateStore internal constructor(private val prefs: SharedPreferences) {
    constructor(context: Context) : this(
        context.applicationContext.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE),
    )

    fun downloadState(workName: String): DownloadState? {
        val uri = prefs.getString(key(workName, KEY_MEDIA_URI), null) ?: return null
        return DownloadState(mediaUri = uri)
    }

    fun saveDownloadUri(workName: String, mediaUri: String) {
        prefs.edit().putString(key(workName, KEY_MEDIA_URI), mediaUri).apply()
    }

    fun clearDownload(workName: String) {
        prefs.edit().remove(key(workName, KEY_MEDIA_URI)).apply()
    }

    // The UPLOAD half of this store now lives in :data
    // (UploadResumeStore), beside the ChunkedUploadPump that produces it —
    // attaching through the terminal needs exactly the same resume memory and
    // cannot depend on :feature-files. Delegating (rather than copying) is
    // what guarantees the two sides never diverge on the key format; the
    // preferences file and the keys stay identical, so an upload already in
    // flight remains resumable across the update.
    private val uploadResume = UploadResumeStore(prefs)

    fun uploadState(workName: String): UploadState? =
        uploadResume.state(workName)?.let { UploadState(sessionId = it.sessionId, bytesUploaded = it.bytesUploaded) }

    fun saveUploadSession(workName: String, sessionId: String) = uploadResume.saveSession(workName, sessionId)

    fun saveUploadProgress(workName: String, bytesUploaded: Long) = uploadResume.saveProgress(workName, bytesUploaded)

    fun clearUpload(workName: String) = uploadResume.clear(workName)

    /**
     * Every work name with at least one entry still persisted here, for
     * cleanup. `workName` itself may contain colons (e.g. `download:$path`
     * or `upload:$destDir/$filename`), so a key cannot be split back into
     * `workName`/`suffix` by its first or last colon -- it is recovered by
     * stripping the known `:$suffix` tail instead.
     */
    fun allTrackedWorkNames(): Set<String> {
        val suffixes = listOf(KEY_MEDIA_URI, KEY_SESSION_ID, KEY_BYTES_UPLOADED)
        return prefs.all.keys.mapNotNullTo(mutableSetOf()) { fullKey ->
            suffixes.firstNotNullOfOrNull { suffix ->
                fullKey.takeIf { it.endsWith(":$suffix") }?.removeSuffix(":$suffix")
            }
        }
    }

    private fun key(workName: String, suffix: String) = "$workName:$suffix"

    private companion object {
        const val PREFS_NAME = "transfer_state"
        const val KEY_MEDIA_URI = "media_uri"
        // The upload suffixes are UploadResumeStore's own — read from
        // there, and not redeclared here, so that allTrackedWorkNames is never
        // left looking for a key the other side has stopped writing.
        const val KEY_SESSION_ID = UploadResumeStore.KEY_SESSION_ID
        const val KEY_BYTES_UPLOADED = UploadResumeStore.KEY_BYTES_UPLOADED
    }
}

data class DownloadState(val mediaUri: String)
data class UploadState(val sessionId: String, val bytesUploaded: Long)
