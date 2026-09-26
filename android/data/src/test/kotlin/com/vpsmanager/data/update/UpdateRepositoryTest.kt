package com.vpsmanager.data.update

import com.vpsmanager.core.model.ServerConfig
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.data.config.ServerConfigStore
import com.vpsmanager.mobileapiclient.api.MobileApi
import java.io.File
import java.security.MessageDigest
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.runTest
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/**
 * The update channel against a real HTTP server (MockWebServer), focused on resumption: a
 * download cut in half must keep its bytes on disk and resume from the exact offset.
 */
class UpdateRepositoryTest {

    @get:Rule
    val temp = TemporaryFolder()

    private lateinit var server: MockWebServer

    /** 40 KiB of deterministic content. */
    private val content = ByteArray(40 * 1024) { (it % 251).toByte() }
    private val contentSha = sha256(content)

    @Before
    fun start() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun stop() {
        server.shutdown()
    }

    private fun repository() = UpdateRepository(
        serverConfigRepository = ServerConfigRepository(FakeStore(baseUrl())),
        mobileApiFactory = { basePath -> MobileApi(basePath) },
    )

    private fun baseUrl() = server.url("/").toString().removeSuffix("/")

    private fun artifact(size: Int = content.size, sha: String = contentSha) = UpdateArtifact(
        url = "/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff",
        sizeBytes = size.toLong(),
        sha256 = sha,
    )

    /**
     * The server omits `patch` when it has none for the given base (the most common case),
     * and the generated client must deserialize that.
     */
    @Test
    fun manifestWithoutPatchDoesNotBreakDeserialization() = runTest {
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(MANIFEST_WITHOUT_PATCH))

        val result = repository().check(baseSha256 = "aa".repeat(32))

