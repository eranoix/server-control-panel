package com.vpsmanager.data.dashboard

import com.vpsmanager.data.ops.OpsAlert
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Aggregation, health rollup and the list on card number one. */
class DashboardSnapshotTest {

    @Test
    fun `oito subsistemas ok NAO viram oito linhas — o cartao agrega`() {
        val snapshot = snapshotReal()
        assertEquals(8, snapshot.health.size)
        assertTrue(snapshot.health.all { it.severity == Severity.OK })
        assertEquals(Severity.OK, snapshot.healthSeverity)
    }

    @Test
    fun `connected conta como saudavel — e o valor que o BFF emite para o whatsapp`() {
        assertEquals(Severity.OK, classifyHealth("connected"))
        assertEquals(Severity.OK, classifyHealth("ok"))
    }

    @Test
    fun `um estado novo inventado no servidor aparece como desvio, nao some como verde`() {
        assertEquals(Severity.CRITICAL, classifyHealth("kaput"))
        assertEquals(Severity.WARNING, classifyHealth("degraded"))
    }

    @Test
    fun `o pior subsistema manda no grupo inteiro`() {
        val ops = opsReal().copy(health = opsReal().health + ("whatsapp" to "disconnected"))
        val snapshot = snapshotReal(ops = ops)
        assertEquals(Severity.CRITICAL, snapshot.healthSeverity)
        assertEquals("whatsapp", snapshot.health.first().name)
    }

    @Test
    fun `health_ok false com tudo verde e contradicao — o painel acredita no pior`() {
        val snapshot = snapshotReal(ops = opsReal().copy(healthOk = false))
        assertEquals(Severity.WARNING, snapshot.healthSeverity)
    }

    @Test
    fun `a contradicao do health_ok chega ao cartao de atencao, nao so ao rollup`() {
        val snapshot = snapshotReal(ops = opsReal().copy(healthOk = false))
        val signal = snapshot.attention.firstOrNull { it.id == "saude:health_ok" }
        assertEquals(Severity.WARNING, signal?.severity)
        assertEquals(
            "the server reports health_ok = false, but no subsystem reports the problem",
            signal?.detail,
        )
    }

    @Test
    fun `com um subsistema ja acusando, a linha generica de health_ok nao se repete`() {
        val ops = opsReal().copy(
            healthOk = false,
            health = opsReal().health + ("whatsapp" to "disconnected"),
        )
        val snapshot = snapshotReal(ops = ops)
        assertTrue(snapshot.attention.none { it.id == "saude:health_ok" })
        assertTrue(snapshot.attention.any { it.id == "saude:whatsapp" })
    }

    @Test
    fun `o deploy revertido de verdade aparece na lista de atencao`() {
        val snapshot = snapshotReal()
        val deploy = snapshot.attention.firstOrNull { it.id == "deploy:hello" }
        assertEquals(Severity.WARNING, deploy?.severity)
        assertEquals(DashboardTarget.DEPLOYS, deploy?.target)
    }

    @Test
    fun `rollback e atencao e falha e critico — a maquina que se salvou nao e incidente em curso`() {
        assertEquals(Severity.WARNING, classifyDeploy("rolled_back"))
        assertEquals(Severity.CRITICAL, classifyDeploy("failed"))
        assertEquals(Severity.OK, classifyDeploy("ok"))
        assertEquals(Severity.OK, classifyDeploy("running"))
    }

    @Test
    fun `os cinco agendados reais estao ok e nenhum sobe para o topo`() {
        val snapshot = snapshotReal()
        assertTrue(snapshot.brokenScheduled.isEmpty())
    }

    @Test
    fun `um agendado que falhou sobe, um agendado desligado nao`() {
        val withFailure = realisticScheduled().toMutableList().also {
            it[0] = it[0].copy(lastStatus = "error")
            it[1] = it[1].copy(lastStatus = "error", enabled = false)
        }
        val snapshot = snapshotReal(scheduled = withFailure)
        assertEquals(1, snapshot.brokenScheduled.size)
        assertEquals(withFailure[0].name, snapshot.brokenScheduled.single().name)
    }

    @Test
    fun `alertas do servidor e limiares locais convivem, ordenados pela mesma regua`() {
        val ops = opsReal().copy(
            alerts = listOf(OpsAlert("disk_root", "critical", "firing", 92.0, 85.0, "%")),
        )
        val warning = snapshotReal(ops = ops).attention

        assertEquals(Severity.CRITICAL, warning.first().severity)
        assertEquals("alerta:disk_root", warning.first().id)
        assertTrue(
            "os sinais derivados têm que continuar na lista: ${warning.map { it.id }}",
            warning.any { it.id == "swap" } && warning.any { it.id == "cpu" },
        )
        // `steal` left this list on purpose in 0.1.30: it is information, not
        // an alert, because there is no action possible from inside the VM.
        // See stealSignal in ResourceSignals.kt.
        assertTrue(
            "steal nao pode voltar para o cartao de atencao",
            warning.none { it.id == "steal" },
        )
    }

    @Test
    fun `numa maquina calma a lista de atencao fica vazia — silencio e a boa noticia`() {
        val calmOps = opsReal(
            system = productionLike(swapUsedPercent = 10.0, steal = 0.0, load1 = 1.0, rootUsedPercent = 20.0),
        )
        val snapshot = snapshotReal(
            ops = calmOps,
            deploys = listOf(DeploySummary("hello", "ok", "2026-07-19 13:17 UTC")),
        )
        assertTrue("veio: ${snapshot.attention}", snapshot.attention.isEmpty())
    }

    @Test
    fun `chamada que falhou vira nulo, nunca lista vazia — nao sei e diferente de nao ha`() {
        val snapshot = snapshotReal(deploys = null, scheduled = null)
        assertEquals(null, snapshot.deploys)
        assertTrue(snapshot.brokenDeploys.isEmpty())
        assertTrue(snapshot.attention.none { it.id.startsWith("deploy:") })
    }
}
