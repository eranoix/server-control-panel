package com.vpsmanager.data.sdui

import com.vpsmanager.core.sdui.SduiComponent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class SduiRepositoryTest {

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

    private fun repositoryFor(): SduiRepository {
        val client = SduiDataClient(basePath = server.url("/").toString())
        return SduiRepository(client, SduiActionRepository(client))
    }

    private val envelopeJson = """
        {"sdui_version":1,"screen":{"id":"scheduler.jobs","title":"Agendador","components":[
            {"type":"action","id":"refresh-jobs","label":"Atualizar","action_id":"scheduler.jobs.refresh"}
        ]}}
    """.trimIndent()

    @Test
    fun `screen fetches the raw envelope and hands it to core's parser, never a generated DTO`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(envelopeJson)
        )

        val result = repositoryFor().screen("scheduler.jobs")

        assertTrue(result is SduiScreenResult.Success)
        val envelope = (result as SduiScreenResult.Success).envelope
        assertEquals("scheduler.jobs", envelope.screen.id)
        val action = envelope.screen.components.single() as SduiComponent.Action
        assertEquals("scheduler.jobs.refresh", action.actionId)
        val request = server.takeRequest()
        assertEquals("/api/mobile/v1/screens/scheduler.jobs", request.path)
        assertEquals("GET", request.method)
    }

    @Test
    fun `screen maps a 404 to NotFound`() = runTest {
        server.enqueue(MockResponse().setResponseCode(404))

        val result = repositoryFor().screen("scheduler.jobs")

        assertTrue(result is SduiScreenResult.NotFound)
    }

    @Test
    fun `screen maps a 403 to Forbidden, distinct from NotFound`() = runTest {
        server.enqueue(MockResponse().setResponseCode(403))

        val result = repositoryFor().screen("scheduler.jobs")

        assertTrue(result is SduiScreenResult.Forbidden)
    }

    @Test
    fun `screen maps a 500 to Error, never throwing`() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))

        val result = repositoryFor().screen("scheduler.jobs")

        assertTrue(result is SduiScreenResult.Error)
    }

    @Test
    fun `screen refuses a malformed section id, without calling the network`() = runTest {
        val result = repositoryFor().screen("../../etc/passwd")

        assertTrue(result is SduiScreenResult.Error)
        assertEquals(0, server.requestCount)
    }

    @Test
    fun `screen maps an unparseable body to Error rather than throwing`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"not_an_envelope": true}""")
        )

        val result = repositoryFor().screen("scheduler.jobs")

        assertTrue(result is SduiScreenResult.Error)
    }

    @Test
    fun `action delegates to SduiActionRepository unchanged`() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"patch":{"id":"job-1","status":"running"}}""")
        )

        val result = repositoryFor().action("scheduler.job.run_now", JsonObject(emptyMap()))

        assertTrue(result is SduiActionHttpResult.Success)
        val request = server.takeRequest()
        assertEquals("/api/mobile/v1/actions/scheduler.job.run_now", request.path)
    }
}