        check(result is UpdateCheckResult.Success)
        assertNull(result.manifest.patch)
        assertEquals(10_029_237L, result.manifest.full.sizeBytes)
        assertEquals("0.1.6", result.manifest.latest.versionName)
        assertFalse(result.manifest.upToDate)
    }

    /** The other shape of the same case: an explicit `"patch": null`, which an older server would send. */
    @Test
    fun manifestWithExplicitNullPatchIsAlsoAccepted() = runTest {
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(MANIFEST_NULL_PATCH))

        val result = repository().check(baseSha256 = null)

        check(result is UpdateCheckResult.Success)
        assertNull(result.manifest.patch)
    }

    @Test
    fun manifestWithPatchReturnsPatchSizeNotApkSize() = runTest {
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody(MANIFESTO_COM_PATCH))

        val result = repository().check(baseSha256 = "bb".repeat(32))

        check(result is UpdateCheckResult.Success)
        assertEquals(1_400_329L, result.manifest.patch?.sizeBytes)
        assertEquals(31_135_416L, result.manifest.latest.apkSizeBytes)
    }

    /** 503 means nothing has been published yet, which is not an error. */
    @Test
    fun unpublishedChannelIsNotAnError() = runTest {
        server.enqueue(MockResponse().setResponseCode(503).setBody("""{"detail":"update channel not published yet"}"""))

        assertEquals(UpdateCheckResult.ChannelNotPublished, repository().check(baseSha256 = null))
    }

    @Test
    fun noConfiguredServerDoesNotTryNetwork() = runTest {
        val repo = UpdateRepository(
            serverConfigRepository = ServerConfigRepository(FakeStore(null)),
            mobileApiFactory = { basePath -> MobileApi(basePath) },
        )

        val result = repo.check(baseSha256 = null)

        check(result is UpdateCheckResult.Error)
        assertEquals(0, server.requestCount)
    }

    @Test
    fun fullDownloadFromScratchChecksHashAndDeliversFile() = runTest {
        server.enqueue(fullResponse(content))
        val target = File(temp.newFolder(), "artifact.hdiff")

        val events = repository().download(artifact(), target).toList()

        val done = events.filterIsInstance<ArtifactDownloadProgress.Done>().single()
        assertArrayEquals(content, done.file.readBytes())
        // No Range on the first trip: there was nothing on disk to resume.
        val request = server.takeRequest()
        assertNull(request.getHeader("Range"))
        assertNull(request.getHeader("If-Range"))
    }

    /**
     * First attempt gets half and fails as a connection error, keeping the partial file.
     * The second sends `Range: bytes=<half>-` with the ETag in `If-Range` and yields an intact file.
     */
    @Test
    fun interruptedDownloadResumesWhereItStopped() = runTest {
        val half = content.size / 2
        server.enqueue(partialResponse(content, from = 0, until = half - 1, declaredTotal = content.size))
        server.enqueue(partialResponse(content, from = half, until = content.size - 1, declaredTotal = content.size))

        val target = File(temp.newFolder(), "artifact.hdiff")
        val repo = repository()

        val first = repo.download(artifact(), target).toList()
        val failure = first.filterIsInstance<ArtifactDownloadProgress.Failed>().single()
        assertFalse("a dropped connection must not be treated as corruption", failure.corrupt)
        assertEquals("the partial file must stay on disk for resuming", half.toLong(), target.length())
        server.takeRequest()

        val second = repo.download(artifact(), target).toList()

        val done = second.filterIsInstance<ArtifactDownloadProgress.Done>().single()
        assertArrayEquals(content, done.file.readBytes())
        assertEquals(contentSha, sha256(done.file.readBytes()))

        val resumed = server.takeRequest()
        assertEquals("bytes=$half-", resumed.getHeader("Range"))
        assertEquals("\"$contentSha\"", resumed.getHeader("If-Range"))
    }

    /**
     * When `If-Range` does not match the server sends the whole file with 200, so the partial
     * must be truncated instead of appended to.
     */
    @Test
    fun status200ToRangeRequestTruncatesPartialInsteadOfAppending() = runTest {
        val target = File(temp.newFolder(), "artifact.hdiff")
        target.writeBytes(ByteArray(1000) { 0x7f })
        server.enqueue(fullResponse(content))

        val events = repository().download(artifact(), target).toList()

        val done = events.filterIsInstance<ArtifactDownloadProgress.Done>().single()
        assertArrayEquals(content, done.file.readBytes())
        assertEquals(content.size.toLong(), target.length())
    }

    /** 416: the partial is not a valid prefix, so delete it and ask for a fresh attempt. */
    @Test
    fun impossibleRangeDeletesPartialAndAsksForRetry() = runTest {
        val target = File(temp.newFolder(), "artifact.hdiff")
        target.writeBytes(ByteArray(content.size - 1) { 0x11 })
        server.enqueue(MockResponse().setResponseCode(416))

        val events = repository().download(artifact(), target).toList()

        val failure = events.filterIsInstance<ArtifactDownloadProgress.Failed>().single()
        assertTrue(failure.corrupt)
        assertFalse("the invalid partial must not survive", target.exists())
    }

    /**
     * Bytes with the wrong hash never reach `hpatchz`, and the file is deleted because
     * resuming a corrupted download would keep the corruption.
     */
    @Test
    fun mismatchedHashDeletesFileAndMarksCorrupted() = runTest {
        server.enqueue(fullResponse(content))
        val target = File(temp.newFolder(), "artifact.hdiff")

        val events = repository().download(artifact(sha = "ff".repeat(32)), target).toList()

        val failure = events.filterIsInstance<ArtifactDownloadProgress.Failed>().single()
        assertTrue(failure.corrupt)
        assertFalse(target.exists())
    }

    /** An intact file already on disk is not downloaded again. */
    @Test
    fun completeIntactFileIsNotDownloadedAgain() = runTest {
        val target = File(temp.newFolder(), "artifact.hdiff")
        target.writeBytes(content)

        val events = repository().download(artifact(), target).toList()

        assertTrue(events.single() is ArtifactDownloadProgress.Done)
        assertEquals(0, server.requestCount)
    }

    /** A partial larger than the target is leftover from something else, so start over. */
    @Test
    fun partialLargerThanTargetIsDiscardedAndDownloadRestarts() = runTest {
        val target = File(temp.newFolder(), "artifact.hdiff")
        target.writeBytes(ByteArray(content.size + 500) { 0x22 })
        server.enqueue(fullResponse(content))

        val events = repository().download(artifact(), target).toList()

        assertTrue(events.filterIsInstance<ArtifactDownloadProgress.Done>().size == 1)
        assertNull(server.takeRequest().getHeader("Range"))
    }

    @Test
    fun progressIsReportedUpToTotal() = runTest {
        server.enqueue(fullResponse(content))
        val target = File(temp.newFolder(), "artifact.hdiff")

        val events = repository().download(artifact(), target).toList()

        val progressEvents = events.filterIsInstance<ArtifactDownloadProgress.Progress>()
        assertTrue("there must be progress before the end", progressEvents.isNotEmpty())
        assertEquals(content.size.toLong(), progressEvents.last().downloadedBytes)
        assertEquals(content.size.toLong(), progressEvents.last().totalBytes)
    }

    @Test
    fun manifestUrlIsResolvedAgainstConfiguredBase() {
        val resolved = resolveArtifactUrl(
            baseUrl = "https://vpsm.example.com/api/mobile/v1",
            artifactUrl = "/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff",
        )

        assertEquals(
            "https://vpsm.example.com/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff",
            resolved.toString(),
        )
    }

    /**
     * The `url` comes from the network, and the `Authorization` interceptor would attach the
     * Bearer token to any host, so a different host or scheme must be rejected.
     */
    @Test
    fun urlThatChangesHostIsRejected() {
        assertNull(resolveArtifactUrl("https://vpsm.example.com/api/mobile/v1", "https://attacker.example/x.hdiff"))
        assertNull(resolveArtifactUrl("https://vpsm.example.com/api/mobile/v1", "http://vpsm.example.com/x.hdiff"))
    }

    @Test
    fun contentRangeIsTakenFromServerNotFromRequest() {
        assertEquals(1024L, contentRangeStart("bytes 1024-2047/4096"))
        assertEquals(0L, contentRangeStart("bytes 0-10/11"))
        assertNull(contentRangeStart(null))
        assertNull(contentRangeStart("items 1-2/3"))
    }

    private fun fullResponse(body: ByteArray) = MockResponse()
        .setResponseCode(200)
        .setHeader("ETag", "\"${sha256(body)}\"")
        .setBody(okio.Buffer().write(body))

    private fun partialResponse(body: ByteArray, from: Int, until: Int, declaredTotal: Int) = MockResponse()
        .setResponseCode(206)
        .setHeader("ETag", "\"${sha256(body)}\"")
        .setHeader("Content-Range", "bytes $from-$until/$declaredTotal")
        .setBody(okio.Buffer().write(body, from, until - from + 1))

    private fun assertArrayEquals(expected: ByteArray, actual: ByteArray) {
        assertEquals("size", expected.size, actual.size)
        assertTrue("content byte by byte", expected.contentEquals(actual))
    }

    private class FakeStore(private val baseUrl: String?) : ServerConfigStore {
        override fun load(): ServerConfig? = baseUrl?.let { ServerConfig(baseUrl = it, allowInsecureHttp = true) }
        override fun save(config: ServerConfig) = Unit
        override fun clear() = Unit
    }

    private companion object {
        fun sha256(bytes: ByteArray): String =
            MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }

        const val MANIFEST_WITHOUT_PATCH = """
            {
              "latest": {"version_name":"0.1.6","version_code":6,"sha256":"4456a4ca","size_bytes":31135416},
              "up_to_date": false,
              "full": {"url":"/api/mobile/v1/app/update/artifact?file=full%2Fx.hdiff","size_bytes":10029237,"sha256":"deadbeef"},
              "patch_tool": "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m"
            }
        """

        const val MANIFEST_NULL_PATCH = """
            {
              "latest": {"version_name":"0.1.6","version_code":6,"sha256":"4456a4ca","size_bytes":31135416},
              "up_to_date": false,
              "patch": null,
              "full": {"url":"/api/mobile/v1/app/update/artifact?file=full%2Fx.hdiff","size_bytes":10029237,"sha256":"deadbeef"},
              "patch_tool": "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m"
            }
        """

        const val MANIFESTO_COM_PATCH = """
            {
              "latest": {"version_name":"0.1.6","version_code":6,"sha256":"4456a4ca","size_bytes":31135416},
              "up_to_date": false,
              "patch": {"url":"/api/mobile/v1/app/update/artifact?file=patches%2Fa-b.hdiff","size_bytes":1400329,"sha256":"cafe"},
              "full": {"url":"/api/mobile/v1/app/update/artifact?file=full%2Fx.hdiff","size_bytes":10029237,"sha256":"deadbeef"},
              "patch_tool": "HDiffPatch::hdiffz v5.1.3 -SD -c-lzma2-9-64m"
            }
        """
    }
}
