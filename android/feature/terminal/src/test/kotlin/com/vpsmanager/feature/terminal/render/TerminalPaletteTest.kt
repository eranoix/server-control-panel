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

    private fun linearChannel(v: Int): Double {
        val s = v / 255.0
        return if (s <= 0.03928) s / 12.92 else ((s + 0.055) / 1.055).pow(2.4)
    }

    private fun relativeLuminance(rgb: Int): Double =
        0.2126 * linearChannel((rgb shr 16) and 0xff) +
            0.7152 * linearChannel((rgb shr 8) and 0xff) +
            0.0722 * linearChannel(rgb and 0xff)

    private fun contrast(a: Int, b: Int): Double {
        val la = relativeLuminance(a)
        val lb = relativeLuminance(b)
        val light = maxOf(la, lb)
        val dark = minOf(la, lb)
        return (light + 0.05) / (dark + 0.05)
    }

    /** The ANSI palette colours that vanish on a light background. */
    private val colorsThatVanishOnLight = mapOf(
        "branco brilhante" to 0xFFFFFF,
        "amarelo brilhante" to 0xFFFF00,
        "ciano brilhante" to 0x00FFFF,
        "verde brilhante" to 0x00FF00,
        "branco (ls: arquivo comum)" to 0xE0E0E0,
        "amarelo (ls: dispositivo)" to 0xCDCD00,
        "ciano (ls: link simbolico)" to 0x00CDCD,
    )

    /** A paleta ANSI de 16 cores inteira, como o emulador a resolve. */
    private val ansi16Palette = listOf(
        0x000000, 0xCD0000, 0x00CD00, 0xCDCD00, 0x0000EE, 0xCD00CD, 0x00CDCD, 0xE5E5E5,
        0x7F7F7F, 0xFF0000, 0x00FF00, 0xFFFF00, 0x5C5CFF, 0xFF00FF, 0x00FFFF, 0xFFFFFF,
    )

    @Test
    fun `no tema claro a paleta ANSI INTEIRA fica legivel`() {
        val bg = LightTerminalPalette.defaultBg
        ansi16Palette.forEach { color ->
            val adjusted = adjustForContrast(color, bg, LightTerminalPalette.minLumaDelta)
            val ratio = contrast(adjusted, bg)
            assertTrue(
                "0x${"%06X".format(color)} sobre o fundo claro ficou em ${"%.2f".format(ratio)}:1 " +
                    "(ajustada para 0x${"%06X".format(adjusted)})",
                ratio >= 4.5,
            )
        }
    }

    @Test
    fun `no tema claro as cores que somem ficam legiveis`() {
        val bg = LightTerminalPalette.defaultBg
        colorsThatVanishOnLight.forEach { (name, color) ->
            val adjusted = adjustForContrast(color, bg, LightTerminalPalette.minLumaDelta)
            val ratio = contrast(adjusted, bg)
            assertTrue(
                "$name sobre o fundo claro ficou em ${"%.2f".format(ratio)}:1 " +
                    "(cor ajustada 0x${"%06X".format(adjusted)})",
                ratio >= 4.5,
            )
        }
    }

    @Test
    fun `sem a guarda essas mesmas cores seriam ilegiveis - o defeito existe`() {
        // If this test ever fails, the guard has become unnecessary; while it
        // passes, it is the proof that the problem is real and not theoretical.
        val bg = LightTerminalPalette.defaultBg
        colorsThatVanishOnLight.forEach { (name, color) ->
            assertTrue(
                "$name não precisaria de guarda — revisar",
                contrast(color, bg) < 4.5,
            )
        }
    }

    @Test
    fun `a guarda preserva o matiz - amarelo continua amarelo`() {
        val yellow = adjustForContrast(0xFFFF00, LightTerminalPalette.defaultBg, LightTerminalPalette.minLumaDelta)
        val r = (yellow shr 16) and 0xff
        val g = (yellow shr 8) and 0xff
        val b = yellow and 0xff
        // High red and green, zero blue: still yellow, just dark.
        assertEquals("o canal azul do amarelo tem que continuar em zero", 0, b)
        assertTrue("amarelo virou cinza", r > b && g > b)
        assertEquals("amarelo perdeu a simetria R=G", r, g)

        val ciano = adjustForContrast(0x00FFFF, LightTerminalPalette.defaultBg, LightTerminalPalette.minLumaDelta)
        assertEquals(0, (ciano shr 16) and 0xff)
        assertTrue("ciano deixou de ser ciano", (ciano and 0xff) > 0)
    }

    @Test
    fun `cores que ja contrastam passam intocadas`() {
        val bg = LightTerminalPalette.defaultBg
        listOf(0x000000, 0x1A1A1A, 0x0000AA, 0x800000).forEach { color ->
            assertEquals(
                "0x${"%06X".format(color)} já contrasta e não deveria ser mexida",
                color,
                adjustForContrast(color, bg, LightTerminalPalette.minLumaDelta),
            )
        }
    }

    @Test
    fun `no tema escuro nada muda - a renderizacao de hoje fica intacta`() {
        val bg = DarkTerminalPalette.defaultBg
        val delta = DarkTerminalPalette.minLumaDelta
        // The whole ANSI palette, including the dark colours that on a black
        // background would be candidates for adjustment: with the guard off,
        // nothing is touched at all. That is what guarantees the dark theme
        // comes out of this change identical.
        ansi16Palette.forEach { color ->
            assertEquals(color, adjustForContrast(color, bg, delta))
        }
    }

    @Test
    fun `a guarda vale tambem contra fundo de celula colorido`() {
        // White text on a white background asked for by the PROGRAM (not the
        // default background) is the same defect, and vanishes the same way.
        val adjusted = adjustForContrast(0xFFFFFF, 0xF0F0F0, LightTerminalPalette.minLumaDelta)
        assertTrue(contrast(adjusted, 0xF0F0F0) >= 4.5)
    }

    @Test
    fun `fundo escuro empurra o texto para o claro, nao para o escuro`() {
        // The guard has to know which way to go. Dark blue on a black
        // background, with the guard ON, is lightened — never darkened further.
        val adjusted = adjustForContrast(0x000080, 0x000000, minLumaDelta = 150)
        assertTrue("deveria clarear", luma(adjusted) > luma(0x000080))
    }

    // ---- integration with the line op builder ----

    private fun cell(
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
            cells = listOf(cell(fg = 0xFFFFFF)),
            defaultFg = LightTerminalPalette.defaultFg,
            defaultBg = LightTerminalPalette.defaultBg,
            minLumaDelta = LightTerminalPalette.minLumaDelta,
        )
        assertNotEquals("branco sobre fundo claro tinha que ter sido ajustado", 0xFFFFFF, ops.single().fg)
        assertTrue(contrast(ops.single().fg, LightTerminalPalette.defaultBg) >= 4.5)
    }

    @Test
    fun `buildRowDrawOps sem guarda e byte a byte o de antes`() {
        // The parameter defaults to 0, so every caller that did not ask for
        // the guard — the dark path in production included — stays identical.
        val cells = listOf(cell(fg = 0xFFFFFF), cell(fg = 0x00FF00, bg = 0x000080), cell())
        val withoutParam = buildRowDrawOps(cells, defaultFg = 0xE0E0E0, defaultBg = 0x000000)
        val comZero = buildRowDrawOps(cells, defaultFg = 0xE0E0E0, defaultBg = 0x000000, minLumaDelta = 0)
        assertEquals(withoutParam, comZero)
        assertEquals(0xFFFFFF, withoutParam[0].fg)
    }

    @Test
    fun `esmaecido continua mais fraco que o normal, mesmo com a guarda`() {
        // The guard runs BEFORE the faint pass on purpose: were it to run
        // after, it would undo the fading and `faint` would stop meaning
        // anything at all. This test is what locks that order.
        val cells = listOf(cell(fg = 0xFFFFFF), cell(fg = 0xFFFFFF, faint = true))
        val ops = buildRowDrawOps(
            cells,
            defaultFg = LightTerminalPalette.defaultFg,
            defaultBg = LightTerminalPalette.defaultBg,
            minLumaDelta = LightTerminalPalette.minLumaDelta,
        )
        val normal = ops[0].fg
        val esmaecido = ops[1].fg
        assertNotEquals("esmaecido igual ao normal — o atributo virou enfeite", normal, esmaecido)
        val background = luma(LightTerminalPalette.defaultBg)
        assertTrue(
            "esmaecido tem que ficar MAIS perto do fundo que o normal",
            abs(luma(esmaecido) - background) < abs(luma(normal) - background),
        )
        // And still visible: a faint that disappears is not faint.
        assertTrue("esmaecido sumiu no fundo", abs(luma(esmaecido) - background) > 40)
    }

    @Test
    fun `video invertido no tema claro nao vira texto claro sobre fundo claro`() {
        // `inverse` swaps fg and bg. With no explicit colours that gives
        // background text on foreground background — and that is precisely
        // where a half-swapped palette would produce light-on-light.
        val ops = buildRowDrawOps(
            cells = listOf(cell(inverse = true)),
            defaultFg = LightTerminalPalette.defaultFg,
            defaultBg = LightTerminalPalette.defaultBg,
            minLumaDelta = LightTerminalPalette.minLumaDelta,
        )
        val op = ops.single()
        assertTrue(
            "invertido ficou ilegível: fg 0x${"%06X".format(op.fg)} sobre bg 0x${"%06X".format(op.bg)}",
            contrast(op.fg, op.bg) >= 4.5,
        )
    }

    @Test
    fun `as duas paletas tem cursor visivel sobre o proprio fundo`() {
        // The cursor used to be a fixed translucent white. On a light
        // background, invisible.
        assertTrue(
            "cursor do tema claro precisa ser escuro",
            LightTerminalPalette.cursor.red < 0.5f && LightTerminalPalette.cursor.green < 0.5f,
        )
        assertTrue(
            "cursor do tema escuro precisa ser claro",
            DarkTerminalPalette.cursor.red > 0.5f && DarkTerminalPalette.cursor.green > 0.5f,
        )
    }

    @Test
    fun `o texto padrao de cada paleta e legivel no fundo dela`() {
        assertTrue(contrast(LightTerminalPalette.defaultFg, LightTerminalPalette.defaultBg) >= 7.0)
        assertTrue(contrast(DarkTerminalPalette.defaultFg, DarkTerminalPalette.defaultBg) >= 7.0)
    }
}
