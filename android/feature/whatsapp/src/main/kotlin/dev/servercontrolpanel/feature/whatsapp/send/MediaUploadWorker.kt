package dev.servercontrolpanel.feature.whatsapp.send

import dev.servercontrolpanel.data.whatsapp.UploadResult
import dev.servercontrolpanel.data.whatsapp.WhatsAppRepository
import java.io.File

const val MAX_UPLOAD_BYTES: Long = 100L * 1024 * 1024

sealed interface MediaSendOutcome {
    data class Success(val id: String) : MediaSendOutcome
    data class Failed(val reason: String, val retryable: Boolean) : MediaSendOutcome
}

fun clampUploadProgress(previous: Int?, next: Int): Int = maxOf(previous ?: 0, next).coerceIn(0, 100)

open class MediaUploadWorker(
    private val repository: WhatsAppRepository,
) {
    open suspend fun upload(
        jid: String,
        clientMsgId: String,
        file: File,
        sizeBytes: Long,
        mimeType: String?,
        msgType: String,
        caption: String? = null,
        quotedId: String? = null,
        onProgress: (percent: Int) -> Unit = {},
    ): MediaSendOutcome {
        if (sizeBytes > MAX_UPLOAD_BYTES) {
            return MediaSendOutcome.Failed(
                reason = "The file exceeds the 100 MB limit.",
                retryable = false,
            )
        }
        return when (val result = repository.uploadMedia(
            jid = jid,
            clientMsgId = clientMsgId,
            file = file,
            caption = caption,
            msgType = msgType,
            quotedId = quotedId,
            onProgress = onProgress,
        )) {
            is UploadResult.Success -> MediaSendOutcome.Success(result.id)
            is UploadResult.Error -> MediaSendOutcome.Failed(reason = result.reason, retryable = !result.overCap)
        }
    }
}
