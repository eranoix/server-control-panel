package com.vpsmanager.feature.terminal.render

import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.abs
import kotlin.math.pow

/**
 * The legibility guard for the terminal on the light theme.
 *
 * The concrete risk these tests cover: the ANSI palette the VT emulator
 * returns was designed for a BLACK background. An `ls --color` returns pure
 * white and pure yellow; a `git status` returns light green and red. Painting
 * that on a light background does not give "poor contrast", it gives INVISIBLE
 * text — the command output vanishes from the screen.
 *
 * The ruler here is the real WCAG contrast ratio (with sRGB linearisation),
 * not the cheap approximation the renderer uses per frame: the renderer only
 * needs to answer "far enough?" fast; the test is what verifies that the
 * chosen threshold corresponds to something actually legible.
 */
class TerminalPaletteTest {

    // ---- ruler: WCAG 2.x contrast ----

    private fun canalLinear(v: Int): Double {
        val s = v / 255.0
        return if (s <= 0.03928) s / 12.92 else ((s + 0.055) / 1.055).pow(2.4)
    }

    private fun luminanciaRelativa(rgb: Int): Double =
        0.2126 * canalLinear((rgb shr 16) and 0xff) +
            0.7152 * canalLinear((rgb shr 8) and 0xff) +
            0.0722 * canalLinear(rgb and 0xff)

    private fun contraste(a: Int, b: Int): Double {
        val la = luminanciaRelativa(a)
        val lb = luminanciaRelativa(b)
        val claro = maxOf(la, lb)
        val escuro = minOf(la, lb)
        return (claro + 0.05) / (escuro + 0.05)
    }

    /** The ANSI palette colours that vanish on a light background. */
    private val coresQueSomemNoClaro = mapOf(
        "branco brilhante" to 0xFFFFFF,
        "amarelo brilhante" to 0xFFFF00,
        "ciano brilhante" to 0x00FFFF,
        "verde brilhante" to 0x00FF00,
        "branco (ls: arquivo comum)" to 0xE0E0E0,
        "amarelo (ls: dispositivo)" to 0xCDCD00,
        "ciano (ls: link simbolico)" to 0x00CDCD,
    )

    /** A paleta ANSI de 16 cores inteira, como o emulador a resolve. */
    private val paletaAnsi16 = listOf(
        0x000000, 0xCD0000, 0x00CD00, 0xCDCD00, 0x0000EE, 0xCD00CD, 0x00CDCD, 0xE5E5E5,
        0x7F7F7F, 0xFF0000, 0x00FF00, 0xFFFF00, 0x5C5CFF, 0xFF00FF, 0x00FFFF, 0xFFFFFF,
    )

    @Test
    fun `no tema claro a paleta ANSI INTEIRA fica legivel`() {
        val bg = PaletaTerminalClara.defaultBg
        paletaAnsi16.forEach { cor ->
            val ajustada = ajustaParaContraste(cor, bg, PaletaTerminalClara.minLumaDelta)
            val razao = contraste(ajustada, bg)
            assertTrue(
                "0x${"%06X".format(cor)} sobre o fundo claro ficou em ${"%.2f".format(razao)}:1 " +
                    "(ajustada para 0x${"%06X".format(ajustada)})",
                razao >= 4.5,
            )
        }
    }

    @Test
    fun `no tema claro as cores que somem ficam legiveis`() {
        val bg = PaletaTerminalClara.defaultBg
        coresQueSomemNoClaro.forEach { (nome, cor) ->
            val ajustada = ajustaParaContraste(cor, bg, PaletaTerminalClara.minLumaDelta)
            val razao = contraste(ajustada, bg)
            assertTrue(
                "$nome sobre o fundo claro ficou em ${"%.2f".format(razao)}:1 " +
                    "(cor ajustada 0x${"%06X".format(ajustada)})",
                razao >= 4.5,
            )
        }
    }

