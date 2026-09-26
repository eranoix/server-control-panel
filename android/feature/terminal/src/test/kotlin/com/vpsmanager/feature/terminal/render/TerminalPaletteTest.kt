package com.vpsmanager.feature.terminal.render

import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.math.abs
import kotlin.math.pow

/**
 * Legibility guard for the light theme: the ANSI palette is designed for a black
 * background, so colors like bright white or yellow would be invisible. The tests use
 * the real WCAG contrast ratio to validate the renderer's cheaper luma threshold.
 */
class TerminalPaletteTest {

    // WCAG 2.x contrast ratio.
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

    /** ANSI palette colors that vanish on a light background. */
    private val colorsThatVanishOnLight = mapOf(
        "branco brilhante" to 0xFFFFFF,
        "amarelo brilhante" to 0xFFFF00,
        "ciano brilhante" to 0x00FFFF,
        "verde brilhante" to 0x00FF00,
        "branco (ls: arquivo comum)" to 0xE0E0E0,
        "amarelo (ls: dispositivo)" to 0xCDCD00,
        "ciano (ls: link simbolico)" to 0x00CDCD,
    )

    /** The full 16-color ANSI palette as the emulator resolves it. */
    private val ansi16Palette = listOf(
        0x000000, 0xCD0000, 0x00CD00, 0xCDCD00, 0x0000EE, 0xCD00CD, 0x00CDCD, 0xE5E5E5,
        0x7F7F7F, 0xFF0000, 0x00FF00, 0xFFFF00, 0x5C5CFF, 0xFF00FF, 0x00FFFF, 0xFFFFFF,
    )

    @Test
    fun `in the light theme the whole ANSI palette is legible`() {
        val bg = LightTerminalPalette.defaultBg
        ansi16Palette.forEach { color ->
            val adjusted = adjustForContrast(color, bg, LightTerminalPalette.minLumaDelta)
            val ratio = contrast(adjusted, bg)
            assertTrue(
                "0x${"%06X".format(color)} on the light background reached ${"%.2f".format(ratio)}:1 " +
                    "(adjusted to 0x${"%06X".format(adjusted)})",
                ratio >= 4.5,
            )
        }
    }

    @Test
    fun `in the light theme the vanishing colors become legible`() {
        val bg = LightTerminalPalette.defaultBg
        colorsThatVanishOnLight.forEach { (name, color) ->
            val adjusted = adjustForContrast(color, bg, LightTerminalPalette.minLumaDelta)
            val ratio = contrast(adjusted, bg)
            assertTrue(
                "$name on the light background reached ${"%.2f".format(ratio)}:1 " +
                    "(adjusted color 0x${"%06X".format(adjusted)})",
                ratio >= 4.5,
            )
        }
    }

    @Test
    fun `without the guard these colors would be illegible`() {
        // If this ever fails, the guard is no longer needed.
        val bg = LightTerminalPalette.defaultBg
        colorsThatVanishOnLight.forEach { (name, color) ->
            assertTrue(
                "$name would not need the guard, review",
                contrast(color, bg) < 4.5,
            )
        }
    }

    @Test
    fun `the guard preserves hue so yellow stays yellow`() {
        val yellow = adjustForContrast(0xFFFF00, LightTerminalPalette.defaultBg, LightTerminalPalette.minLumaDelta)
        val r = (yellow shr 16) and 0xff
        val g = (yellow shr 8) and 0xff
        val b = yellow and 0xff
        // High red and green, zero blue: still yellow, just dark.
        assertEquals("yellow's blue channel must stay at zero", 0, b)
        assertTrue("yellow turned gray", r > b && g > b)
        assertEquals("yellow lost its R=G symmetry", r, g)

        val ciano = adjustForContrast(0x00FFFF, LightTerminalPalette.defaultBg, LightTerminalPalette.minLumaDelta)
        assertEquals(0, (ciano shr 16) and 0xff)
        assertTrue("cyan is no longer cyan", (ciano and 0xff) > 0)
    }

    @Test
    fun `colors that already contrast are left untouched`() {
        val bg = LightTerminalPalette.defaultBg
        listOf(0x000000, 0x1A1A1A, 0x0000AA, 0x800000).forEach { color ->
            assertEquals(
                "0x${"%06X".format(color)} already contrasts and should not be changed",
                color,
                adjustForContrast(color, bg, LightTerminalPalette.minLumaDelta),
            )
        }
    }

    @Test
    fun `in the dark theme nothing changes`() {
        val bg = DarkTerminalPalette.defaultBg
        val delta = DarkTerminalPalette.minLumaDelta
        // With the guard off, no color is touched, even dark ones on black.
        ansi16Palette.forEach { color ->
            assertEquals(color, adjustForContrast(color, bg, delta))
        }
    }

