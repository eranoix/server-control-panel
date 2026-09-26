package com.vpsmanager.feature.terminal.render

import android.graphics.Bitmap
import android.graphics.Canvas
import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Pins down the contract of [rasterizeFrame]: a frame is a PURE function of
 * the grid contents. Drawing the same content twice has to produce exactly the
 * same pixels, even when nothing changed between the two passes.
 *
 * The defect this guards against: there used to be a "dirty line" cache in
 * [TerminalCanvas] that skipped any line whose content had not changed since
 * the previous frame. Since the surface is cleared at the start of every pass,
 * skipping did not preserve the line — it erased it. All that was left on
 * screen was the line that had changed in that frame (nearly always the cursor
 * one), and the output of earlier commands simply vanished. A cache like that
 * would only be valid with a buffer persisted between frames, which neither
 * Compose's `Canvas` nor `SurfaceView`'s `lockCanvas` offers.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class FrameRasterizerTest {

    private val metrics = computeTerminalCellMetrics(42f)
    private val cols = 20
    private val rows = 6

    /** "linha 0", "linha 1"... one per row, so every row has its own content. */
    private fun cellAt(x: Int, y: Int): CellSnapshot.Cell {
        val text = "linha $y do terminal"
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

    /**
     * EVERY row has to appear in the frame, not just the one that changed last.
     */
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
            assertTrue("a linha $y não desenhou nada", hasInk)
        }
    }

    /**
     * Redrawing the same content has to produce the same frame. If anyone
     * reintroduces a cache between frames in the drawing path, the second
     * pass comes out different (typically blank) and this test breaks.
     */
    @Test
    fun redrawingTheSameContentProducesAnIdenticalFrame() {
        val first = newBitmap()
        drawFrame(first)

        // Same surface, second pass: the result has to be identical.
        val second = newBitmap()
        drawFrame(second)
        drawFrame(second)

        var differing = 0
        for (y in 0 until first.height) {
            for (x in 0 until first.width) {
                if (first.getPixel(x, y) != second.getPixel(x, y)) differing++
            }
        }
        assertEquals("a segunda passada de desenho mudou $differing pixels", 0, differing)
    }
}
