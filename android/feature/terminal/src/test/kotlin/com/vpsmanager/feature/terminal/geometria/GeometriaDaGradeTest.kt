package com.vpsmanager.feature.terminal.geometria

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The contract of this calculation is the antidote to a defect that came back
 * four times: the terminal history duplicated itself every time the keyboard
 * opened. That is why the tests below are written as QUESTIONS ABOUT THE
 * SERVER — "how many distinct grid sizes would it receive?" — and not as
 * checks on return values: it is the number of `SIGWINCH` that causes the
 * copy, not the number itself.
 *
 * The previous version of this class kept the tallest height ever seen and
 * passed the duplication tests — and still wiped the operator's screen in
 * 0.1.17, because a maximum also keeps the spurious measurements taken during
 * navigation animations. The duplication tests are still here; what changed is
 * that they now describe a MEASUREMENT, not a memory, and so there is no state
 * left that can be wrong.
 */
class GeometriaDaGradeTest {

    private val alturaComTecladoFechadoPx = 1840
    private val barraDeNavegacaoPx = 63
    private val celulaH = 40
    private val celulaW = 16

    /**
     * The height the layout offers when the IME has an inset of [imePx] — it is
     * what the `imePadding()` in `AppNavHost` lets through.
     */
    private fun alturaOferecida(imePx: Int) =
        alturaComTecladoFechadoPx - (imePx - barraDeNavegacaoPx).coerceAtLeast(0)

    /**
     * The case that produced the duplication: ten intermediate insets on the
     * way open and ten on the way closed. The server must see NONE of them.
     */
    @Test
    fun `a animacao inteira do teclado nao muda o numero de linhas`() {
        val linhasVistas = mutableSetOf<Int>()

        // From keyboard closed (0) to open (740), frame by frame, and back.
        val insets = listOf(0, 120, 260, 400, 510, 600, 660, 700, 720, 740)
        (insets + insets.reversed()).forEach { ime ->
            val cheia = GeometriaDaGrade.alturaSemTeclado(
                alturaDisponivelPx = alturaOferecida(ime),
                imePx = ime,
                barraDeNavegacaoPx = barraDeNavegacaoPx,
            )
            linhasVistas += GeometriaDaGrade.linhas(cheia, celulaH)
        }

        assertEquals(
            "cada linha a mais neste conjunto é um SIGWINCH, e cada SIGWINCH é uma cópia do histórico",
            setOf(alturaComTecladoFechadoPx / celulaH),
            linhasVistas,
        )
    }

    /**
     * With the keyboard closed the calculation has to be the IDENTITY. A
     * correction that stays on when it is not needed is one that fails
     * silently.
     */
    @Test
    fun `sem teclado nada e somado e nada e tapado`() {
        assertEquals(
            alturaComTecladoFechadoPx,
            GeometriaDaGrade.alturaSemTeclado(alturaComTecladoFechadoPx, 0, barraDeNavegacaoPx),
        )
        assertEquals(0, GeometriaDaGrade.tapadoPeloTeclado(0, barraDeNavegacaoPx))
    }

    /**
     * The IME inset includes the navigation bar area, and `imePadding()`
     * discounts what has already been consumed above. Adding the RAW inset
     * would count the bar twice and give the grid rows that do not exist —
     * which is exactly how an oversized grid disappears off the top of the
     * screen.
     */
    @Test
    fun `a barra de navegacao nao e contada duas vezes`() {
        val ime = 740
        val cheia = GeometriaDaGrade.alturaSemTeclado(alturaOferecida(ime), ime, barraDeNavegacaoPx)

        assertEquals(alturaComTecladoFechadoPx, cheia)
        assertEquals(ime - barraDeNavegacaoPx, GeometriaDaGrade.tapadoPeloTeclado(ime, barraDeNavegacaoPx))
    }

    /**
     * A keyboard smaller than the navigation bar (or a device with no bar) must
     * not produce a negative offset — that would push the grid DOWN, hiding the
     * cursor behind the keyboard instead of above it.
     */
    @Test
    fun `inset menor que a barra nao vira deslocamento negativo`() {
        assertEquals(0, GeometriaDaGrade.tapadoPeloTeclado(imePx = 20, barraDeNavegacaoPx = 63))
        assertEquals(
            alturaComTecladoFechadoPx,
            GeometriaDaGrade.alturaSemTeclado(alturaComTecladoFechadoPx, 20, 63),
        )
    }

    /**
     * The transient furniture of the screen — connection banner, autocorrect
     * strip, attachment bar — changes the height FOR REAL, and the grid has to
     * follow it. The previous rule, which kept the maximum, treated that as an
     * obstruction and left ghost rows behind.
     */
    @Test
    fun `mudanca real de altura muda o numero de linhas`() {
        val comFaixa = GeometriaDaGrade.alturaSemTeclado(alturaComTecladoFechadoPx - 120, 0, barraDeNavegacaoPx)
        val semFaixa = GeometriaDaGrade.alturaSemTeclado(alturaComTecladoFechadoPx, 0, barraDeNavegacaoPx)

        assertEquals((alturaComTecladoFechadoPx - 120) / celulaH, GeometriaDaGrade.linhas(comFaixa, celulaH))
        assertEquals(alturaComTecladoFechadoPx / celulaH, GeometriaDaGrade.linhas(semFaixa, celulaH))
    }

    /**
     * No memory: a spurious measurement from a navigation animation affects its
     * own frame and nothing else. It was the absence of this that wiped the
     * screen in 0.1.17.
     */
    @Test
    fun `uma medida espuria nao contamina as seguintes`() {
        GeometriaDaGrade.alturaSemTeclado(9_000, 0, barraDeNavegacaoPx)
        val depois = GeometriaDaGrade.alturaSemTeclado(alturaComTecladoFechadoPx, 0, barraDeNavegacaoPx)

        assertEquals(alturaComTecladoFechadoPx, depois)
    }

    /** Changing the font size CHANGES the grid on purpose. */
    @Test
    fun `mudar o tamanho da celula muda o numero de linhas`() {
        assertEquals(alturaComTecladoFechadoPx / 20, GeometriaDaGrade.linhas(alturaComTecladoFechadoPx, 20))
        assertEquals(alturaComTecladoFechadoPx / 60, GeometriaDaGrade.linhas(alturaComTecladoFechadoPx, 60))
    }

    /** A degenerate window must not produce a grid of zero rows. */
    @Test
    fun `uma janela menor que uma celula ainda rende uma linha e uma coluna`() {
        assertEquals(1, GeometriaDaGrade.colunas(4, celulaW))
        assertEquals(1, GeometriaDaGrade.linhas(4, celulaH))
    }
}
