package dev.servercontrolpanel.data.sdui

import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.core.sdui.SduiDataSource
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class SduiProductionBasePathTest {

    private lateinit var server: MockWebServer

    private val schedulerJobsEnvelope = """
        {"sdui_version":1,
         "screen":{"id":"scheduler.jobs","title":"Scheduler",
           "components":[
             {"type":"table","id":"jobs-table",
              "columns":[
                {"key":"name","label":"Name","kind":"text"},
                {"key":"schedule","label":"Agenda","kind":"text"},
                {"key":"kind","label":"Type","kind":"text"},
                {"key":"last_status","label":"Last status","kind":"badge",
                 "badge_map":{"ok":"success","failed":"danger","skipped":"neutral"}},
                {"key":"next_fire","label":"Next run","kind":"text"}],
              "rows_source":{"endpoint":"/api/mobile/v1/scheduler/jobs"},
              "row_actions":[
                {"action_id":"scheduler.job.run_now","label":"Run now","style":"secondary"},
                {"action_id":"scheduler.job.delete","label":"Delete","style":"destructive"}],
              "empty_state":{"text":"No jobs scheduled yet."}},
             {"type":"form","id":"job-form",
              "fields":[
                {"key":"name","label":"Name","kind":"text","required":true},
                {"key":"schedule","label":"Agenda (cron)","kind":"text","required":true,"placeholder":"*/15 * * * *"},
                {"key":"kind","label":"Type","kind":"select","required":true,"options":["shell","backup"]},
                {"key":"enabled","label":"Active","kind":"bool"},
                {"key":"run_as_root","label":"Run as root","kind":"bool"}],
              "submit_action":{"action_id":"scheduler.job.save","label":"Save","style":"primary"}},
             {"type":"action","id":"refresh-jobs","label":"Refresh",
              "action_id":"scheduler.jobs.refresh","style":"secondary"},
             {"type":"confirm_destructive","id":"job-delete-confirm",
              "action_id":"scheduler.job.delete",
              "message":"This job will be deleted permanently. This action cannot be undone."}]}}
    """.trimIndent()

    private val jobsRows = """[{"name":"backup-daily","schedule":"0 3 * * *","kind":"backup","last_status":"ok","next_fire":"03:00"}]"""

    @Before
    fun setUp() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse = when (request.path) {
                "/api/mobile/v1/screens/scheduler.jobs" -> json(schedulerJobsEnvelope)
                "/api/mobile/v1/scheduler/jobs" -> json(jobsRows)
                "/api/mobile/v1/actions/scheduler.job.run_now" -> json("""{"patch":{"id":"job-1"}}""")
                else -> MockResponse().setResponseCode(404)
            }
        }
        server.start()
    }

    private fun json(body: String) = MockResponse()
        .setResponseCode(200)
        .setHeader("Content-Type", "application/json")
        .setBody(body)

    @After
    fun tearDown() {
        server.shutdown()
    }

    private fun productionClient() = SduiDataClient(basePath = server.url("/api/mobile/v1").toString())

    @Test
    fun `serverRootOf strips the BFF prefix that production puts in the base`() {
        assertEquals("https://panel.northwind.example", serverRootOf("https://panel.northwind.example/api/mobile/v1"))
        assertEquals("https://panel.northwind.example", serverRootOf("https://panel.northwind.example/api/mobile/v1/"))
        assertEquals("https://host/panel", serverRootOf("https://host/panel/api/mobile/v1"))
        assertEquals("", serverRootOf("/api/mobile/v1"))
    }

    @Test
    fun `screen fetches the descriptor from the real route, without a doubled BFF prefix`() = runTest {
        val client = productionClient()

        val result = SduiRepository(client, SduiActionRepository(client)).screen("scheduler.jobs")

        assertTrue("expected Success, got $result", result is SduiScreenResult.Success)
        val envelope = (result as SduiScreenResult.Success).envelope
        assertEquals("scheduler.jobs", envelope.screen.id)
        assertEquals("Scheduler", envelope.screen.title)
        val table = envelope.screen.components.first() as SduiComponent.Table
        assertEquals("jobs-table", table.id)
        assertEquals(5, table.columns.size)
        assertEquals("/api/mobile/v1/scheduler/jobs", table.rowsSource.endpoint)
        assertEquals("/api/mobile/v1/screens/scheduler.jobs", server.takeRequest().path)
    }

    @Test
    fun `the descriptor rows_source also resolves against the server root`() = runTest {
        val result = SduiDataRepository(productionClient())
            .fetch(SduiDataSource(endpoint = "/api/mobile/v1/scheduler/jobs"))

        assertTrue("expected Success, got $result", result is SduiDataResult.Success)
        assertEquals("/api/mobile/v1/scheduler/jobs", server.takeRequest().path)
    }

    @Test
    fun `a row action also resolves against the server root`() = runTest {
        val result = SduiActionRepository(productionClient())
            .invoke("scheduler.job.run_now", JsonObject(emptyMap()))

        assertTrue("expected Success, got $result", result is SduiActionHttpResult.Success)
        assertEquals("/api/mobile/v1/actions/scheduler.job.run_now", server.takeRequest().path)
    }
}
