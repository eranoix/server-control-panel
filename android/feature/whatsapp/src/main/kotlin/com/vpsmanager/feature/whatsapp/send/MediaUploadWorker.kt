package com.vpsmanager.feature.whatsapp.send

import com.vpsmanager.data.whatsapp.UploadResult
import com.vpsmanager.data.whatsapp.WhatsAppRepository
import java.io.File

/** Same 100 MiB cap the BFF enforces server-side (its `413`) -- checked client-side first so an obviously oversized file never opens a socket at all. */
const val MAX_UPLOAD_BYTES: Long = 100L * 1024 * 1024

/**
 * Outcome of one [MediaUploadWorker.upload] attempt. [Failed.retryable] is
 * `false` for exactly the cases where retrying with the same bytes cannot
 * possibly succeed -- over the size cap, whether caught client-side before a
 * request was even made or reported by the server as a `413` -- so the UI
 * shows the same non-retryable state either way instead of offering a retry
 * button that will only fail again.
 */
sealed interface MediaSendOutcome {
    data class Success(val id: String) : MediaSendOutcome
    data class Failed(val reason: String, val retryable: Boolean) : MediaSendOutcome
}

/**
 * Monotonic clamp for upload progress: never lets a new reading regress
 * below what was already reported. Needed because [WhatsAppRepository]'s
 * progress callback reports the *current* attempt's byte count, and a
 * retry's callback starts back at 0 -- without this clamp a bubble's percent
 * would visibly jump backwards on every retry, which is the "must never
 * reset progress to 0 mid-flight" requirement this plan calls out. Pure and
 * directly unit-testable on its own, independent of any coroutine/UI
 * plumbing.
 */
fun clampUploadProgress(previous: Int?, next: Int): Int = maxOf(previous ?: 0, next).coerceIn(0, 100)

/**
 * Wraps [WhatsAppRepository.uploadMedia] with the client-side size pre-check --
 * a plain suspend-function class, not a `CoroutineWorker`: the BFF's media
 * endpoint is a single-shot multipart POST (unlike `:feature-files`'
 * chunked/resumable session protocol), and this design wires every send into
 * [com.vpsmanager.feature.whatsapp.ConversationViewModel]'s existing in-memory
 * optimistic-bubble/reconciliation mechanism rather than a background-persistent
 * WorkManager+notification flow.
 */
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
