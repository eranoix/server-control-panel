package dev.servercontrolpanel.data.files

import android.content.Context
import android.content.SharedPreferences

data class UploadResumeState(val sessionId: String, val bytesUploaded: Long)

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
        const val PREFS_NAME = "transfer_state"
        const val KEY_SESSION_ID = "session_id"
        const val KEY_BYTES_UPLOADED = "bytes_uploaded"
    }
}
