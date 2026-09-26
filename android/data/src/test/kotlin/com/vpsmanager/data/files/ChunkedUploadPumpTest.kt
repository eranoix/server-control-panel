package com.vpsmanager.data.files

import java.io.ByteArrayInputStream
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The chunked upload loop, exercised against a fake [TransferRepository].
 * What these tests protect is not the loop itself — it is the RESUMPTION: that
 * the chunk goes back out from the right offset after a drop, that the
 * server's count beats ours, and that "retrying will not help" does not turn
 * into an eternal retry.
 */
class ChunkedUploadPumpTest {

    private class FakeTransferRepository(
        private val chunkResults: MutableList<UploadChunkResult> = mutableListOf(),
        private val completeResult: UploadCompleteResult = UploadCompleteResult.Success("/destino/arquivo.bin"),
    ) : TransferRepository() {
        val chunksRecebidos = mutableListOf<Pair<Long, Int>>()

        override suspend fun uploadChunk(sessionId: String, offset: Long, data: ByteArray): UploadChunkResult {
            chunksRecebidos += offset to data.size
            return if (chunkResults.isEmpty()) UploadChunkResult.Accepted else chunkResults.removeAt(0)
        }

        override suspend fun completeUpload(sessionId: String): UploadCompleteResult = completeResult
    }

    private fun fonteDe(bytes: ByteArray) = UploadByteSource { ByteArrayInputStream(bytes) }

    @Test
    fun `envia todos os pedacos e conclui com o caminho do servidor`() = runTest {
        val repo = FakeTransferRepository()
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(fonteDe(ByteArray(10)), "s1", startOffset = 0, totalSize = 10, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Completed("/destino/arquivo.bin"), outcome)
        assertEquals(listOf(0L to 4, 4L to 4, 8L to 2), repo.chunksRecebidos)
    }

    @Test
    fun `retomada comeca do offset ja confirmado, nao do zero`() = runTest {
        val repo = FakeTransferRepository()
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        pump.send(fonteDe(ByteArray(10)), "s1", startOffset = 8, totalSize = 10, onProgress = {})

        // A single chunk, and at offset 8 — the first 8 bytes were already on
        // the server and were not sent again.
        assertEquals(listOf(8L to 2), repo.chunksRecebidos)
    }

    @Test
    fun `progresso e reportado por pedaco ACEITO, para a retomada nunca deixar buraco`() = runTest {
        val repo = FakeTransferRepository()
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)
        val progresso = mutableListOf<Long>()

        pump.send(fonteDe(ByteArray(10)), "s1", 0, 10, onProgress = { progresso += it })

        assertEquals(listOf(4L, 8L, 10L), progresso)
    }

    @Test
    fun `pedaco recusado por falha temporaria devolve Interrompido com o que ja foi aceito`() = runTest {
        val repo = FakeTransferRepository(
            chunkResults = mutableListOf(
                UploadChunkResult.Accepted,
                UploadChunkResult.Rejected("rede caiu", retryable = true),
            ),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(fonteDe(ByteArray(10)), "s1", 0, 10, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Interrupted(bytesSent = 4, reason = "rede caiu"), outcome)
    }

    @Test
    fun `pedaco recusado por disco cheio devolve Recusado, nunca Interrompido`() = runTest {
        val repo = FakeTransferRepository(
            chunkResults = mutableListOf(UploadChunkResult.Rejected("sem espaço", retryable = false)),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(fonteDe(ByteArray(10)), "s1", 0, 10, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Refused("sem espaço"), outcome)
    }

    @Test
    fun `origem que sumiu do aparelho vira Recusado com instrucao de reescolher`() = runTest {
        val pump = ChunkedUploadPump(FakeTransferRepository(), chunkSizeBytes = 4)

        val outcome = pump.send(UploadByteSource { null }, "s1", 0, 10, onProgress = {})

        val recusado = outcome as ChunkedUploadOutcome.Refused
        assertTrue(recusado.reason.contains("choose the file again", ignoreCase = true))
    }

    @Test
    fun `complete incompleto adota a contagem do servidor, nao a nossa`() = runTest {
        val repo = FakeTransferRepository(
            completeResult = UploadCompleteResult.Incomplete(receivedBytes = 6, totalSize = 10),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(fonteDe(ByteArray(10)), "s1", 0, 10, onProgress = {})

        // Locally we would have counted 10; the server says 6, and it is the
        // server that knows what became durable on disk.
        assertEquals(6L, (outcome as ChunkedUploadOutcome.Interrupted).bytesSent)
    }

    @Test
    fun `complete recusado sem retomada possivel vira Recusado`() = runTest {
        val repo = FakeTransferRepository(
            completeResult = UploadCompleteResult.Error("pasta sem permissão", retryable = false),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(fonteDe(ByteArray(4)), "s1", 0, 4, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Refused("pasta sem permissão"), outcome)
    }
}
