package com.vpsmanager.feature.terminal.geometria

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The anchor is a small piece of arithmetic that decides where the person sees
 * the compose box. Get it wrong one way and the end of the frame is hidden
 * (the old report: "the keyboard is cutting off the writing window"); get it
 * wrong the other way and the box floats in the top third (the new report:
 * "the writing window must always stay at the bottom"). Both fit the same
 * rule, and it is that rule these tests pin down.
 */
class AncoraDoQuadroTest {

    private val celula = 30

    @Test
    fun `quadro curto DESCE ate encostar no fundo`() {
        // 20 frame lines in an area of 52 lines: the blank space has to go
        // UP, and the compose box down to the bottom.
        val deslocamento = AncoraDoQuadro.deslocamentoY(
            fundoDoConteudoPx = 20 * celula,
            alturaVisivelPx = 52 * celula,
            maximoParaSubirPx = 0,
        )
        assertEquals("desceu o que faltava para encostar", 32 * celula, deslocamento)
        assertTrue("positivo e para baixo", deslocamento > 0)
    }

    @Test
    fun `quadro do tamanho exato da area nao se mexe`() {
        assertEquals(
            0,
            AncoraDoQuadro.deslocamentoY(
                fundoDoConteudoPx = 52 * celula,
                alturaVisivelPx = 52 * celula,
                maximoParaSubirPx = 0,
            ),
        )
    }

    @Test
    fun `com o teclado no ar o quadro SOBE, ate o limite do que foi tapado`() {
        // The grid still has 52 lines, but only 30 are showing. The end of
        // the frame is below the visible area: move up.
        val tapado = 22 * celula
        val deslocamento = AncoraDoQuadro.deslocamentoY(
            fundoDoConteudoPx = 52 * celula,
            alturaVisivelPx = 30 * celula,
            maximoParaSubirPx = tapado,
        )
        assertEquals(-22 * celula, deslocamento)
    }

    @Test
    fun `nunca sobe mais do que existe de grade fora da area`() {
        // Going up beyond that would drag the grid out with nothing to put
        // in its place — that is how an earlier attempt WIPED the operator's
        // screen.
        val deslocamento = AncoraDoQuadro.deslocamentoY(
            fundoDoConteudoPx = 200 * celula,
            alturaVisivelPx = 30 * celula,
            maximoParaSubirPx = 5 * celula,
        )
        assertEquals(-5 * celula, deslocamento)
    }

    @Test
    fun `a ultima linha util respeita a folga abaixo do cursor`() {
        // Cursor on line 10, content ending on line 9: what needs to show is
        // line 12, otherwise the bottom edge of the input box disappears.
        assertEquals(
            12,
            AncoraDoQuadro.ultimaLinhaUtil(
                ultimaLinhaComConteudo = 9,
                linhaDoCursor = 10,
                folgaAbaixoDoCursor = 2,
                linhas = 52,
            ),
        )
    }

    @Test
    fun `conteudo abaixo do cursor ganha do cursor`() {
        // Footer drawn below the box: it is the footer that defines the end.
        assertEquals(
            30,
            AncoraDoQuadro.ultimaLinhaUtil(
                ultimaLinhaComConteudo = 30,
                linhaDoCursor = 20,
                folgaAbaixoDoCursor = 2,
                linhas = 52,
            ),
        )
    }

    @Test
    fun `a folga nunca aponta para fora da grade`() {
        assertEquals(
            51,
            AncoraDoQuadro.ultimaLinhaUtil(
                ultimaLinhaComConteudo = 51,
                linhaDoCursor = 51,
                folgaAbaixoDoCursor = 2,
                linhas = 52,
            ),
        )
    }

    @Test
    fun `grade vazia nao desloca nada - a faixa de duas linhas no rodape`() {
        // Without this rule, a grid without a single character fell back on
        // the cursor (line 0 in an empty grid) and the frame dropped fifty
        // lines, becoming a two-line strip glued to the footer with the app
        // surface showing above it. That was the screen in the photograph:
        // "when the session opens everything is black".
        val ultima = AncoraDoQuadro.ultimaLinhaUtil(
            ultimaLinhaComConteudo = -1,
            linhaDoCursor = 0,
            folgaAbaixoDoCursor = 2,
            linhas = 52,
        )
        assertEquals("grade vazia ancora no fim dela mesma", 51, ultima)
        assertEquals(
            "e portanto nao se desloca",
            0,
            AncoraDoQuadro.deslocamentoY(
                fundoDoConteudoPx = (ultima + 1) * celula,
                alturaVisivelPx = 52 * celula,
                maximoParaSubirPx = 0,
            ),
        )
    }

    @Test
    fun `acha a ultima linha com conteudo varrendo de baixo`() {
        // 10x5 grid with something only on line 2.
        val achou = AncoraDoQuadro.ultimaLinhaComConteudo(colunas = 10, linhas = 5) { x, y ->
            !(y == 2 && x == 3)
        }
        assertEquals(2, achou)
    }

    @Test
    fun `grade inteiramente vazia devolve menos um, nao zero`() {
        // -1 means "there is nothing to anchor to". Returning 0 would make an
        // empty grid behave as if it had a line of content at the top, and the
        // frame would drop by the whole screen.
        assertEquals(-1, AncoraDoQuadro.ultimaLinhaComConteudo(colunas = 10, linhas = 5) { _, _ -> true })
    }

    @Test
    fun `espaco conta como vazio`() {
        // A footer ending in thirty columns of blank space cannot pretend the
        // line runs to the end of the grid.
        val achou = AncoraDoQuadro.ultimaLinhaComConteudo(colunas = 10, linhas = 3) { x, y ->
            // Line 0 has text; lines 1 and 2 are only spaces.
            y != 0 || x > 4
        }
        assertEquals(0, achou)
    }
}
