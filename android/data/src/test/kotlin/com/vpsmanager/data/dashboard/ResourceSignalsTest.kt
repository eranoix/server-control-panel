package com.vpsmanager.data.dashboard

import com.vpsmanager.data.ops.OpsAlert
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The threshold judgement, measured against the REAL MACHINE (see
 * [DashboardFixtures]).
 *
 * The test that gives the suite its name is the first one: on this machine,
 * with `alerts` empty and `health_ok = true`, a naive dashboard would show
 * "all good". These tests pin the opposite.
 */
class ResourceSignalsTest {

    private fun sinal(id: String, system: com.vpsmanager.data.ops.SystemSnapshot = producaoReal()) =
        gradeResources(system).first { it.id == id }

    @Test
    fun `a maquina real NAO passa por saudavel — swap sem folga e carga sobem`() {
        val atencao = attentionSignals(gradeResources(producaoReal()))

        assertTrue(
            "esperava sinais de atenção nesta máquina, veio: $atencao",
            atencao.isNotEmpty(),
        )
        assertNotNull("swap a 100% tem que virar sinal", atencao.firstOrNull { it.id == "swap" })
        assertNotNull("load 11,97 em 8 núcleos tem que virar sinal", atencao.firstOrNull { it.id == "cpu" })
    }

    /**
     * STEAL IS NO LONGER AN ALERT (0.1.30) — and this test exists so that it
     * does not go back to being one.
     *
     * The owner reported it, with a photo: "when stolen cpu shows up it keeps
     * popping up something needs attention. i don't want that". He is right,
     * and the reason was already written in the code itself: steal "cannot be
     * fixed from inside the VM".
     *
     * An alert exists to provoke ACTION. There is no command, no file and no
     * reboot on this machine that changes the number — the only answer is to
     * resize or switch provider, a contract decision taken once a year.
     * Alerting every day about something unfixable is the false alarm the
     * header of ResourceSignals.kt tells us to avoid, and its cost is not the
     * annoyance: it is red ceasing to mean anything the day a disk really
     * fills up.
     */
    @Test
    fun `steal alto NAO sobe para o cartao de atencao`() {
        val atencao = attentionSignals(gradeResources(producaoReal(steal = 37.0)))

        assertEquals(
            "steal nao pode virar alerta: nao ha acao possivel de dentro da VM",
            null,
            atencao.firstOrNull { it.id == "steal" },
        )
    }

    // ── swap ────────────────────────────────────────────────────────────────

    @Test
    fun `swap a 100 por cento com RAM folgada e ATENCAO, nao CRITICO`() {
        // A machine 18 days up with 37% of RAM free: full swap is the normal
        // state of long uptime. Red here every day is the false alarm that
        // teaches people to ignore red.
        assertEquals(Severity.ATENCAO, sinal("swap").severity)
    }

    @Test
    fun `swap cheio COM a RAM no limite vira CRITICO — e a receita do OOM`() {
        val system = producaoReal(memUsedPercent = 96.0)
        assertEquals(Severity.CRITICO, sinal("swap", system).severity)
    }

    @Test
    fun `swap com folga nao levanta sinal`() {
        val system = producaoReal(swapUsedPercent = 40.0)
        assertEquals(Severity.OK, sinal("swap", system).severity)
    }

    @Test
    fun `o limiar de swap e exatamente 95 por cento, inclusive`() {
        assertEquals(Severity.OK, sinal("swap", producaoReal(swapUsedPercent = 94.9)).severity)
        assertEquals(Severity.ATENCAO, sinal("swap", producaoReal(swapUsedPercent = 95.0)).severity)
    }

    @Test
    fun `o detalhe do swap explica o limiar em vez de so repetir o numero`() {
        val swap = sinal("swap")
        assertTrue(
            "o detalhe tem que dizer POR QUE 100% de swap importa: ${swap.detail}",
            swap.detail.contains("no room left to page"),
        )
    }

    // ── steal ───────────────────────────────────────────────────────────────

    @Test
    fun `steal e sempre OK — informacao, nunca alerta`() {
        // Not 7%, not 20%, not 37% (the real number the owner photographed).
        // The number stays VISIBLE; what it no longer does is shout.
        assertEquals(Severity.OK, sinal("steal").severity)
        assertEquals(Severity.OK, sinal("steal", producaoReal(steal = 20.0)).severity)
        assertEquals(Severity.OK, sinal("steal", producaoReal(steal = 37.0)).severity)
    }

