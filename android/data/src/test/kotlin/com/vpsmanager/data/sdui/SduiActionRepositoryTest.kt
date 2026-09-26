package com.vpsmanager.data.sdui

import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class SduiActionRepositoryTest {

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

    private fun repositoryFor(): SduiActionRepository {
        val client = SduiDataClient(basePath = server.url("/").toString())
        return SduiActionRepository(client)
    }

    @Test
    fun `invoke posts to the actions endpoint and maps a 200 patch body to Success`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"patch":{"id":"job-1","status":"running"}}""")
        )

        val result = repositoryFor().invoke("scheduler.jobs.run_now", JsonObject(emptyMap()))

        assertTrue(result is SduiActionHttpResult.Success)
        val patch = ((result as SduiActionHttpResult.Success).body["patch"] as JsonObject)
        assertEquals("job-1", patch["id"]?.jsonPrimitive?.content)
        val request = server.takeRequest()
        assertEquals("/api/mobile/v1/actions/scheduler.jobs.run_now", request.path)
        assertEquals("POST", request.method)
    }

    @Test
    fun `invoke maps a 422 body to ValidationFailed, preserving the raw body verbatim`() = runTest {
        val fixtureBody = """{"error":"validation_failed","fields":{"name":["required"]}}"""
        server.enqueue(
            MockResponse()
                .setResponseCode(422)
                .setHeader("Content-Type", "application/json")
                .setBody(fixtureBody)
        )

        val result = repositoryFor().invoke("scheduler.jobs.create", JsonObject(emptyMap()))

        assertTrue(result is SduiActionHttpResult.ValidationFailed)
        assertEquals(fixtureBody, (result as SduiActionHttpResult.ValidationFailed).rawBody)
    }

    @Test
    fun `invoke maps a 404 to NotFound, indistinguishable from an unauthorized id`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(404)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"error":"unknown_action"}""")
        )

        val result = repositoryFor().invoke("does.not.exist", JsonObject(emptyMap()))

        assertTrue(result is SduiActionHttpResult.NotFound)
    }

    @Test
    fun `invoke maps a 403 to Stale`() = runTest {
        server.enqueue(MockResponse().setResponseCode(403))

        val result = repositoryFor().invoke("scheduler.jobs.run_now", JsonObject(emptyMap()))

        assertTrue(result is SduiActionHttpResult.Stale)
    }

    @Test
    fun `invoke maps a 500 to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))

        val result = repositoryFor().invoke("scheduler.jobs.run_now", JsonObject(emptyMap()))

        assertTrue(result is SduiActionHttpResult.Error)
    }

    @Test
    fun `invoke refuses a malformed action id, without calling the network`() = runTest {
        val result = repositoryFor().invoke("../../etc/passwd", JsonObject(emptyMap()))

        assertTrue(result is SduiActionHttpResult.Error)
        assertEquals(0, server.requestCount)
    }
}
