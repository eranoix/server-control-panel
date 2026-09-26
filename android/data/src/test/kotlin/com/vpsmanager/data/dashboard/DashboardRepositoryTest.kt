package com.vpsmanager.data.dashboard

import com.vpsmanager.data.ops.DeployStatusResult
import com.vpsmanager.data.ops.OpsSource
import com.vpsmanager.data.ops.OpsStatusResult
import com.vpsmanager.data.ops.TriggerDeployResult
import com.vpsmanager.data.session.SessionResult
import com.vpsmanager.data.session.SessionSource
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The four calls joined together, and what happens when each one falls over. */
class DashboardRepositoryTest {

    @Test
    fun `junta ops, identidade, deploys e agendados num snapshot so`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Success("teste", "test@northwind.example", isAdmin = true)),
            rows = FakeRows(
                mapOf(
                    "/api/mobile/v1/deploy/apps" to linhas(
                        """{"id":"hello","name":"hello","last_status":"rolled_back","updated":"2026-07-19 13:17 UTC"}""",
                    ),
                    "/api/mobile/v1/scheduler/jobs" to linhas(
                        """{"id":"sc_1","name":"Backup","last_status":"ok","last_fire":"07:00","next_fire":"07:10","enabled":true}""",
                    ),
                ),
            ),
            now = { 1_788_678_502_000 },
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot

        assertEquals("teste", snapshot.identity?.user)
        assertEquals(true, snapshot.identity?.isAdmin)
        assertEquals("rolled_back", snapshot.deploys?.single()?.lastStatus)
        assertEquals("Backup", snapshot.scheduled?.single()?.name)
        assertEquals(true, snapshot.scheduled?.single()?.enabled)
        assertEquals(1_788_678_502_000, snapshot.fetchedAtEpochMs)
    }

    @Test
    fun `ops fora do ar derruba o painel com o que fazer — e a unica chamada indispensavel`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Error("Falha de conexão. Verifique a rede e tente novamente.")),
            session = FakeSession(SessionResult.Success("teste", "t@t", isAdmin = true)),
            rows = FakeRows(emptyMap()),
        )

        val result = repo.load()
        assertTrue(result is DashboardResult.Error)
        assertEquals(
            "Falha de conexão. Verifique a rede e tente novamente.",
            (result as DashboardResult.Error).reason,
        )
    }

    @Test
    fun `deploy fora do ar deixa aquele pedaco nulo e NAO derruba o painel`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Success("teste", "t@t", isAdmin = true)),
            rows = FakeRows(emptyMap()), // toda busca de linha devolve null
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot
        assertNull(snapshot.deploys)
        assertNull(snapshot.scheduled)
        // and the rest stays standing, with the resources judged
        assertTrue(snapshot.resourceSignals.isNotEmpty())
    }

    @Test
    fun `identidade fora do ar vira rodape sem nome, nao tela de erro`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Error("O servidor está indisponível no momento.")),
            rows = FakeRows(emptyMap()),
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot
        assertNull(snapshot.identity)
        assertEquals(8, snapshot.health.size)
    }

    @Test
    fun `linha sem nome cai para o id em vez de virar rotulo vazio`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Empty),
            rows = FakeRows(
                mapOf(
                    "/api/mobile/v1/deploy/apps" to linhas("""{"id":"hello","last_status":"ok","updated":"x"}"""),
                ),
            ),
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot
        assertEquals("hello", snapshot.deploys?.single()?.name)
    }
}

private fun linhas(vararg json: String): List<JsonObject> =
    json.map { Json.parseToJsonElement(it).asRow() }

private class FakeOps(private val result: OpsStatusResult) : OpsSource {
    override suspend fun fetchStatus() = result
    override suspend fun triggerDeploy() = TriggerDeployResult.Error("não usado")
    override suspend fun fetchDeployStatus(jobId: String) = DeployStatusResult.Error("não usado")
}

private class FakeSession(private val result: SessionResult) : SessionSource {
    override suspend fun getMe() = result
}

private class FakeRows(private val porEndpoint: Map<String, List<JsonObject>>) : RowsSource {
    override suspend fun fetch(endpoint: String): List<JsonObject>? = porEndpoint[endpoint]
}