    @Test
    fun `the guard also applies against a colored cell background`() {
        // White text on a light background set by the program vanishes the same way.
        val adjusted = adjustForContrast(0xFFFFFF, 0xF0F0F0, LightTerminalPalette.minLumaDelta)
        assertTrue(contrast(adjusted, 0xF0F0F0) >= 4.5)
    }

    @Test
    fun `a dark background pushes text lighter, not darker`() {
        // Dark blue on black is lightened, never darkened further.
        val adjusted = adjustForContrast(0x000080, 0x000000, minLumaDelta = 150)
        assertTrue("should lighten", luma(adjusted) > luma(0x000080))
    }

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
    fun `buildRowDrawOps applies the guard in the light theme`() {
        val ops = buildRowDrawOps(
            cells = listOf(cell(fg = 0xFFFFFF)),
            defaultFg = LightTerminalPalette.defaultFg,
            defaultBg = LightTerminalPalette.defaultBg,
            minLumaDelta = LightTerminalPalette.minLumaDelta,
        )
        assertNotEquals("white on a light background should have been adjusted", 0xFFFFFF, ops.single().fg)
        assertTrue(contrast(ops.single().fg, LightTerminalPalette.defaultBg) >= 4.5)
    }

    @Test
    fun `buildRowDrawOps without the guard is unchanged`() {
        // The parameter defaults to 0, so callers without the guard are unaffected.
        val cells = listOf(cell(fg = 0xFFFFFF), cell(fg = 0x00FF00, bg = 0x000080), cell())
        val withoutParam = buildRowDrawOps(cells, defaultFg = 0xE0E0E0, defaultBg = 0x000000)
        val withZero = buildRowDrawOps(cells, defaultFg = 0xE0E0E0, defaultBg = 0x000000, minLumaDelta = 0)
        assertEquals(withoutParam, withZero)
        assertEquals(0xFFFFFF, withoutParam[0].fg)
    }

    @Test
    fun `faint stays weaker than normal even with the guard`() {
        // The guard must run before the faint pass, or it would undo the fading.
        val cells = listOf(cell(fg = 0xFFFFFF), cell(fg = 0xFFFFFF, faint = true))
        val ops = buildRowDrawOps(
            cells,
            defaultFg = LightTerminalPalette.defaultFg,
            defaultBg = LightTerminalPalette.defaultBg,
            minLumaDelta = LightTerminalPalette.minLumaDelta,
        )
        val normal = ops[0].fg
        val esmaecido = ops[1].fg
        assertNotEquals("faint equals normal, so the attribute has no effect", normal, esmaecido)
        val background = luma(LightTerminalPalette.defaultBg)
        assertTrue(
            "faint must be closer to the background than normal",
            abs(luma(esmaecido) - background) < abs(luma(normal) - background),
        )
        // But still visible.
        assertTrue("faint vanished into the background", abs(luma(esmaecido) - background) > 40)
    }

    @Test
    fun `inverse video in the light theme does not become light text on light background`() {
        // `inverse` swaps default fg and bg; a half-swapped palette would give
        // light on light.
        val ops = buildRowDrawOps(
            cells = listOf(cell(inverse = true)),
            defaultFg = LightTerminalPalette.defaultFg,
            defaultBg = LightTerminalPalette.defaultBg,
            minLumaDelta = LightTerminalPalette.minLumaDelta,
        )
        val op = ops.single()
        assertTrue(
            "inverse became illegible: fg 0x${"%06X".format(op.fg)} on bg 0x${"%06X".format(op.bg)}",
            contrast(op.fg, op.bg) >= 4.5,
        )
    }

    @Test
    fun `both palettes have a cursor visible on their own background`() {
        // A fixed translucent white cursor would be invisible on a light background.
        assertTrue(
            "the light theme cursor must be dark",
            LightTerminalPalette.cursor.red < 0.5f && LightTerminalPalette.cursor.green < 0.5f,
        )
        assertTrue(
            "the dark theme cursor must be light",
            DarkTerminalPalette.cursor.red > 0.5f && DarkTerminalPalette.cursor.green > 0.5f,
        )
    }

    @Test
    fun `each palette's default text is legible on its background`() {
        assertTrue(contrast(LightTerminalPalette.defaultFg, LightTerminalPalette.defaultBg) >= 7.0)
        assertTrue(contrast(DarkTerminalPalette.defaultFg, DarkTerminalPalette.defaultBg) >= 7.0)
    }
}
