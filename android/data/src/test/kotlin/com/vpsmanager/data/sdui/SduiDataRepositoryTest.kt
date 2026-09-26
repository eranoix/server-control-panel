package com.vpsmanager.data.sdui

import com.vpsmanager.core.sdui.SduiDataSource
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonArray
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class SduiDataRepositoryTest {

    private lateinit var server: MockWebServer

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun repositoryFor(): SduiDataRepository {
        val client = SduiDataClient(basePath = server.url("/").toString())
        return SduiDataRepository(client)
    }

    @Test
    fun `fetch maps a JSON array body to Success`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""[{"name":"web"},{"name":"db"}]""")
        )

        val result = repositoryFor().fetch(SduiDataSource(endpoint = "/api/mobile/v1/docker/containers"))

        assertTrue(result is SduiDataResult.Success)
        assertEquals(2, ((result as SduiDataResult.Success).body as JsonArray).size)
        val request = server.takeRequest()
        assertEquals("/api/mobile/v1/docker/containers", request.path)
    }

    @Test
    fun `fetch parses an embedded query string into real query parameters`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""[{"ts":1,"value":2}]""")
        )

        repositoryFor().fetch(
            SduiDataSource(endpoint = "/api/mobile/v1/system/history?metric=cpu")
        )

        val request = server.takeRequest()
        assertEquals("/api/mobile/v1/system/history?metric=cpu", request.path)
    }

    @Test
    fun `fetch maps an empty array body to Empty`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("[]")
        )

        val result = repositoryFor().fetch(SduiDataSource(endpoint = "/api/mobile/v1/notify/inbox"))

        // An empty array is still Success at this layer -- ComponentDataSource
        // (in :sdui) is what decides an empty list means the Empty UI state.
        assertTrue(result is SduiDataResult.Success)
    }

    @Test
    fun `fetch maps an HTTP client error to Error, never throwing`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(403)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"title":"Forbidden"}""")
        )

        val result = repositoryFor().fetch(SduiDataSource(endpoint = "/api/mobile/v1/docker/containers"))

        assertTrue(result is SduiDataResult.Error)
    }

    @Test
    fun `fetch maps an HTTP server error to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(503))

        val result = repositoryFor().fetch(SduiDataSource(endpoint = "/api/mobile/v1/docker/containers"))

        assertTrue(result is SduiDataResult.Error)
    }

    @Test
    fun `fetch refuses an endpoint outside the BFF mobile namespace, without calling the network`() = runTest {
        val result = repositoryFor().fetch(SduiDataSource(endpoint = "https://evil.example.com/steal"))

        assertTrue(result is SduiDataResult.Error)
        assertEquals(0, server.requestCount)
    }

    @Test
    fun `fetch refuses an unsupported HTTP method, without calling the network`() = runTest {
        val result = repositoryFor().fetch(
            SduiDataSource(endpoint = "/api/mobile/v1/docker/containers", method = "OPTIONS")
        )

        assertTrue(result is SduiDataResult.Error)
        assertEquals(0, server.requestCount)
    }
}
