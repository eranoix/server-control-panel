package com.vpsmanager.data.dashboard

import com.vpsmanager.data.ops.OpsAlert
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Threshold grading measured against a snapshot of the real machine (see [DashboardFixtures]),
 * where `alerts` is empty and `health_ok = true` yet the resources still need attention.
 */
class ResourceSignalsTest {

    private fun signal(id: String, system: com.vpsmanager.data.ops.SystemSnapshot = productionLike()) =
        gradeResources(system).first { it.id == id }

    @Test
    fun `the real machine is not graded healthy, swap and load raise signals`() {
        val warning = attentionSignals(gradeResources(productionLike()))

        assertTrue(
            "expected attention signals on this machine, got: $warning",
            warning.isNotEmpty(),
        )
        assertNotNull("swap at 100% must raise a signal", warning.firstOrNull { it.id == "swap" })
        assertNotNull("load 11.97 on 8 cores must raise a signal", warning.firstOrNull { it.id == "cpu" })
    }

    /**
     * Steal is never an alert: nothing inside the VM can change it, and a daily
     * unfixable alarm teaches people to ignore red.
     */
    @Test
    fun `high steal does not reach the attention card`() {
        val warning = attentionSignals(gradeResources(productionLike(steal = 37.0)))

        assertEquals(
            "steal must not become an alert, nothing can be done from inside the VM",
            null,
            warning.firstOrNull { it.id == "steal" },
        )
    }

    @Test
    fun `swap at 100 percent with free RAM is a warning, not critical`() {
        // With RAM to spare, full swap is normal after long uptime.
        assertEquals(Severity.WARNING, signal("swap").severity)
    }

    @Test
    fun `full swap with RAM at the limit is critical because it leads to OOM`() {
        val system = productionLike(memUsedPercent = 96.0)
        assertEquals(Severity.CRITICAL, signal("swap", system).severity)
    }

    @Test
    fun `swap with headroom raises no signal`() {
        val system = productionLike(swapUsedPercent = 40.0)
        assertEquals(Severity.OK, signal("swap", system).severity)
    }

    @Test
    fun `the swap threshold is exactly 95 percent, inclusive`() {
        assertEquals(Severity.OK, signal("swap", productionLike(swapUsedPercent = 94.9)).severity)
        assertEquals(Severity.WARNING, signal("swap", productionLike(swapUsedPercent = 95.0)).severity)
    }

    @Test
    fun `the swap detail explains the threshold instead of repeating the number`() {
        val swap = signal("swap")
        assertTrue(
            "the detail must say why 100% swap matters: ${swap.detail}",
            swap.detail.contains("no room left to page"),
        )
    }

    @Test
    fun `steal is always OK, information but never an alert`() {
        // The number stays visible, it just never raises severity.
        assertEquals(Severity.OK, signal("steal").severity)
        assertEquals(Severity.OK, signal("steal", productionLike(steal = 20.0)).severity)
        assertEquals(Severity.OK, signal("steal", productionLike(steal = 37.0)).severity)
    }

    /** Steal still informs: it explains a slow machine whose usage is not high. */
    @Test
    fun `high steal keeps its number and explanation`() {
        val s = signal("steal", productionLike(steal = 37.0))

        assertEquals("37%", s.headline)
        assertTrue(s.detail.contains("cannot be fixed from inside the VM"))
    }

    @Test
    fun `low steal says the hypervisor delivers the paid CPU`() {
        val s = signal("steal", productionLike(steal = 4.9))

        assertEquals(Severity.OK, s.severity)
        assertTrue(s.detail.contains("delivering the CPU you pay for"))
    }

    @Test
    fun `the steal detail says it cannot be fixed from inside the VM`() {
        assertTrue(signal("steal").detail.contains("cannot be fixed from inside the VM"))
    }

    @Test
    fun `CPU is graded by load per core, not by instant usage`() {
        // High usage with low load per core is a spike, not a queue.
        val peak = productionLike(load1 = 3.2)
        assertEquals(Severity.OK, signal("cpu", peak).severity)
        assertEquals("91%", signal("cpu", peak).headline)
    }

    @Test
    fun `load equal to the core count is already a warning`() {
        assertEquals(Severity.WARNING, signal("cpu", productionLike(load1 = 8.0)).severity)
        assertEquals(Severity.OK, signal("cpu", productionLike(load1 = 7.9)).severity)
    }

    @Test
    fun `load at twice the core count is critical`() {
        assertEquals(Severity.CRITICAL, signal("cpu", productionLike(load1 = 16.0)).severity)
    }

    @Test
    fun `without a core count the panel does not grade instead of dividing by zero`() {
        val withoutCores = productionLike().let { real ->
            real.copy(cpu = real.cpu.copy(cores = 0))
        }
        assertEquals(Severity.OK, signal("cpu", withoutCores).severity)
    }

