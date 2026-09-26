package com.vpsmanager.feature.whatsapp.send

import com.vpsmanager.data.whatsapp.UploadResult
import com.vpsmanager.data.whatsapp.WhatsAppRepository
import java.io.File
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Fake repository that records each `uploadMedia` call and returns scripted [UploadResult]s in order. */
private class FakeUploadRepository(
    private val results: MutableList<UploadResult> = mutableListOf(),
) : WhatsAppRepository() {
    val receivedClientMsgIds = mutableListOf<String>()
    var callCount = 0
        private set

    fun enqueue(result: UploadResult) {
        results += result
    }

    override suspend fun uploadMedia(
        jid: String,
        clientMsgId: String,
        file: File,
        caption: String?,
        msgType: String?,
        quotedId: String?,
        onProgress: (percent: Int) -> Unit,
    ): UploadResult {
        callCount++
        receivedClientMsgIds += clientMsgId
        onProgress(50)
        onProgress(100)
        return results.removeAt(0)
    }
}

class MediaUploadWorkerTest {

    private fun tempFile(): File = File.createTempFile("attachment", ".jpg").apply { deleteOnExit() }

    @Test
    fun `retrying with the same client_msg_id sends the exact same id both times`() = runTest {
        val repository = FakeUploadRepository().apply {
            enqueue(UploadResult.Error("network failure"))
            enqueue(UploadResult.Success("srv-1"))
        }
        val worker = MediaUploadWorker(repository)
        val file = tempFile()

        worker.upload(jid = "a@s.whatsapp.net", clientMsgId = "retry-1", file = file, sizeBytes = 10L, mimeType = "image/jpeg", msgType = "image")
        worker.upload(jid = "a@s.whatsapp.net", clientMsgId = "retry-1", file = file, sizeBytes = 10L, mimeType = "image/jpeg", msgType = "image")

        assertEquals(listOf("retry-1", "retry-1"), repository.receivedClientMsgIds)
        assertEquals(2, repository.callCount)
    }

    @Test
    fun `clampUploadProgress never lets progress regress, even across a retry`() {
        var progress: Int? = null
        progress = clampUploadProgress(progress, 40)
        progress = clampUploadProgress(progress, 90)
        // A retry restarts at 0; the clamp must keep the higher value already shown.
        progress = clampUploadProgress(progress, 0)

        assertEquals(90, progress)
    }

    @Test
    fun `clampUploadProgress tracks a fresh, higher reading`() {
        val progress = clampUploadProgress(previous = 30, next = 75)

        assertEquals(75, progress)
    }

    @Test
    fun `a file over the size cap fails fast without calling the repository`() = runTest {
        val repository = FakeUploadRepository()
        val worker = MediaUploadWorker(repository)

        val outcome = worker.upload(
            jid = "a@s.whatsapp.net",
            clientMsgId = "big-1",
            file = tempFile(),
            sizeBytes = MAX_UPLOAD_BYTES + 1,
            mimeType = "video/mp4",
            msgType = "video",
        )

        assertTrue(outcome is MediaSendOutcome.Failed)
        assertFalse("an oversized file must never be retryable", (outcome as MediaSendOutcome.Failed).retryable)
        assertEquals("the client-side check must not even open a request", 0, repository.callCount)
    }

    @Test
    fun `a server 413 maps to the same non-retryable shape as the client-side size cap`() = runTest {
        val repository = FakeUploadRepository().apply {
            enqueue(UploadResult.Error(reason = "Could not upload the file (error 413).", overCap = true))
        }
        val worker = MediaUploadWorker(repository)

        val outcome = worker.upload(
            jid = "a@s.whatsapp.net",
            clientMsgId = "big-2",
            file = tempFile(),
            sizeBytes = 1024L,
            mimeType = "video/mp4",
            msgType = "video",
        ) as MediaSendOutcome.Failed

        assertFalse("a server 413 must never be retryable either", outcome.retryable)
    }

    @Test
    fun `a retryable server error maps to a retryable outcome`() = runTest {
        val repository = FakeUploadRepository().apply {
            enqueue(UploadResult.Error(reason = "Connection failed. Check the network and try again."))
        }
        val worker = MediaUploadWorker(repository)

        val outcome = worker.upload(
            jid = "a@s.whatsapp.net",
            clientMsgId = "flaky-1",
            file = tempFile(),
            sizeBytes = 1024L,
            mimeType = "image/jpeg",
            msgType = "image",
        ) as MediaSendOutcome.Failed

        assertTrue(outcome.retryable)
    }
}
