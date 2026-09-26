package com.vpsmanager.feature.auth.painel

import com.vpsmanager.data.dashboard.DashboardTarget
import com.vpsmanager.data.dashboard.Severity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * What these tests protect: the grid is the first thing a person looks at
 * when they open the app in a hurry. Two of its guarantees cannot be just a
 * sentence in the KDoc — that it **does not hide a fire**, and that it does
 * not scramble the alignment of the columns.
 */
class BlocosDoPainelTest {

    private fun bloco(
        id: String,
        severidade: Severity = Severity.OK,
        largo: Boolean = false,
    ) = BlocoDoPainel(
        id = id,
        rotulo = id,
        valor = "1",
        sub = "",
        severidade = severidade,
        alvo = DashboardTarget.METRICAS,
        largo = largo,
    )

    // ── the rule that matters most ──────────────────────────────────────────

    /**
     * The real case: the person assembles the dashboard on a good day with CPU
     * and memory, the disk fills up, and the disk was not on the list. A
     * dashboard that stays green in that scenario is worse than none at all,
     * because it is consulted with confidence.
     */
    @Test
    fun `bloco CRITICO entra na grade mesmo sem ter sido escolhido`() {
        val catalogo = listOf(
            bloco("cpu"),
            bloco("memoria"),
            bloco("disco", severidade = Severity.CRITICO),
        )

        val visiveis = blocosVisiveis(catalogo, escolhidos = listOf("cpu", "memoria"))

        assertTrue("o disco crítico tem de aparecer", visiveis.any { it.id == "disco" })
    }

    /** And it goes in FIRST: a fire at the foot of a scrollable dashboard is unseen. */
    @Test
    fun `o critico vai para a frente da grade`() {
        val catalogo = listOf(
            bloco("cpu"),
            bloco("memoria"),
            bloco("disco", severidade = Severity.CRITICO),
        )

        val visiveis = blocosVisiveis(catalogo, escolhidos = listOf("cpu", "memoria"))

        assertEquals("disco", visiveis.first().id)
    }

    /** A critical one that was ALSO chosen must not show up twice. */
    @Test
    fun `critico escolhido aparece uma vez so`() {
        val catalogo = listOf(bloco("cpu", severidade = Severity.CRITICO), bloco("memoria"))

        val visiveis = blocosVisiveis(catalogo, escolhidos = listOf("cpu", "memoria"))

        assertEquals(listOf("cpu", "memoria"), visiveis.map { it.id })
    }

    /**
     * ATTENTION does not force its way in. The distinction is the same as that
     * of the two threshold bands: "look today" fits within the person's
     * choice, "look now" does not. Without this, a machine with a long uptime
     * (swap in attention the whole time) would clog the dashboard with blocks
     * nobody asked for.
     */
    @Test
    fun `ATENCAO nao força entrada na grade`() {
        val catalogo = listOf(bloco("cpu"), bloco("swap", severidade = Severity.ATENCAO))

        val visiveis = blocosVisiveis(catalogo, escolhidos = listOf("cpu"))

        assertEquals(listOf("cpu"), visiveis.map { it.id })
    }

    /** A stored id that vanished from the catalogue leaves no tombstone on screen. */
    @Test
    fun `id que nao existe mais no catalogo simplesmente some`() {
        val catalogo = listOf(bloco("cpu"))

        val visiveis = blocosVisiveis(catalogo, escolhidos = listOf("cpu", "disco:/antigo"))

        assertEquals(listOf("cpu"), visiveis.map { it.id })
    }

    /** The order of the choice IS the choice — it is what puts what matters on top. */
    @Test
    fun `a ordem escolhida e preservada`() {
        val catalogo = listOf(bloco("a"), bloco("b"), bloco("c"))

        val visiveis = blocosVisiveis(catalogo, escolhidos = listOf("c", "a", "b"))

        assertEquals(listOf("c", "a", "b"), visiveis.map { it.id })
    }

    // ── the alignment of the columns ────────────────────────────────────────

    @Test
    fun `dois estreitos por linha`() {
        val linhas = linhasDaGrade(listOf(bloco("a"), bloco("b"), bloco("c")))

        assertEquals(2, linhas[0].size)
        assertEquals(1, linhas[1].size)
    }

    /**
     * A wide block CLOSES the row in progress before going in. Without that, a
     * wide one after a narrow one would produce a row of three weights and the
     * grid would lose its vertical alignment — which is precisely what lets
     * you compare two numbers at a glance.
     */
    @Test
    fun `o largo nunca divide linha com um estreito`() {
        val linhas = linhasDaGrade(listOf(bloco("a"), bloco("largo", largo = true), bloco("b")))

        assertEquals(listOf("a"), linhas[0].map { it.id })
        assertEquals(listOf("largo"), linhas[1].map { it.id })
        assertEquals(listOf("b"), linhas[2].map { it.id })
    }

    @Test
    fun `grade vazia nao produz linha vazia`() {
        assertTrue(linhasDaGrade(emptyList()).isEmpty())
    }
}
