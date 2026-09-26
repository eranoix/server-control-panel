package dev.servercontrolpanel.data.files

import java.io.IOException
import java.io.InputStream

/**
 * The default size of each chunk sent. 1 MiB is the value the
 * `/files/upload/{init,chunk,complete}` protocol was designed around: large
 * enough that per-request overhead does not dominate on a file of tens of MB,
 * small enough that a dropped connection costs, at worst, a single chunk resent
 * — and not the whole file.
 */
const val DEFAULT_UPLOAD_CHUNK_SIZE_BYTES: Int = 1 * 1024 * 1024

/**
 * Where the upload's bytes come from. An interface, and not a `java.io.File` or
 * an `android.net.Uri`, because the two callers have different origins (a
 * `content://` from the system picker, a cache file from the camera) and this
 * module should know neither. [open] returns a stream positioned at byte 0 —
 * [ChunkedUploadPump] itself advances to the resume point.
 *
 * `null` is a legitimate and expected answer: the source is gone (the user
 * deleted the photo) or read permission was revoked between picking and
 * sending.
 */
fun interface UploadByteSource {
    fun open(): InputStream?
}

/**
 * The outcome of a chunked upload. The three cases are kept separate because
 * the caller does DIFFERENT things with each, and a `Boolean` (or a generic
 * exception) would erase precisely that difference:
 *
 * - [Completed] — done; there is a path on the server to show or use.
 * - [Interrupted] — failed for something that passes (network dropped, server
 *   down). The progress in [bytesSent] is real and already durable on the
 *   server: trying again resumes from there, not from zero.
 * - [Refused] — failed for something that trying again will NOT fix (file over
 *   the cap, disk full, no permission on the folder). Repeating here only burns
 *   battery and hides from the operator what they need to fix.
 */
sealed interface ChunkedUploadOutcome {
    data class Completed(val path: String) : ChunkedUploadOutcome
    data class Interrupted(val bytesSent: Long, val reason: String) : ChunkedUploadOutcome
    data class Refused(val reason: String) : ChunkedUploadOutcome
}

/**
 * The chunked-upload loop — the app's ONLY implementation of it.
 *
 * It used to exist only inside `UploadWorker` (`:feature-files`). When the
 * terminal gained attachments (sending an image to the assistant on the other
 * side of the session), copying that loop over would have created two copies of
 * the same delicate logic — resume by offset, the server's authoritative count,
 * error translation — which would age at different rates. A feature module may
 * not depend on another (there is no such edge in the project), so the loop
 * moved down into `:data`, next to the [TransferRepository] it already used.
 * The two callers keep what is genuinely theirs: where to store the resume
 * state and what to do with the outcome.
 *
 * What this object deliberately does NOT do: create the upload session. Each
 * caller has its own policy for the destination name and its own memory of an
 * in-flight session; forcing both through here would only couple them back
 * together.
 */
open class ChunkedUploadPump(
    private val transferRepository: TransferRepository,
    private val chunkSizeBytes: Int = DEFAULT_UPLOAD_CHUNK_SIZE_BYTES,
) {

    /**
     * Sends what remains of session [sessionId] from [startOffset] and
     * finalises it.
     *
     * [onProgress] is called on every chunk the server ACCEPTS, never on every
     * chunk read — whoever persists that number needs it in order to resume,
     * and resuming from a byte the server never confirmed would assemble a file
     * with a hole in it.
     *
     * `CancellationException` (the operator cancelling) passes through this
     * method on purpose: no `catch` here is broad enough to swallow it, or a
     * cancelled upload would carry on to the end.
     */
    open suspend fun send(
        source: UploadByteSource,
        sessionId: String,
        startOffset: Long,
        totalSize: Long,
        onProgress: suspend (Long) -> Unit,
    ): ChunkedUploadOutcome {
        var offset = startOffset
        try {
            val opened = source.open()
                ?: return ChunkedUploadOutcome.Refused(
                    "The app lost access to the chosen file. Choose the file again.",
                )
            opened.use { input ->
                skipFully(input, startOffset)
                val buffer = ByteArray(chunkSizeBytes)
                while (true) {
                    val read = input.read(buffer)
                    if (read == -1) break
                    when (val result = transferRepository.uploadChunk(sessionId, offset, buffer.copyOf(read))) {
                        is UploadChunkResult.Accepted -> Unit
                        is UploadChunkResult.Rejected -> return if (result.retryable) {
                            ChunkedUploadOutcome.Interrupted(bytesSent = offset, reason = result.reason)
                        } else {
                            ChunkedUploadOutcome.Refused(result.reason)
                        }
                    }
                    offset += read
                    onProgress(offset)
                }
            }
        } catch (e: IOException) {
            return ChunkedUploadOutcome.Interrupted(
                bytesSent = offset,
                reason = "Failed to read the file on the device. Try again.",
            )
        } catch (e: SecurityException) {
            return ChunkedUploadOutcome.Refused(
                "The app lost permission to read the chosen file. Choose the file again.",
            )
        }

        return when (val result = transferRepository.completeUpload(sessionId)) {
            is UploadCompleteResult.Success -> ChunkedUploadOutcome.Completed(result.path)
            // The server's count always overrides ours: it is the only one
            // that knows what became durable on disk.
            is UploadCompleteResult.Incomplete -> ChunkedUploadOutcome.Interrupted(
                bytesSent = result.receivedBytes,
                reason = "The upload is still incomplete. It will resume where it left off.",
            )
            is UploadCompleteResult.Error -> if (result.retryable) {
                ChunkedUploadOutcome.Interrupted(bytesSent = offset, reason = result.reason)
            } else {
                ChunkedUploadOutcome.Refused(result.reason)
            }
        }
    }

    /**
     * `InputStream.skip` may skip LESS than asked without that being an error
     * (its contract allows it), so skipping just once would leave the offset
     * wrong and assemble the wrong file. Skipping in a loop until it arrives
     * (or until the stream declares it will go no further) is the only correct
     * use.
     */
    private fun skipFully(input: InputStream, byteCount: Long) {
        var remaining = byteCount
        while (remaining > 0) {
            val skipped = input.skip(remaining)
            if (skipped <= 0) break
            remaining -= skipped
        }
    }
}