    /**
     * Ceasing to alert is NOT ceasing to inform. The number is the answer to
     * "why is this machine slow if usage is not high?" — the question that
     * sends you looking in the wrong place when the number is hidden.
     */
    @Test
    fun `steal alto continua com numero e com a frase que explica`() {
        val s = sinal("steal", producaoReal(steal = 37.0))

        assertEquals("37%", s.headline)
        assertTrue(s.detail.contains("cannot be fixed from inside the VM"))
    }

    @Test
    fun `steal baixo diz que o hipervisor esta entregando a CPU contratada`() {
        val s = sinal("steal", producaoReal(steal = 4.9))

        assertEquals(Severity.OK, s.severity)
        assertTrue(s.detail.contains("delivering the CPU you pay for"))
    }

    @Test
    fun `o detalhe do steal diz que nao ha conserto de dentro da VM`() {
        assertTrue(sinal("steal").detail.contains("cannot be fixed from inside the VM"))
    }

    // ── CPU / load ──────────────────────────────────────────────────────────

    @Test
    fun `CPU e julgada por load por nucleo, nao pelo uso instantaneo`() {
        // 91% usage with load 0.4 on 8 cores: a compile spike, not a queue.
        // High usage on its own must NOT light the signal.
        val pico = producaoReal(load1 = 3.2)
        assertEquals(Severity.OK, sinal("cpu", pico).severity)
        assertEquals("91%", sinal("cpu", pico).headline)
    }

    @Test
    fun `load igual ao numero de nucleos ja e atencao — dai pra cima ha fila`() {
        assertEquals(Severity.ATENCAO, sinal("cpu", producaoReal(load1 = 8.0)).severity)
        assertEquals(Severity.OK, sinal("cpu", producaoReal(load1 = 7.9)).severity)
    }

    @Test
    fun `load ao dobro dos nucleos e CRITICO`() {
        assertEquals(Severity.CRITICO, sinal("cpu", producaoReal(load1 = 16.0)).severity)
    }

    @Test
    fun `sem nucleos declarados o painel nao julga em vez de dividir por zero`() {
        val semNucleos = producaoReal().let { real ->
            real.copy(cpu = real.cpu.copy(cores = 0))
        }
        assertEquals(Severity.OK, sinal("cpu", semNucleos).severity)
    }

    // ── iowait, memory, disk ────────────────────────────────────────────────

    @Test
    fun `iowait zerado nao levanta sinal e iowait alto levanta`() {
        assertEquals(Severity.OK, sinal("iowait").severity)
        assertEquals(Severity.ATENCAO, sinal("iowait", producaoReal(iowait = 12.0)).severity)
        assertEquals(Severity.CRITICO, sinal("iowait", producaoReal(iowait = 30.0)).severity)
    }

    @Test
    fun `memoria a 63 por cento e ok, a 90 e atencao, a 96 e critica`() {
        assertEquals(Severity.OK, sinal("memoria").severity)
        assertEquals(Severity.ATENCAO, sinal("memoria", producaoReal(memUsedPercent = 90.0)).severity)
        assertEquals(Severity.CRITICO, sinal("memoria", producaoReal(memUsedPercent = 96.0)).severity)
    }

    @Test
    fun `a raiz a 73 por cento e ok e a 92 sobe para atencao`() {
        assertEquals(Severity.OK, sinal("disco:/").severity)
        assertEquals(Severity.ATENCAO, sinal("disco:/", producaoReal(rootUsedPercent = 92.0)).severity)
        assertEquals(Severity.CRITICO, sinal("disco:/", producaoReal(rootUsedPercent = 96.0)).severity)
    }

    @Test
    fun `cada ponto de montagem vira um sinal proprio`() {
        val ids = gradeResources(producaoReal()).map { it.id }
        assertTrue(ids.containsAll(listOf("disco:/", "disco:/boot", "disco:/boot/efi")))
    }

    // ── rede ────────────────────────────────────────────────────────────────

