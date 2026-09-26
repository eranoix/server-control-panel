package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.DeployStatusResult
import dev.servercontrolpanel.data.ops.OpsSource
import dev.servercontrolpanel.data.ops.OpsStatusResult
import dev.servercontrolpanel.data.ops.TriggerDeployResult
import dev.servercontrolpanel.data.session.SessionResult
import dev.servercontrolpanel.data.session.SessionSource
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The four calls joined together, and what happens when each one fails. */
class DashboardRepositoryTest {

    @Test
    fun `joins ops, identity, deploys and scheduled jobs into one snapshot`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Success("tester", "test@northwind.example", isAdmin = true)),
            rows = FakeRows(
                mapOf(
                    "/api/mobile/v1/deploy/apps" to lines(
                        """{"id":"hello","name":"hello","last_status":"rolled_back","updated":"2026-07-19 13:17 UTC"}""",
                    ),
                    "/api/mobile/v1/scheduler/jobs" to lines(
                        """{"id":"sc_1","name":"Backup","last_status":"ok","last_fire":"07:00","next_fire":"07:10","enabled":true}""",
                    ),
                ),
            ),
            now = { 1_788_678_502_000 },
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot

        assertEquals("tester", snapshot.identity?.user)
        assertEquals(true, snapshot.identity?.isAdmin)
        assertEquals("rolled_back", snapshot.deploys?.single()?.lastStatus)
        assertEquals("Backup", snapshot.scheduled?.single()?.name)
        assertEquals(true, snapshot.scheduled?.single()?.enabled)
        assertEquals(1_788_678_502_000, snapshot.fetchedAtEpochMs)
    }

    @Test
    fun `ops being down fails the dashboard because it is the only required call`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Error("Connection failed. Check the network and try again.")),
            session = FakeSession(SessionResult.Success("tester", "t@t", isAdmin = true)),
            rows = FakeRows(emptyMap()),
        )

        val result = repo.load()
        assertTrue(result is DashboardResult.Error)
        assertEquals(
            "Connection failed. Check the network and try again.",
            (result as DashboardResult.Error).reason,
        )
    }

    @Test
    fun `deploys being down leaves that part null without failing the dashboard`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Success("tester", "t@t", isAdmin = true)),
            rows = FakeRows(emptyMap()), // every row fetch returns null
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot
        assertNull(snapshot.deploys)
        assertNull(snapshot.scheduled)
        // the rest still loads, with resources graded
        assertTrue(snapshot.resourceSignals.isNotEmpty())
    }

    @Test
    fun `identity being down leaves a nameless footer, not an error screen`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Error("The server is unavailable right now.")),
            rows = FakeRows(emptyMap()),
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot
        assertNull(snapshot.identity)
        assertEquals(8, snapshot.health.size)
    }

    @Test
    fun `a row without a name falls back to its id instead of an empty label`() = runTest {
        val repo = DashboardRepository(
            ops = FakeOps(OpsStatusResult.Success(opsReal())),
            session = FakeSession(SessionResult.Empty),
            rows = FakeRows(
                mapOf(
                    "/api/mobile/v1/deploy/apps" to lines("""{"id":"hello","last_status":"ok","updated":"x"}"""),
                ),
            ),
        )

        val snapshot = (repo.load() as DashboardResult.Success).snapshot
        assertEquals("hello", snapshot.deploys?.single()?.name)
    }
}

private fun lines(vararg json: String): List<JsonObject> =
    json.map { Json.parseToJsonElement(it).asRow() }

private class FakeOps(private val result: OpsStatusResult) : OpsSource {
    override suspend fun fetchStatus() = result
    override suspend fun triggerDeploy() = TriggerDeployResult.Error("unused")
    override suspend fun fetchDeployStatus(jobId: String) = DeployStatusResult.Error("unused")
}

private class FakeSession(private val result: SessionResult) : SessionSource {
    override suspend fun getMe() = result
}

private class FakeRows(private val byEndpoint: Map<String, List<JsonObject>>) : RowsSource {
    override suspend fun fetch(endpoint: String): List<JsonObject>? = byEndpoint[endpoint]
}
