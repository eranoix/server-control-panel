package dev.servercontrolpanel.feature.terminal.render

import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Typeface
import androidx.test.ext.junit.runners.AndroidJUnit4
import dev.servercontrolpanel.feature.terminal.prefs.TerminalLineSpacing
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Line spacing measured against the device's real font. Must be instrumented: the
 * JVM stub `Paint` measures no font, and the spacing floor comes from
 * `Paint.getTextBounds` and `Paint.fontMetrics`.
 */
@RunWith(AndroidJUnit4::class)
class LineSpacingMetricsTest {

    private val bodies = listOf(10f * 2.625f, 16f * 2.625f, 28f * 2.625f)

    @Test
    fun cellHeightIsAlwaysPositiveInteger() {
        // A 1:1 blit needs the atlas slot and destination rect to be the same size,
        // so the height is never fractional at any step.
        for (body in bodies) {
            for (step in TerminalLineSpacing.entries) {
                val m = computeTerminalCellMetrics(body, step.deltaPx)
                assertTrue(
                    "body=$body step=$step height=${m.cellHeightPx}",
                    m.cellHeightPx > 0,
                )
            }
        }
    }

    @Test
    fun glyphBodyDoesNotChangeWithLineSpacing() {
        // Tighter spacing means less vertical space, not smaller type: `textSizePx`
        // must not depend on `cellHeightPx`.
        for (body in bodies) {
            val sizes = TerminalLineSpacing.entries.map {
                computeTerminalCellMetrics(body, it.deltaPx).textSizePx
            }.distinct()
            assertEquals("body=$body sizes=$sizes", 1, sizes.size)
        }
    }

    @Test
    fun cellWidthDoesNotChangeWithLineSpacing() {
        // Changing the width would change the column count and where the remote
        // program wraps its text.
        for (body in bodies) {
            val widths = TerminalLineSpacing.entries.map {
                computeTerminalCellMetrics(body, it.deltaPx).cellWidthPx
            }.distinct()
            assertEquals("body=$body widths=$widths", 1, widths.size)
        }
    }

    @Test
    fun noStepClipsGlyphs() {
        // The floor is the exact height at which the most extreme sample glyph touches
        // the cell edge; recompute it with the GlyphAtlas baseline and allow no overflow.
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
                        "body=$body step=$step '$c' overflows the top: top=$top",
                        top >= -0.5f,
                    )
                    assertTrue(
                        "body=$body step=$step '$c' overflows the bottom: bottom=$background height=${m.cellHeightPx}",
                        background <= m.cellHeightPx + 0.5f,
                    )
                }
            }
        }
    }

    @Test
    fun ladderIsMonotonic_andCompactReallyGainsRows() {
        // Two steps with the same height would be menu items that do the same thing.
        // Hence whole-pixel deltas: fractional multipliers round together at small sizes.
        val body = 16f * 2.625f
        val heights = TerminalLineSpacing.entries.map {
            computeTerminalCellMetrics(body, it.deltaPx).cellHeightPx
        }
        assertEquals("the ladder must be strictly increasing: $heights", heights.sorted(), heights)
        assertEquals("no step may repeat another: $heights", heights.distinct().size, heights.size)

        val normal = computeTerminalCellMetrics(body, TerminalLineSpacing.NORMAL.deltaPx).cellHeightPx
        val compact = computeTerminalCellMetrics(body, TerminalLineSpacing.COMPACT.deltaPx).cellHeightPx
        assertTrue("compact ($compact) must be smaller than normal ($normal)", compact < normal)
    }

    @Test
    fun normalChangedNothing() {
        // NORMAL must keep the original grid for users who never touch the preference.
        for (body in bodies) {
            val m = computeTerminalCellMetrics(body, TerminalLineSpacing.NORMAL.deltaPx)
            assertEquals(
                "body=$body",
                Math.round(body),
                m.cellHeightPx,
            )
        }
    }
}
