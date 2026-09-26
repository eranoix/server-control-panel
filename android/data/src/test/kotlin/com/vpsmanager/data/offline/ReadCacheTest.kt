package com.vpsmanager.data.offline

import java.io.File
import java.nio.file.Files
import okhttp3.Cache
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * Proves the offline read cache actually serves responses, since HTTP cache misconfiguration fails
 * silently. The server is really shut down mid-test to produce the same `IOException` as no network.
 */
class ReadCacheTest {

    private lateinit var server: MockWebServer
    private lateinit var dir: File
    private lateinit var client: OkHttpClient

    @Before
    fun build() {
        server = MockWebServer()
        server.start()
        dir = Files.createTempDirectory("read-cache").toFile()
        client = OkHttpClient.Builder()
            .cache(Cache(dir, 4L * 1024 * 1024))
            .addNetworkInterceptor(ReadCache.MakeCacheable)
            .addInterceptor(ReadCache.ServeFromCacheWhenOffline)
            .build()
    }

    @After
    fun tearDown() {
        runCatching { server.shutdown() }
        dir.deleteRecursively()
    }

    private fun get(path: String) =
        client.newCall(Request.Builder().url(server.url(path)).build()).execute()

    @Test
    fun `with the server down the previous response is still served`() {
        server.enqueue(MockResponse().setBody("""{"cpu":21}"""))
        get("/ops/status").use { assertEquals("""{"cpu":21}""", it.body?.string()) }

        // The server really goes away.
        server.shutdown()

        get("/ops/status").use { response ->
            assertEquals(200, response.code)
            assertEquals("""{"cpu":21}""", response.body?.string())
            assertTrue("the response must come from the cache", response.networkResponse == null)
        }
    }

    /** A route never fetched gets OkHttp's 504 "Unsatisfiable Request", which the caller maps to the usual error. */
    @Test
    fun `an unseen route without network returns 504, not a made-up body`() {
        server.shutdown()

        get("/never-visited").use { response ->
            assertEquals(504, response.code)
        }
    }

    /** The server's `no-store` always wins. */
    @Test
    fun `server no-store is honoured and nothing is stored`() {
        server.enqueue(
            MockResponse().setBody("secret").addHeader("Cache-Control", "no-store"),
        )
        get("/security/secrets").use { it.body?.string() }

        server.shutdown()

        get("/security/secrets").use { response ->
            assertEquals("nothing marked no-store may survive the outage", 504, response.code)
        }
    }

    /** With network, always revalidate: these screens show live state. */
    @Test
    fun `with network the response is always fresh, never cached`() {
        server.enqueue(MockResponse().setBody("""{"cpu":21}"""))
        get("/ops/status").use { it.body?.string() }

        server.enqueue(MockResponse().setBody("""{"cpu":88}"""))
        get("/ops/status").use { response ->
            assertEquals("""{"cpu":88}""", response.body?.string())
        }
    }
}
