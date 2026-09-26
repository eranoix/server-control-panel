package com.vpsmanager.data.files

import android.content.Context
import android.content.SharedPreferences

/** What has to survive for an interrupted upload to carry on from where it stopped. */
data class UploadResumeState(val sessionId: String, val bytesUploaded: Long)

/**
 * The resume memory of a chunked upload, next to the [ChunkedUploadPump] that
 * produces it.
 *
 * **Why it is not WorkManager's `Data`.** The input `Data` is fixed at enqueue
 * time (it cannot be changed with the worker running, nor between one attempt
 * and the next) and the output one only becomes durable once the worker
 * finishes — neither of them serves a value that changes DURING a long,
 * repeated attempt. `SharedPreferences` is the simple, process-death-proof
 * place for the little each upload has to remember: a session id and a byte
 * count.
 *
 * The preferences file name and the key format are the SAME ones
 * `TransferStateStore` (`:feature-files`) used before it delegated here — on
 * purpose: an upload that was halfway through when the app was updated stays
 * resumable, instead of starting over because of a refactor.
 */
class UploadResumeStore(private val prefs: SharedPreferences) {
    constructor(context: Context) : this(
        context.applicationContext.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE),
    )

    fun state(workName: String): UploadResumeState? {
        val sessionId = prefs.getString(key(workName, KEY_SESSION_ID), null) ?: return null
        return UploadResumeState(sessionId = sessionId, bytesUploaded = prefs.getLong(key(workName, KEY_BYTES_UPLOADED), 0L))
    }

    fun saveSession(workName: String, sessionId: String) {
        prefs.edit()
            .putString(key(workName, KEY_SESSION_ID), sessionId)
            .putLong(key(workName, KEY_BYTES_UPLOADED), 0L)
            .apply()
    }

    fun saveProgress(workName: String, bytesUploaded: Long) {
        prefs.edit().putLong(key(workName, KEY_BYTES_UPLOADED), bytesUploaded).apply()
    }

    fun clear(workName: String) {
        prefs.edit()
            .remove(key(workName, KEY_SESSION_ID))
            .remove(key(workName, KEY_BYTES_UPLOADED))
            .apply()
    }

    private fun key(workName: String, suffix: String) = "$workName:$suffix"

    companion object {
        /**
         * Public because `TransferStateStore` (`:feature-files`) has to sweep
         * the SAME keys in order to list tracked transfers; if each side
         * declared its own, a rename on one side would leave the other looking
         * for a key nobody writes any more.
         */
        const val PREFS_NAME = "transfer_state"
        const val KEY_SESSION_ID = "session_id"
        const val KEY_BYTES_UPLOADED = "bytes_uploaded"
    }
}