    @Test
    fun `rede nunca e julgada — nao existe limiar honesto para muito trafego`() {
        val rede = sinal("rede")
        assertEquals(Severity.OK, rede.severity)
        assertTrue("veio: ${rede.detail}", rede.detail.contains("147.5 KiB/s"))
        assertTrue("veio: ${rede.detail}", rede.detail.contains("256.3 KiB/s"))
    }

    @Test
    fun `rede nao reserva a coluna do valor — o par de taxas nao cabe nela`() {
        // Two rates with units squeezed the label onto two lines and the detail
        // onto four at phone width. An empty headline makes the column vanish.
        assertEquals("", sinal("rede").headline)
        assertTrue(gradeResources(producaoReal()).filter { it.id != "rede" }.all { it.headline.isNotBlank() })
    }

    // ── sorting and rollup ──────────────────────────────────────────────────

    @Test
    fun `o pior sobe primeiro na lista de atencao`() {
        // Memory at 97% makes SWAP critical (no headroom to page AND no RAM to
        // allocate). This test used to use steal at 25%, which stopped being an
        // alert in 0.1.30 — the ordering rule is still the same, it is the
        // example that needed a signal that still alerts.
        val system = producaoReal(memUsedPercent = 97.0)
        val atencao = attentionSignals(gradeResources(system))

        // What this test pins is the RULE (worst first), not which signal wins
        // the tie: with RAM at 97% both memory AND swap go critical, and among
        // equals the stable order of `gradeResources` decides. Pinning an id
        // here would turn a change of reading order into a red test, which is
        // noise.
        assertEquals(Severity.CRITICO, atencao.first().severity)
        assertTrue(
            "os dois criticos tem que estar no topo: ${atencao.map { it.id }}",
            atencao.take(2).map { it.id }.containsAll(listOf("memoria", "swap")),
        )
    }

    @Test
    fun `a ordem dos recursos e estavel, independente da gravidade`() {
        val calmo = gradeResources(producaoReal(swapUsedPercent = 1.0, steal = 0.0, load1 = 0.5))
        val caotico = gradeResources(producaoReal(steal = 40.0))
        assertEquals(calmo.map { it.id }, caotico.map { it.id })
    }

    // ── alertas do servidor ─────────────────────────────────────────────────

    @Test
    fun `worstOf devolve o pior dos dois — a operacao de rollup de grupo`() {
        assertEquals(Severity.CRITICO, worstOf(Severity.ATENCAO, Severity.CRITICO))
        assertEquals(Severity.ATENCAO, worstOf(Severity.ATENCAO, Severity.OK))
        assertEquals(Severity.OK, worstOf(Severity.OK, Severity.OK))
    }

    @Test
    fun `um alerta do servidor nunca e rebaixado — critical continua CRITICO`() {
        val alerta = OpsAlert(
            name = "disk_root",
            severity = "critical",
            state = "firing",
            currentValue = 92.0,
            threshold = 85.0,
            unit = "%",
        ).toSignal()

        assertEquals(Severity.CRITICO, alerta.severity)
        assertEquals("92 %", alerta.headline)
        assertEquals(DashboardTarget.ALERTAS, alerta.target)
    }

    @Test
    fun `severidade desconhecida do servidor vira ATENCAO, nunca silencio`() {
        val alerta = OpsAlert("queue_lag", "warn", "firing", 310.0, 120.0, "s").toSignal()
        assertEquals(Severity.ATENCAO, alerta.severity)
        assertTrue(alerta.detail.contains("threshold 120 s"))
    }

    @Test
    fun `alerta sem unidade nao imprime espaco solto`() {
        val alerta = OpsAlert("regra", "warn", "firing", 3.5, 2.0, null).toSignal()
        assertEquals("3.5", alerta.headline)
    }

    @Test
    fun `todo sinal aponta para uma tela — nenhum e beco sem saida`() {
        gradeResources(producaoReal()).forEach { signal ->
            assertNotNull("sinal ${signal.id} sem destino", signal.target)
        }
    }

    @Test
    fun `sem o bloco system o painel nao inventa recursos`() {
        val snapshot = snapshotReal(ops = opsReal(system = null))
        assertTrue(snapshot.resourceSignals.isEmpty())
        assertNull(snapshot.ops.system)
    }
}