    @Test
    fun `zero iowait raises no signal and high iowait does`() {
        assertEquals(Severity.OK, signal("iowait").severity)
        assertEquals(Severity.WARNING, signal("iowait", productionLike(iowait = 12.0)).severity)
        assertEquals(Severity.CRITICAL, signal("iowait", productionLike(iowait = 30.0)).severity)
    }

    @Test
    fun `memory at 63 percent is ok, at 90 a warning, at 96 critical`() {
        assertEquals(Severity.OK, signal("memoria").severity)
        assertEquals(Severity.WARNING, signal("memoria", productionLike(memUsedPercent = 90.0)).severity)
        assertEquals(Severity.CRITICAL, signal("memoria", productionLike(memUsedPercent = 96.0)).severity)
    }

    @Test
    fun `the root disk at 73 percent is ok and at 92 is a warning`() {
        assertEquals(Severity.OK, signal("disco:/").severity)
        assertEquals(Severity.WARNING, signal("disco:/", productionLike(rootUsedPercent = 92.0)).severity)
        assertEquals(Severity.CRITICAL, signal("disco:/", productionLike(rootUsedPercent = 96.0)).severity)
    }

    @Test
    fun `each mount point gets its own signal`() {
        val ids = gradeResources(productionLike()).map { it.id }
        assertTrue(ids.containsAll(listOf("disco:/", "disco:/boot", "disco:/boot/efi")))
    }

    @Test
    fun `network is never graded because there is no honest traffic threshold`() {
        val network = signal("rede")
        assertEquals(Severity.OK, network.severity)
        assertTrue("got: ${network.detail}", network.detail.contains("147.5 KiB/s"))
        assertTrue("got: ${network.detail}", network.detail.contains("256.3 KiB/s"))
    }

    @Test
    fun `network leaves the value column empty because two rates do not fit`() {
        // Two rates wrap badly at phone width; an empty headline hides the column.
        assertEquals("", signal("rede").headline)
        assertTrue(gradeResources(productionLike()).filter { it.id != "rede" }.all { it.headline.isNotBlank() })
    }

    @Test
    fun `the worst signal comes first in the attention list`() {
        // Memory at 97% makes swap critical too (no room to page and no RAM to allocate).
        val system = productionLike(memUsedPercent = 97.0)
        val warning = attentionSignals(gradeResources(system))

        // Pins the rule (worst first), not which of the tied critical signals wins.
        assertEquals(Severity.CRITICAL, warning.first().severity)
        assertTrue(
            "both critical signals must be on top: ${warning.map { it.id }}",
            warning.take(2).map { it.id }.containsAll(listOf("memoria", "swap")),
        )
    }

    @Test
    fun `resource order is stable regardless of severity`() {
        val calm = gradeResources(productionLike(swapUsedPercent = 1.0, steal = 0.0, load1 = 0.5))
        val chaotic = gradeResources(productionLike(steal = 40.0))
        assertEquals(calm.map { it.id }, chaotic.map { it.id })
    }

    @Test
    fun `worstOf returns the worse of two, the group rollup operation`() {
        assertEquals(Severity.CRITICAL, worstOf(Severity.WARNING, Severity.CRITICAL))
        assertEquals(Severity.WARNING, worstOf(Severity.WARNING, Severity.OK))
        assertEquals(Severity.OK, worstOf(Severity.OK, Severity.OK))
    }

    @Test
    fun `a server alert is never downgraded, critical stays critical`() {
        val alert = OpsAlert(
            name = "disk_root",
            severity = "critical",
            state = "firing",
            currentValue = 92.0,
            threshold = 85.0,
            unit = "%",
        ).toSignal()

        assertEquals(Severity.CRITICAL, alert.severity)
        assertEquals("92 %", alert.headline)
        assertEquals(DashboardTarget.ALERTS, alert.target)
    }

    @Test
    fun `an unknown server severity becomes a warning, never silence`() {
        val alert = OpsAlert("queue_lag", "warn", "firing", 310.0, 120.0, "s").toSignal()
        assertEquals(Severity.WARNING, alert.severity)
        assertTrue(alert.detail.contains("threshold 120 s"))
    }

    @Test
    fun `an alert without a unit prints no stray space`() {
        val alert = OpsAlert("rule", "warn", "firing", 3.5, 2.0, null).toSignal()
        assertEquals("3.5", alert.headline)
    }

    @Test
    fun `every signal points to a screen`() {
        gradeResources(productionLike()).forEach { signal ->
            assertNotNull("signal ${signal.id} has no target", signal.target)
        }
    }

    @Test
    fun `without the system block the panel invents no resources`() {
        val snapshot = snapshotReal(ops = opsReal(system = null))
        assertTrue(snapshot.resourceSignals.isEmpty())
        assertNull(snapshot.ops.system)
    }
}