    @Test
    fun `sem a guarda essas mesmas cores seriam ilegiveis - o defeito existe`() {
        // If this test ever fails, the guard has become unnecessary; while it
        // passes, it is the proof that the problem is real and not theoretical.
        val bg = PaletaTerminalClara.defaultBg
        coresQueSomemNoClaro.forEach { (nome, cor) ->
            assertTrue(
                "$nome não precisaria de guarda — revisar",
                contraste(cor, bg) < 4.5,
            )
        }
    }

    @Test
    fun `a guarda preserva o matiz - amarelo continua amarelo`() {
        val amarelo = ajustaParaContraste(0xFFFF00, PaletaTerminalClara.defaultBg, PaletaTerminalClara.minLumaDelta)
        val r = (amarelo shr 16) and 0xff
        val g = (amarelo shr 8) and 0xff
        val b = amarelo and 0xff
        // High red and green, zero blue: still yellow, just dark.
        assertEquals("o canal azul do amarelo tem que continuar em zero", 0, b)
        assertTrue("amarelo virou cinza", r > b && g > b)
        assertEquals("amarelo perdeu a simetria R=G", r, g)

        val ciano = ajustaParaContraste(0x00FFFF, PaletaTerminalClara.defaultBg, PaletaTerminalClara.minLumaDelta)
        assertEquals(0, (ciano shr 16) and 0xff)
        assertTrue("ciano deixou de ser ciano", (ciano and 0xff) > 0)
    }

    @Test
    fun `cores que ja contrastam passam intocadas`() {
        val bg = PaletaTerminalClara.defaultBg
        listOf(0x000000, 0x1A1A1A, 0x0000AA, 0x800000).forEach { cor ->
            assertEquals(
                "0x${"%06X".format(cor)} já contrasta e não deveria ser mexida",
                cor,
                ajustaParaContraste(cor, bg, PaletaTerminalClara.minLumaDelta),
            )
        }
    }

    @Test
    fun `no tema escuro nada muda - a renderizacao de hoje fica intacta`() {
        val bg = PaletaTerminalEscura.defaultBg
        val delta = PaletaTerminalEscura.minLumaDelta
        // The whole ANSI palette, including the dark colours that on a black
        // background would be candidates for adjustment: with the guard off,
        // nothing is touched at all. That is what guarantees the dark theme
        // comes out of this change identical.
        paletaAnsi16.forEach { cor ->
            assertEquals(cor, ajustaParaContraste(cor, bg, delta))
        }
    }

    @Test
    fun `a guarda vale tambem contra fundo de celula colorido`() {
        // White text on a white background asked for by the PROGRAM (not the
        // default background) is the same defect, and vanishes the same way.
        val ajustada = ajustaParaContraste(0xFFFFFF, 0xF0F0F0, PaletaTerminalClara.minLumaDelta)
        assertTrue(contraste(ajustada, 0xF0F0F0) >= 4.5)
    }

    @Test
    fun `fundo escuro empurra o texto para o claro, nao para o escuro`() {
        // The guard has to know which way to go. Dark blue on a black
        // background, with the guard ON, is lightened — never darkened further.
        val ajustada = ajustaParaContraste(0x000080, 0x000000, minLumaDelta = 150)
        assertTrue("deveria clarear", luma(ajustada) > luma(0x000080))
    }

    // ---- integration with the line op builder ----

    private fun celula(
        codepoint: Int = 'A'.code,
        fg: Int? = null,
        bg: Int? = null,
        faint: Boolean = false,
        inverse: Boolean = false,
    ) = CellSnapshot.Cell(
        codepoint = codepoint,
        fg = fg,
        bg = bg,
        bold = false,
        italic = false,
        faint = faint,
        blink = false,
        inverse = inverse,
        invisible = false,
        strikethrough = false,
        overline = false,
        underline = 0,
        wide = CellSnapshot.Wide.NARROW,
    )

    @Test
    fun `buildRowDrawOps aplica a guarda no tema claro`() {
        val ops = buildRowDrawOps(
            cells = listOf(celula(fg = 0xFFFFFF)),
            defaultFg = PaletaTerminalClara.defaultFg,
            defaultBg = PaletaTerminalClara.defaultBg,
            minLumaDelta = PaletaTerminalClara.minLumaDelta,
        )
        assertNotEquals("branco sobre fundo claro tinha que ter sido ajustado", 0xFFFFFF, ops.single().fg)
        assertTrue(contraste(ops.single().fg, PaletaTerminalClara.defaultBg) >= 4.5)
    }

