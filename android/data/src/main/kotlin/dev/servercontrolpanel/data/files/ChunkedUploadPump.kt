package dev.servercontrolpanel.data.files

import java.io.IOException
import java.io.InputStream

const val DEFAULT_UPLOAD_CHUNK_SIZE_BYTES: Int = 1 * 1024 * 1024

fun interface UploadByteSource {
    fun open(): InputStream?
}

sealed interface ChunkedUploadOutcome {
    data class Completed(val path: String) : ChunkedUploadOutcome
    data class Interrupted(val bytesSent: Long, val reason: String) : ChunkedUploadOutcome
    data class Refused(val reason: String) : ChunkedUploadOutcome
}

open class ChunkedUploadPump(
    private val transferRepository: TransferRepository,
    private val chunkSizeBytes: Int = DEFAULT_UPLOAD_CHUNK_SIZE_BYTES,
) {

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

    private fun skipFully(input: InputStream, byteCount: Long) {
        var remaining = byteCount
        while (remaining > 0) {
            val skipped = input.skip(remaining)
            if (skipped <= 0) break
            remaining -= skipped
        }
    }
}
