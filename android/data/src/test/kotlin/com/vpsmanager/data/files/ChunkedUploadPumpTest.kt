package com.vpsmanager.data.files

import java.io.ByteArrayInputStream
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The chunked upload loop against a fake [TransferRepository]. The focus is resumption: the right
 * offset after a drop, the server's byte count winning over ours, and non-retryable errors not retrying.
 */
class ChunkedUploadPumpTest {

    private class FakeTransferRepository(
        private val chunkResults: MutableList<UploadChunkResult> = mutableListOf(),
        private val completeResult: UploadCompleteResult = UploadCompleteResult.Success("/dest/file.bin"),
    ) : TransferRepository() {
        val receivedChunks = mutableListOf<Pair<Long, Int>>()

        override suspend fun uploadChunk(sessionId: String, offset: Long, data: ByteArray): UploadChunkResult {
            receivedChunks += offset to data.size
            return if (chunkResults.isEmpty()) UploadChunkResult.Accepted else chunkResults.removeAt(0)
        }

        override suspend fun completeUpload(sessionId: String): UploadCompleteResult = completeResult
    }

    private fun sourceOf(bytes: ByteArray) = UploadByteSource { ByteArrayInputStream(bytes) }

    @Test
    fun `sends every chunk and completes with the server path`() = runTest {
        val repo = FakeTransferRepository()
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(sourceOf(ByteArray(10)), "s1", startOffset = 0, totalSize = 10, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Completed("/dest/file.bin"), outcome)
        assertEquals(listOf(0L to 4, 4L to 4, 8L to 2), repo.receivedChunks)
    }

    @Test
    fun `resuming starts at the confirmed offset, not at zero`() = runTest {
        val repo = FakeTransferRepository()
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        pump.send(sourceOf(ByteArray(10)), "s1", startOffset = 8, totalSize = 10, onProgress = {})

        // The first 8 bytes were already on the server and are not sent again.
        assertEquals(listOf(8L to 2), repo.receivedChunks)
    }

    @Test
    fun `progress is reported per accepted chunk so resuming never leaves a gap`() = runTest {
        val repo = FakeTransferRepository()
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)
        val progress = mutableListOf<Long>()

        pump.send(sourceOf(ByteArray(10)), "s1", 0, 10, onProgress = { progress += it })

        assertEquals(listOf(4L, 8L, 10L), progress)
    }

    @Test
    fun `a chunk rejected by a temporary failure returns Interrupted with the accepted bytes`() = runTest {
        val repo = FakeTransferRepository(
            chunkResults = mutableListOf(
                UploadChunkResult.Accepted,
                UploadChunkResult.Rejected("network dropped", retryable = true),
            ),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(sourceOf(ByteArray(10)), "s1", 0, 10, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Interrupted(bytesSent = 4, reason = "network dropped"), outcome)
    }

    @Test
    fun `a chunk rejected by a full disk returns Refused, never Interrupted`() = runTest {
        val repo = FakeTransferRepository(
            chunkResults = mutableListOf(UploadChunkResult.Rejected("no space", retryable = false)),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(sourceOf(ByteArray(10)), "s1", 0, 10, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Refused("no space"), outcome)
    }

    @Test
    fun `a source gone from the device becomes Refused with a hint to choose again`() = runTest {
        val pump = ChunkedUploadPump(FakeTransferRepository(), chunkSizeBytes = 4)

        val outcome = pump.send(UploadByteSource { null }, "s1", 0, 10, onProgress = {})

        val refused = outcome as ChunkedUploadOutcome.Refused
        assertTrue(refused.reason.contains("choose the file again", ignoreCase = true))
    }

    @Test
    fun `an incomplete complete call adopts the server byte count`() = runTest {
        val repo = FakeTransferRepository(
            completeResult = UploadCompleteResult.Incomplete(receivedBytes = 6, totalSize = 10),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(sourceOf(ByteArray(10)), "s1", 0, 10, onProgress = {})

        // The server knows what became durable on disk, so its count wins.
        assertEquals(6L, (outcome as ChunkedUploadOutcome.Interrupted).bytesSent)
    }

    @Test
    fun `a refused complete call with no possible resume becomes Refused`() = runTest {
        val repo = FakeTransferRepository(
            completeResult = UploadCompleteResult.Error("folder not writable", retryable = false),
        )
        val pump = ChunkedUploadPump(repo, chunkSizeBytes = 4)

        val outcome = pump.send(sourceOf(ByteArray(4)), "s1", 0, 4, onProgress = {})

        assertEquals(ChunkedUploadOutcome.Refused("folder not writable"), outcome)
    }
}
