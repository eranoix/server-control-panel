package dev.servercontrolpanel.data.dashboard

import dev.servercontrolpanel.data.ops.OpsAlert
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class DashboardSnapshotTest {

    @Test
    fun `eight healthy subsystems are aggregated by the card`() {
        val snapshot = snapshotReal()
        assertEquals(8, snapshot.health.size)
        assertTrue(snapshot.health.all { it.severity == Severity.OK })
        assertEquals(Severity.OK, snapshot.healthSeverity)
    }

    @Test
    fun `connected counts as healthy because the BFF emits it for whatsapp`() {
        assertEquals(Severity.OK, classifyHealth("connected"))
        assertEquals(Severity.OK, classifyHealth("ok"))
    }

    @Test
    fun `an unknown new server state shows as a deviation, not as green`() {
        assertEquals(Severity.CRITICAL, classifyHealth("kaput"))
        assertEquals(Severity.WARNING, classifyHealth("degraded"))
    }

    @Test
    fun `the worst subsystem decides the whole group`() {
        val ops = opsReal().copy(health = opsReal().health + ("whatsapp" to "disconnected"))
        val snapshot = snapshotReal(ops = ops)
        assertEquals(Severity.CRITICAL, snapshot.healthSeverity)
        assertEquals("whatsapp", snapshot.health.first().name)
    }

    @Test
    fun `health_ok false with everything green is a contradiction and the worst is assumed`() {
        val snapshot = snapshotReal(ops = opsReal().copy(healthOk = false))
        assertEquals(Severity.WARNING, snapshot.healthSeverity)
    }

    @Test
    fun `the health_ok contradiction reaches the attention card, not only the rollup`() {
        val snapshot = snapshotReal(ops = opsReal().copy(healthOk = false))
        val signal = snapshot.attention.firstOrNull { it.id == "health:health_ok" }
        assertEquals(Severity.WARNING, signal?.severity)
        assertEquals(
            "the server reports health_ok = false, but no subsystem reports the problem",
            signal?.detail,
        )
    }

    @Test
    fun `when a subsystem already reports it, the generic health_ok line is not repeated`() {
        val ops = opsReal().copy(
            healthOk = false,
            health = opsReal().health + ("whatsapp" to "disconnected"),
        )
        val snapshot = snapshotReal(ops = ops)
        assertTrue(snapshot.attention.none { it.id == "health:health_ok" })
        assertTrue(snapshot.attention.any { it.id == "health:whatsapp" })
    }

    @Test
    fun `the real rolled back deploy appears in the attention list`() {
        val snapshot = snapshotReal()
        val deploy = snapshot.attention.firstOrNull { it.id == "deploy:hello" }
        assertEquals(Severity.WARNING, deploy?.severity)
        assertEquals(DashboardTarget.DEPLOYS, deploy?.target)
    }

    @Test
    fun `rollback is a warning and failure is critical`() {
        assertEquals(Severity.WARNING, classifyDeploy("rolled_back"))
        assertEquals(Severity.CRITICAL, classifyDeploy("failed"))
        assertEquals(Severity.OK, classifyDeploy("ok"))
        assertEquals(Severity.OK, classifyDeploy("running"))
    }

    @Test
    fun `the five real scheduled jobs are ok and none is promoted`() {
        val snapshot = snapshotReal()
        assertTrue(snapshot.brokenScheduled.isEmpty())
    }

    @Test
    fun `a failed scheduled job is promoted, a disabled one is not`() {
        val withFailure = realisticScheduled().toMutableList().also {
            it[0] = it[0].copy(lastStatus = "error")
            it[1] = it[1].copy(lastStatus = "error", enabled = false)
        }
        val snapshot = snapshotReal(scheduled = withFailure)
        assertEquals(1, snapshot.brokenScheduled.size)
        assertEquals(withFailure[0].name, snapshot.brokenScheduled.single().name)
    }

    @Test
    fun `server alerts and local thresholds coexist, sorted by the same scale`() {
        val ops = opsReal().copy(
            alerts = listOf(OpsAlert("disk_root", "critical", "firing", 92.0, 85.0, "%")),
        )
        val warning = snapshotReal(ops = ops).attention

        assertEquals(Severity.CRITICAL, warning.first().severity)
        assertEquals("alert:disk_root", warning.first().id)
        assertTrue(
            "derived signals must stay in the list: ${warning.map { it.id }}",
            warning.any { it.id == "swap" } && warning.any { it.id == "cpu" },
        )
        assertTrue(
            "steal must not return to the attention card",
            warning.none { it.id == "steal" },
        )
    }

    @Test
    fun `on a calm machine the attention list is empty`() {
        val calmOps = opsReal(
            system = productionLike(swapUsedPercent = 10.0, steal = 0.0, load1 = 1.0, rootUsedPercent = 20.0),
        )
        val snapshot = snapshotReal(
            ops = calmOps,
            deploys = listOf(DeploySummary("hello", "ok", "2026-07-19 13:17 UTC")),
        )
        assertTrue("got: ${snapshot.attention}", snapshot.attention.isEmpty())
    }

    @Test
    fun `a failed call becomes null, never an empty list, since unknown differs from none`() {
        val snapshot = snapshotReal(deploys = null, scheduled = null)
        assertEquals(null, snapshot.deploys)
        assertTrue(snapshot.brokenDeploys.isEmpty())
        assertTrue(snapshot.attention.none { it.id.startsWith("deploy:") })
    }
}
