package com.vpsmanager.data.files

import com.vpsmanager.mobileapiclient.api.MobileApi
import java.io.File
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class TransferRepositoryTest {

    private lateinit var server: MockWebServer
    private lateinit var stagingDir: File

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        stagingDir = File.createTempFile("transfer-repo-test", "").let {
            it.delete()
            it.mkdirs()
            it
        }
    }

    @After
    fun tearDown() {
        server.shutdown()
        stagingDir.deleteRecursively()
    }

    private fun repositoryFor(): TransferRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return TransferRepository(api, stagingDir)
    }

    @Test
    fun `downloadRange with a non-zero fromByte sends the corresponding Range header`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(206)
                .setHeader("Content-Type", "application/octet-stream")
                .setHeader("Content-Range", "bytes 100-104/105")
                .setBody("hello"),
        )

        repositoryFor().downloadRange("/srv/big.bin", fromByte = 100).toList()

        val recorded = server.takeRequest()
        assertEquals("bytes=100-", recorded.getHeader("Range"))
    }

    @Test
    fun `downloadRange with fromByte zero sends no Range header (fresh download)`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/octet-stream")
                .setBody("hello"),
        )

        repositoryFor().downloadRange("/srv/big.bin", fromByte = 0).toList()

        val recorded = server.takeRequest()
        assertNull(recorded.getHeader("Range"))
    }

    @Test
    fun `downloadRange streams the body and terminates with an isLast chunk`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/octet-stream")
                .setBody("hello world"),
        )

        val chunks = repositoryFor().downloadRange("/srv/small.txt", fromByte = 0).toList()

        val bytesChunks = chunks.filterIsInstance<DownloadChunkResult.Bytes>()
        assertTrue(bytesChunks.isNotEmpty())
        val reassembled = bytesChunks.filterNot { it.isLast }.joinToString("") { String(it.data) }
        assertEquals("hello world", reassembled)
        assertEquals(true, bytesChunks.last().isLast)
    }

    @Test
    fun `downloadRange maps an HTTP error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("not found"))

        val chunks = repositoryFor().downloadRange("/srv/missing.bin", fromByte = 0).toList()

        assertEquals(1, chunks.size)
        assertTrue(chunks.first() is DownloadChunkResult.Error)
    }

    @Test
    fun `startUpload maps a successful init response to Started with the session id`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"session_id":"abc-123"}"""),
        )

        val result = repositoryFor().startUpload("/srv/dest", "file.bin", 1000)

        assertEquals(UploadSessionResult.Started("abc-123"), result)
    }

    @Test
    fun `startUpload maps an HTTP error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(400).setBody("""{"title":"bad request"}"""))

        val result = repositoryFor().startUpload("", "", 0)

        assertTrue(result is UploadSessionResult.Error)
    }

    @Test
    fun `uploadChunk returns Accepted when the server accepts the chunk`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"received_bytes":400}"""),
        )

        val result = repositoryFor().uploadChunk("abc-123", 0, ByteArray(400))

        assertEquals(UploadChunkResult.Accepted, result)
    }

    @Test
    fun `uploadChunk returns Rejected when the server rejects the chunk`() = runTest {
        server.enqueue(MockResponse().setResponseCode(400).setBody("""{"title":"bad offset"}"""))

        val result = repositoryFor().uploadChunk("abc-123", 0, ByteArray(400))

        assertTrue(result is UploadChunkResult.Rejected)
    }

    @Test
    fun `a full server disk (507) is not treated as a temporary failure`() = runTest {
        // Otherwise ENOSPC would retry forever in the background without telling the operator.
        server.enqueue(MockResponse().setResponseCode(507).setBody("""{"title":"no space left on device"}"""))

        val result = repositoryFor().uploadChunk("abc-123", 0, ByteArray(400))

        val rejected = result as UploadChunkResult.Rejected
        assertEquals(false, rejected.retryable)
        assertTrue(rejected.reason.contains("out of disk space", ignoreCase = true))
    }

    @Test
    fun `a file too large (413) states the limit and does not ask to retry`() = runTest {
        server.enqueue(MockResponse().setResponseCode(413).setBody("""{"title":"upload too large"}"""))

        val result = repositoryFor().startUpload("/srv/dest", "huge.bin", 3L shl 30)

        val error = result as UploadSessionResult.Error
        assertEquals(false, error.retryable)
        assertTrue(error.reason.contains("2 GB"))
    }

    @Test
    fun `permission denied (403) asks for another folder instead of retrying`() = runTest {
        server.enqueue(MockResponse().setResponseCode(403).setBody("""{"title":"permission denied"}"""))

        val result = repositoryFor().startUpload("/root/.ssh", "key.txt", 100)

        val error = result as UploadSessionResult.Error
        assertEquals(false, error.retryable)
        assertTrue(error.reason.contains("permission", ignoreCase = true))
    }

    @Test
    fun `server down (503) is temporary and the upload continues later`() = runTest {
        server.enqueue(MockResponse().setResponseCode(503))

        val result = repositoryFor().uploadChunk("abc-123", 0, ByteArray(400))

        assertEquals(true, (result as UploadChunkResult.Rejected).retryable)
    }

    @Test
    fun `completeUpload maps a successful response to Success carrying the final path`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"ok":true,"path":"/srv/dest/file.bin"}"""),
        )

        val result = repositoryFor().completeUpload("abc-123")

        assertEquals(UploadCompleteResult.Success("/srv/dest/file.bin"), result)
    }

    @Test
    fun `completeUpload maps a 409 incomplete response to Incomplete with the exact byte counts, not a generic Error`() =
        runTest {
            server.enqueue(
                MockResponse()
                    .setResponseCode(409)
                    .setHeader("Content-Type", "application/json")
                    .setBody("""{"error":"incomplete","received_bytes":400,"total_size":1000}"""),
            )

            val result = repositoryFor().completeUpload("abc-123")

            assertEquals(UploadCompleteResult.Incomplete(receivedBytes = 400, totalSize = 1000), result)
        }

    @Test
    fun `completeUpload maps a non-conflict client error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"title":"session not found"}"""))

        val result = repositoryFor().completeUpload("does-not-exist")

        assertTrue(result is UploadCompleteResult.Error)
    }
}
