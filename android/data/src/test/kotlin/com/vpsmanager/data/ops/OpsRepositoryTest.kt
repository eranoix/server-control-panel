package com.vpsmanager.data.ops

import com.vpsmanager.mobileapiclient.api.MobileApi
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class OpsRepositoryTest {

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

    private fun repositoryFor(): OpsRepository {
        val api = MobileApi(basePath = server.url("/").toString())
        return OpsRepository(api)
    }

    @Test
    fun fetchStatusReturnsSnapshotFromServerResponse() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody(
                    """
                    {
                      "health": {"docker": "ok", "supabase": "ok"},
                      "health_ok": true,
                      "queue_running": 1,
                      "queue_queued": 2,
                      "alerts": [
                        {"name": "cpu-alta", "severity": "critical", "state": "firing", "current_value": 95.0, "threshold": 90.0, "unit": "%"}
                      ]
                    }
                    """.trimIndent(),
                ),
        )

        val result = repositoryFor().fetchStatus()

        check(result is OpsStatusResult.Success)
        assertEquals(true, result.snapshot.healthOk)
        assertEquals(1L, result.snapshot.queueRunning)
        assertEquals(2L, result.snapshot.queueQueued)
        assertEquals(1, result.snapshot.alerts.size)
        assertEquals("cpu-alta", result.snapshot.alerts[0].name)

        val recorded = server.takeRequest()
        assertEquals("GET", recorded.method)
        assertEquals("/ops/status", recorded.path)
    }

    @Test
    fun triggerDeployAlwaysSendsConfirmTrueAndReturnsJobId() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"job_id":"job-42"}"""),
        )

        val result = repositoryFor().triggerDeploy()

        check(result is TriggerDeployResult.Success)
        assertEquals("job-42", result.jobId)

        val recorded = server.takeRequest()
        assertEquals("POST", recorded.method)
        assertEquals("/ops/deploy", recorded.path)
        assertTrue(recorded.body.readUtf8().contains("\"confirm\":true"))
    }

    @Test
    fun fetchDeployStatusReturnsStatusFromServerResponse() = runTest {
        server.enqueue(
            MockResponse()
                .setResponseCode(200)
                .setHeader("Content-Type", "application/json")
                .setBody("""{"status":"running","progress":42,"step":"build"}"""),
        )

        val result = repositoryFor().fetchDeployStatus(jobId = "job-42")

        check(result is DeployStatusResult.Success)
        assertEquals("running", result.status.status)
        assertEquals(42L, result.status.progress)
        assertEquals("build", result.status.step)

        val recorded = server.takeRequest()
        assertEquals("GET", recorded.method)
        assertTrue(recorded.path!!.endsWith("/ops/deploy/job-42"))
    }

    @Test
    fun fetchStatusSurfacesErrorOnServerFailure() = runTest {
        server.enqueue(MockResponse().setResponseCode(500))

        val result = repositoryFor().fetchStatus()

        check(result is OpsStatusResult.Error)
    }

    @Test
    fun decodeOpsSnapshotParsesAnOpsHealthChannelEvent() {
        val element = Json.parseToJsonElement(
            """
            {
              "health": {"docker": "ok"},
              "health_ok": true,
              "queue_running": 1,
              "queue_queued": 0,
              "alerts": [{"name": "cpu-alta", "severity": "warning", "state": "firing", "current_value": 80.0, "threshold": 75.0}]
            }
            """.trimIndent(),
        )

        val snapshot = decodeOpsSnapshot(element)

        check(snapshot != null)
        assertEquals(true, snapshot.healthOk)
        assertEquals(1, snapshot.alerts.size)
        assertEquals("cpu-alta", snapshot.alerts[0].name)
    }

    @Test
    fun decodeOpsSnapshotReturnsNullOnMalformedPayload() {
        val element = Json.parseToJsonElement("""{"nao":"e um ops status"}""")

        assertNull(decodeOpsSnapshot(element))
    }

    @Test
    fun decodeDeployEventParsesADeployChannelLogLine() {
        val element = Json.parseToJsonElement(
            """{"type":"log","job_id":"job-42","log":"building...","ts":1234}""",
        )

        val event = decodeDeployEvent(element)

        check(event != null)
        assertEquals("log", event.type)
        assertEquals("job-42", event.jobId)
        assertEquals("building...", event.logLine)
    }

    @Test
    fun decodeDeployEventParsesATerminalStatusEvent() {
        val element = Json.parseToJsonElement(
            """{"type":"status","job_id":"job-42","status":"done","progress":100,"ts":1234}""",
        )

        val event = decodeDeployEvent(element)

        check(event != null)
        assertEquals("status", event.type)
        assertEquals("done", event.status)
        assertEquals(100, event.progress)
    }
}
