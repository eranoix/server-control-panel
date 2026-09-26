package com.vpsmanager.feature.terminal.render

import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Typeface
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.vpsmanager.feature.terminal.prefs.TerminalLineSpacing
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Line spacing, measured against the device's REAL font.
 *
 * This has to be an instrumented test, not a JVM one: the Android Gradle
 * Plugin stub's `Paint` measures no font at all, and this feature's floor
 * comes entirely out of `Paint.getTextBounds` and `Paint.fontMetrics`. A JVM
 * test here would prove arithmetic, not typography.
 */
@RunWith(AndroidJUnit4::class)
class LineSpacingMetricsTest {

    private val bodies = listOf(10f * 2.625f, 16f * 2.625f, 28f * 2.625f)

    @Test
    fun cellHeightIsAlwaysPositiveInteger() {
        // The condition for a 1:1 blit: the atlas slot and the destination
        // rectangle have to be exactly the same size, which is why the height
        // cannot be fractional at any step of the ladder.
        for (body in bodies) {
            for (step in TerminalLineSpacing.entries) {
                val m = computeTerminalCellMetrics(body, step.deltaPx)
                assertTrue(
                    "corpo=$body passo=$step altura=${m.cellHeightPx}",
                    m.cellHeightPx > 0,
                )
            }
        }
    }

    @Test
    fun glyphBodyDoesNotChangeWithLineSpacing() {
        // The owner asked for "less vertical space", not "smaller type".
        // Before this work `textSizePx` came out of `cellHeightPx`, and
        // tightening the line spacing would have shrunk the glyph with it —
        // this test exists so nobody ties the two together again.
        for (body in bodies) {
            val sizes = TerminalLineSpacing.entries.map {
                computeTerminalCellMetrics(body, it.deltaPx).textSizePx
            }.distinct()
            assertEquals("corpo=$body tamanhos=$sizes", 1, sizes.size)
        }
    }

    @Test
    fun cellWidthDoesNotChangeWithLineSpacing() {
        // Line spacing is vertical. If it touched the width it would change
        // the number of COLUMNS, and therefore where the remote program wraps
        // its text — which is exactly the damage this work went to fix
        // elsewhere.
        for (body in bodies) {
            val widths = TerminalLineSpacing.entries.map {
                computeTerminalCellMetrics(body, it.deltaPx).cellWidthPx
            }.distinct()
            assertEquals("corpo=$body larguras=$widths", 1, widths.size)
        }
    }

    @Test
    fun noStepClipsGlyphs() {
        // The floor is not a safety margin: it is the exact height at which
        // the sample's most extreme letter touches the cell's edge. Here the
        // sum is redone from the outside, with the same baseline centring the
        // GlyphAtlas uses, and NOTHING is allowed to cross the edge.
        val sample = "ÂÊÍÕÜWMbdfhklt gjpqy ç,;_"
        for (body in bodies) {
            for (step in TerminalLineSpacing.entries) {
                val m = computeTerminalCellMetrics(body, step.deltaPx)
                val paint = Paint().apply {
                    typeface = Typeface.MONOSPACE
                    textSize = m.textSizePx
                }
                val fm = paint.fontMetrics
                val base = (m.cellHeightPx - fm.ascent - fm.descent) / 2f
                val inbox = Rect()
                for (c in sample) {
                    paint.getTextBounds(c.toString(), 0, 1, inbox)
                    val top = base + inbox.top
                    val background = base + inbox.bottom
                    assertTrue(
                        "corpo=$body passo=$step '$c' sai por cima: topo=$top",
                        top >= -0.5f,
                    )
                    assertTrue(
                        "corpo=$body passo=$step '$c' sai por baixo: fundo=$background altura=${m.cellHeightPx}",
                        background <= m.cellHeightPx + 0.5f,
                    )
                }
            }
        }
    }

    @Test
    fun ladderIsMonotonic_andCompactReallyGainsRows() {
        // Two steps that produce the SAME height are two menu items that do
        // the same thing — and that is how a preference gets a reputation for
        // being broken. It is why the ladder became a whole-pixel delta rather
        // than a fractional multiplier: at small type sizes, 0.90 and 0.95
        // round to the same pixel.
        val body = 16f * 2.625f
        val heights = TerminalLineSpacing.entries.map {
            computeTerminalCellMetrics(body, it.deltaPx).cellHeightPx
        }
        assertEquals("a escada tem de ser estritamente crescente: $heights", heights.sorted(), heights)
        assertEquals("nenhum passo pode repetir outro: $heights", heights.distinct().size, heights.size)

        val normal = computeTerminalCellMetrics(body, TerminalLineSpacing.NORMAL.deltaPx).cellHeightPx
        val compact = computeTerminalCellMetrics(body, TerminalLineSpacing.COMPACT.deltaPx).cellHeightPx
        assertTrue("compacta ($compact) tem de ser menor que normal ($normal)", compact < normal)
    }

    @Test
    fun normalChangedNothing() {
        // A non-regression guard: the grid everyone has always had stays the
        // grid everyone has always had. If this test falls, everyone who never
        // touched the preference has had their screen moved.
        for (body in bodies) {
            val m = computeTerminalCellMetrics(body, TerminalLineSpacing.NORMAL.deltaPx)
            assertEquals(
                "corpo=$body",
                Math.round(body),
                m.cellHeightPx,
            )
        }
    }
}
