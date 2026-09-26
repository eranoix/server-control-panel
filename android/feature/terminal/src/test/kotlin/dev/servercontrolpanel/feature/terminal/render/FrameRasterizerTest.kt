package dev.servercontrolpanel.feature.terminal.render

import android.graphics.Bitmap
import android.graphics.Canvas
import dev.servercontrolpanel.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * [rasterizeFrame] must be a pure function of the grid contents. The surface is
 * cleared every pass, so skipping "unchanged" lines erases them; a per-line cache is
 * only valid with a buffer kept between frames, which neither Compose's `Canvas` nor
 * `SurfaceView.lockCanvas` provides.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class FrameRasterizerTest {

    private val metrics = computeTerminalCellMetrics(42f)
    private val cols = 20
    private val rows = 6

    /** Distinct text on every row. */
    private fun cellAt(x: Int, y: Int): CellSnapshot.Cell {
        val text = "row $y of the terminal"
        val ch = if (x < text.length) text[x] else ' '
        return CellSnapshot.Cell(
            codepoint = ch.code,
            fg = null,
            bg = null,
            bold = false,
            italic = false,
            faint = false,
            blink = false,
            inverse = false,
            invisible = false,
            strikethrough = false,
            overline = false,
            underline = 0,
            wide = CellSnapshot.Wide.NARROW,
        )
    }

    private fun drawFrame(bitmap: Bitmap) {
        val atlas = GlyphAtlas(
            cellWidthPx = metrics.cellWidthPx,
            cellHeightPx = metrics.cellHeightPx,
            textSizePx = metrics.textSizePx,
        )
        rasterizeFrame(
            canvas = Canvas(bitmap),
            cols = cols,
            rows = rows,
            cellAt = ::cellAt,
            cellWidthPx = metrics.cellWidthPx.toFloat(),
            cellHeightPx = metrics.cellHeightPx.toFloat(),
            glyphAtlas = atlas,
            defaultFg = 0xE0E0E0,
            defaultBg = 0x000000,
        )
    }

    private fun newBitmap() = Bitmap.createBitmap(
        cols * metrics.cellWidthPx,
        rows * metrics.cellHeightPx,
        Bitmap.Config.ARGB_8888,
    )

    /** Every row appears in the frame, not just the one that changed last. */
    @Test
    fun everyRowIsPaintedInASingleFrame() {
        val bitmap = newBitmap()
        drawFrame(bitmap)
        for (y in 0 until rows) {
            val hasInk = (0 until metrics.cellHeightPx).any { dy ->
                (0 until bitmap.width).any { x ->
                    (bitmap.getPixel(x, y * metrics.cellHeightPx + dy) and 0x00ffffff) != 0
                }
            }
            assertTrue("row $y drew nothing", hasInk)
        }
    }

    /** Redrawing the same content must produce the same frame; a cache between frames would blank the second pass. */
    @Test
    fun redrawingTheSameContentProducesAnIdenticalFrame() {
        val first = newBitmap()
        drawFrame(first)

        val second = newBitmap()
        drawFrame(second)
        drawFrame(second)

        var differing = 0
        for (y in 0 until first.height) {
            for (x in 0 until first.width) {
                if (first.getPixel(x, y) != second.getPixel(x, y)) differing++
            }
        }
        assertEquals("the second drawing pass changed $differing pixels", 0, differing)
    }
}
