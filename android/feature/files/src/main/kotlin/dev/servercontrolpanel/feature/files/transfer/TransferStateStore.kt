package dev.servercontrolpanel.feature.files.transfer

import android.content.Context
import android.content.SharedPreferences
import dev.servercontrolpanel.data.files.UploadResumeStore

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

    private val uploadResume = UploadResumeStore(prefs)

    fun uploadState(workName: String): UploadState? =
        uploadResume.state(workName)?.let { UploadState(sessionId = it.sessionId, bytesUploaded = it.bytesUploaded) }

    fun saveUploadSession(workName: String, sessionId: String) = uploadResume.saveSession(workName, sessionId)

    fun saveUploadProgress(workName: String, bytesUploaded: Long) = uploadResume.saveProgress(workName, bytesUploaded)

    fun clearUpload(workName: String) = uploadResume.clear(workName)

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
        const val KEY_SESSION_ID = UploadResumeStore.KEY_SESSION_ID
        const val KEY_BYTES_UPLOADED = UploadResumeStore.KEY_BYTES_UPLOADED
    }
}

data class DownloadState(val mediaUri: String)
data class UploadState(val sessionId: String, val bytesUploaded: Long)
