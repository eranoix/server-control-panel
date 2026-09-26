package com.vpsmanager.data.sdui

import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.core.sdui.SduiDataSource
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

/**
 * Regression: the entire SDUI surface was unreachable in production.
 *
 * THE SYMPTOM. The Admin screen showed "This section does not exist" for EVERY
 * section, even though the server answered 200 to
 * `GET /api/mobile/v1/screens/scheduler.jobs` with the complete descriptor.
 *
 * THE CAUSE. [SduiDataClient] inherited from `ApiClient` the base
 * `MobileApi.defaultBasePath`, which in production is already
 * `https://server/api/mobile/v1` (published by
 * `ServerConfigRepository.publishLegacyBasePathSeam`). But [SduiDataClient.call]
 * takes an ABSOLUTE path from the root of the server — the prefix went in twice
 * and the final URL became
 * `/api/mobile/v1/api/mobile/v1/screens/scheduler.jobs`, which the server does
 * not register: 404 → [SduiScreenResult.NotFound] → "This section does not exist".
 *
 * WHY THE SUITE DID NOT CATCH IT. The three SDUI suites build the client with
 * `server.url("/")` — a base WITHOUT the BFF prefix, the one shape in which the
 * concatenation happened to work. The fixture was hiding the defect. That is
 * why this suite uses `server.url("/api/mobile/v1")`: exactly the shape
 * production publishes.
 *
 * THE FAKE SERVER IS ROUTED, NOT ENQUEUED. An `enqueue` answers 200 for any
 * path and so could never fail a wrong URL; the [Dispatcher] below answers 404
 * outside the real routes, just like the real server — which is what makes this
 * test fail before the fix.
 */
class SduiProductionBasePathTest {

    private lateinit var server: MockWebServer

    /**
     * The real body of `GET /api/mobile/v1/screens/scheduler.jobs` in
     * production, for an admin viewer (`{"user":"teste","is_admin":true}`) —
     * the true contract, assembled by `internal/mobilebff/screens/scheduler.go`.
     */
    private val schedulerJobsEnvelope = """
        {"sdui_version":1,
         "screen":{"id":"scheduler.jobs","title":"Scheduler",
           "components":[
             {"type":"table","id":"jobs-table",
              "columns":[
                {"key":"name","label":"Nome","kind":"text"},
                {"key":"schedule","label":"Agenda","kind":"text"},
                {"key":"kind","label":"Tipo","kind":"text"},
                {"key":"last_status","label":"Último status","kind":"badge",
                 "badge_map":{"ok":"success","failed":"danger","skipped":"neutral"}},
                {"key":"next_fire","label":"Próxima execução","kind":"text"}],
              "rows_source":{"endpoint":"/api/mobile/v1/scheduler/jobs"},
              "row_actions":[
                {"action_id":"scheduler.job.run_now","label":"Executar agora","style":"secondary"},
                {"action_id":"scheduler.job.delete","label":"Excluir","style":"destructive"}],
              "empty_state":{"text":"Nenhum job agendado ainda."}},
             {"type":"form","id":"job-form",
              "fields":[
                {"key":"name","label":"Nome","kind":"text","required":true},
                {"key":"schedule","label":"Agenda (cron)","kind":"text","required":true,"placeholder":"*/15 * * * *"},
                {"key":"kind","label":"Tipo","kind":"select","required":true,"options":["shell","backup"]},
                {"key":"enabled","label":"Ativo","kind":"bool"},
                {"key":"run_as_root","label":"Executar como root","kind":"bool"}],
              "submit_action":{"action_id":"scheduler.job.save","label":"Salvar","style":"primary"}},
             {"type":"action","id":"refresh-jobs","label":"Atualizar",
              "action_id":"scheduler.jobs.refresh","style":"secondary"},
             {"type":"confirm_destructive","id":"job-delete-confirm",
              "action_id":"scheduler.job.delete",
              "message":"Este job será excluído permanentemente. Essa ação não pode ser desfeita."}]}}
    """.trimIndent()

    private val jobsRows = """[{"name":"backup-diario","schedule":"0 3 * * *","kind":"backup","last_status":"ok","next_fire":"03:00"}]"""

    @Before
    fun setUp() {
        server = MockWebServer()
        // Only the routes the BFF actually registers answer; anything else
        // returns 404, like the real server (which on top of that uses
        // 404-instead-of-403 as an anti-enumeration stance).
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

    /**
     * The base exactly as `ServerConfigRepository.publishLegacyBasePathSeam`
     * publishes it: the server URL ALREADY with `/api/mobile/v1` at the end.
     */
    private fun productionClient() = SduiDataClient(basePath = server.url("/api/mobile/v1").toString())

    @Test
    fun `serverRootOf remove o prefixo do BFF que a producao ja embute na base`() {
        assertEquals("https://panel.northwind.example", serverRootOf("https://panel.northwind.example/api/mobile/v1"))
        assertEquals("https://panel.northwind.example", serverRootOf("https://panel.northwind.example/api/mobile/v1/"))
        // Installation under a sub-path: keeps the sub-path, does not collapse to the "origin".
        assertEquals("https://host/vpsm", serverRootOf("https://host/vpsm/api/mobile/v1"))
        // App not configured yet: a relative base comes out empty, and empty is
        // rejected by the ApiClient — fails loudly, never falls back to localhost.
        assertEquals("", serverRootOf("/api/mobile/v1"))
    }

    @Test
    fun `screen busca o descritor na rota real, e nao no prefixo do BFF duplicado`() = runTest {
        val client = productionClient()

        val result = SduiRepository(client, SduiActionRepository(client)).screen("scheduler.jobs")

        assertTrue("esperava Success, veio $result", result is SduiScreenResult.Success)
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
    fun `rows_source do descritor tambem resolve contra a raiz do servidor`() = runTest {
        val result = SduiDataRepository(productionClient())
            .fetch(SduiDataSource(endpoint = "/api/mobile/v1/scheduler/jobs"))

        assertTrue("esperava Success, veio $result", result is SduiDataResult.Success)
        assertEquals("/api/mobile/v1/scheduler/jobs", server.takeRequest().path)
    }

    @Test
    fun `acao de linha tambem resolve contra a raiz do servidor`() = runTest {
        val result = SduiActionRepository(productionClient())
            .invoke("scheduler.job.run_now", JsonObject(emptyMap()))

        assertTrue("esperava Success, veio $result", result is SduiActionHttpResult.Success)
        assertEquals("/api/mobile/v1/actions/scheduler.job.run_now", server.takeRequest().path)
    }
}