    @Test
    fun `buildRowDrawOps sem guarda e byte a byte o de antes`() {
        // The parameter defaults to 0, so every caller that did not ask for
        // the guard — the dark path in production included — stays identical.
        val cells = listOf(celula(fg = 0xFFFFFF), celula(fg = 0x00FF00, bg = 0x000080), celula())
        val semParametro = buildRowDrawOps(cells, defaultFg = 0xE0E0E0, defaultBg = 0x000000)
        val comZero = buildRowDrawOps(cells, defaultFg = 0xE0E0E0, defaultBg = 0x000000, minLumaDelta = 0)
        assertEquals(semParametro, comZero)
        assertEquals(0xFFFFFF, semParametro[0].fg)
    }

    @Test
    fun `esmaecido continua mais fraco que o normal, mesmo com a guarda`() {
        // The guard runs BEFORE the faint pass on purpose: were it to run
        // after, it would undo the fading and `faint` would stop meaning
        // anything at all. This test is what locks that order.
        val cells = listOf(celula(fg = 0xFFFFFF), celula(fg = 0xFFFFFF, faint = true))
        val ops = buildRowDrawOps(
            cells,
            defaultFg = PaletaTerminalClara.defaultFg,
            defaultBg = PaletaTerminalClara.defaultBg,
            minLumaDelta = PaletaTerminalClara.minLumaDelta,
        )
        val normal = ops[0].fg
        val esmaecido = ops[1].fg
        assertNotEquals("esmaecido igual ao normal — o atributo virou enfeite", normal, esmaecido)
        val fundo = luma(PaletaTerminalClara.defaultBg)
        assertTrue(
            "esmaecido tem que ficar MAIS perto do fundo que o normal",
            abs(luma(esmaecido) - fundo) < abs(luma(normal) - fundo),
        )
        // And still visible: a faint that disappears is not faint.
        assertTrue("esmaecido sumiu no fundo", abs(luma(esmaecido) - fundo) > 40)
    }

    @Test
    fun `video invertido no tema claro nao vira texto claro sobre fundo claro`() {
        // `inverse` swaps fg and bg. With no explicit colours that gives
        // background text on foreground background — and that is precisely
        // where a half-swapped palette would produce light-on-light.
        val ops = buildRowDrawOps(
            cells = listOf(celula(inverse = true)),
            defaultFg = PaletaTerminalClara.defaultFg,
            defaultBg = PaletaTerminalClara.defaultBg,
            minLumaDelta = PaletaTerminalClara.minLumaDelta,
        )
        val op = ops.single()
        assertTrue(
            "invertido ficou ilegível: fg 0x${"%06X".format(op.fg)} sobre bg 0x${"%06X".format(op.bg)}",
            contraste(op.fg, op.bg) >= 4.5,
        )
    }

    @Test
    fun `as duas paletas tem cursor visivel sobre o proprio fundo`() {
        // The cursor used to be a fixed translucent white. On a light
        // background, invisible.
        assertTrue(
            "cursor do tema claro precisa ser escuro",
            PaletaTerminalClara.cursor.red < 0.5f && PaletaTerminalClara.cursor.green < 0.5f,
        )
        assertTrue(
            "cursor do tema escuro precisa ser claro",
            PaletaTerminalEscura.cursor.red > 0.5f && PaletaTerminalEscura.cursor.green > 0.5f,
        )
    }

    @Test
    fun `o texto padrao de cada paleta e legivel no fundo dela`() {
        assertTrue(contraste(PaletaTerminalClara.defaultFg, PaletaTerminalClara.defaultBg) >= 7.0)
        assertTrue(contraste(PaletaTerminalEscura.defaultFg, PaletaTerminalEscura.defaultBg) >= 7.0)
    }
}
