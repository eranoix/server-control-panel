package com.vpsmanager.feature.whatsapp.send

import com.vpsmanager.data.whatsapp.UploadResult
import com.vpsmanager.data.whatsapp.WhatsAppRepository
import java.io.File
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Records every `uploadMedia` call it receives and returns a scripted,
 * queued [UploadResult] per call -- mirrors [WhatsAppRepositoryTest]'s
 * `MockWebServer` coverage of the same idempotency contract, but at the
 * seam [MediaUploadWorker] actually depends on (never `WhatsappApi`
 * directly).
 */
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
            enqueue(UploadResult.Error("falha de rede"))
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
        // A retry's callback restarts its own attempt at 0 -- the clamp must hold the higher value already shown.
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
        assertFalse("um arquivo grande demais nunca deve ser retentável", (outcome as MediaSendOutcome.Failed).retryable)
        assertEquals("a checagem client-side não deve nem abrir uma requisição", 0, repository.callCount)
    }

    @Test
    fun `a server 413 maps to the same non-retryable shape as the client-side size cap`() = runTest {
        val repository = FakeUploadRepository().apply {
            enqueue(UploadResult.Error(reason = "Não foi possível enviar o arquivo (erro 413).", overCap = true))
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

        assertFalse("um 413 do servidor também nunca deve ser retentável", outcome.retryable)
    }

    @Test
    fun `a retryable server error maps to a retryable outcome`() = runTest {
        val repository = FakeUploadRepository().apply {
            enqueue(UploadResult.Error(reason = "Falha de conexão. Verifique a rede e tente novamente."))
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
