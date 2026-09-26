package com.vpsmanager.feature.terminal.render

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Typeface
import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * Checks in pixels that the terminal draws on a true grid: glyph advance equals the
 * cell width, no glyph is clipped, and every column places a glyph at the same offset.
 *
 * Uses `@GraphicsMode(NATIVE)` so Robolectric renders with real Skia and fonts; the
 * default stub would make the measurements meaningless. Inside a `Paint.apply {}`, a
 * bare `typeface` resolves to the Paint's own (null) property, which silently selects
 * the proportional default face; these tests catch that.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class TerminalGridAlignmentTest {

    private val fontSizePx = 42f
    private val metrics = computeTerminalCellMetrics(fontSizePx)

    /** Cell size must be whole pixels, or columns alternate between w and w+1 pixels. */
    @Test
    fun cellMetrics_areWholePixels_soTheGridNeverAccumulatesRoundingError() {
        assertTrue("cell width must be positive", metrics.cellWidthPx > 0)
        assertTrue("cell height must be positive", metrics.cellHeightPx > 0)
        assertEquals(
            "the font size must be the same one GlyphAtlas uses",
            metrics.cellHeightPx * GLYPH_TEXT_SIZE_RATIO,
            metrics.textSizePx,
            0f,
        )
    }

    /** `computeTerminalCellMetrics` must use a monospaced face whose advance equals the cell width. */
    @Test
    fun layoutCellWidth_comesFromAMonospacedAdvance() {
        val advances = SAMPLE.associateWith { monospaceAdvanceOf(it) }
        val distinct = advances.values.distinct()
        assertEquals(
            "every glyph must have the same advance (monospaced font); measured: $advances",
            1,
            distinct.size,
        )
        assertEquals(
            "the glyph advance must equal the grid cell width; measured: $advances",
            metrics.cellWidthPx.toFloat(),
            distinct.single(),
            0.5f,
        )
    }

    /**
     * [GlyphAtlas] must rasterize with the monospaced face the grid was measured from.
     * Checked on the atlas's own pixels (not a rebuilt `Paint`) by comparing ink width,
     * which ignores position, in a two-cell `wide` slot so nothing is clipped.
     */
    @Test
    fun atlasRasterizesWithTheSameMonospaceFaceTheGridWasMeasuredFrom() {
        val atlas = GlyphAtlas(
            cellWidthPx = metrics.cellWidthPx,
            cellHeightPx = metrics.cellHeightPx,
            textSizePx = metrics.textSizePx,
        )
        val divergent = mutableListOf<String>()
        for (ch in SAMPLE) {
            val slot = atlas.slotFor(GlyphKey(ch.code, bold = false, italic = false, wide = true))
            val drawn = inkWidthIn(slot.bitmap, slot.rect.left, slot.rect.right)
            val expected = referenceMonospaceInkWidth(ch)
            if (drawn != expected) {
                divergent += "'$ch': atlas drew ${drawn}px of ink, monospace draws ${expected}px"
            }
        }
        assertTrue(
            "the atlas is not using the monospaced font:\n" + divergent.joinToString("\n"),
            divergent.isEmpty(),
        )
    }

    /**
     * No glyph may be clipped by its cell: its ink in a narrow slot must match the same
     * glyph in a two-cell `wide` slot, which has room to spare.
     */
    @Test
    fun noGlyphIsClippedByItsCell() {
        val atlas = GlyphAtlas(
            cellWidthPx = metrics.cellWidthPx,
            cellHeightPx = metrics.cellHeightPx,
            textSizePx = metrics.textSizePx,
        )
        for (ch in SAMPLE) {
            val narrow = atlas.slotFor(GlyphKey(ch.code, bold = false, italic = false, wide = false))
            val wide = atlas.slotFor(GlyphKey(ch.code, bold = false, italic = false, wide = true))
            val narrowInk = inkWidthIn(narrow.bitmap, narrow.rect.left, narrow.rect.right)
            val wideInk = inkWidthIn(wide.bitmap, wide.rect.left, wide.rect.right)
            assertEquals(
                "'$ch' lost pixels in the narrow cell: $narrowInk px versus $wideInk px with room to spare (glyph clipped)",
                wideInk,
                narrowInk,
            )
        }
    }

    /**
     * In an 80-column row, each glyph's ink must sit at the same offset from its cell
     * origin `N * cellWidthPx`. Ink need not be centered, only consistent across columns.
     */
    @Test
    fun everyColumnPlacesItsGlyphAtTheSameOffsetFromTheCellOrigin() {
        val w = metrics.cellWidthPx
        val h = metrics.cellHeightPx
        val cols = 80

        for (ch in SAMPLE) {
            val bitmap = Bitmap.createBitmap(cols * w, h, Bitmap.Config.ARGB_8888)
            val canvas = Canvas(bitmap)
            canvas.drawColor(0xff000000.toInt())
            val atlas = GlyphAtlas(cellWidthPx = w, cellHeightPx = h, textSizePx = metrics.textSizePx)
            val ops = buildRowDrawOps(
                cells = (0 until cols).map { cell(ch) },
                defaultFg = 0xE0E0E0,
                defaultBg = 0x000000,
            )
            rasterizeRow(canvas, ops, 0f, w.toFloat(), h.toFloat(), atlas, defaultBg = 0x000000)

            val reference = inkRangeIn(bitmap, 0, w)
                ?: throw AssertionError("'$ch': column 0 drew nothing")
            val expected = (reference.first - 0)..(reference.last - 0)
            for (column in 1 until cols) {
                val left = column * w
                val ink = inkRangeIn(bitmap, left, left + w)
                    ?: throw AssertionError("'$ch' column $column: nothing drawn")
                assertEquals(
                    "'$ch' column $column: ink starts at x=${ink.first}, expected ${left + expected.first}",
                    left + expected.first,
                    ink.first,
                )
                assertEquals(
                    "'$ch' column $column: ink ends at x=${ink.last}, expected ${left + expected.last}",
                    left + expected.last,
                    ink.last,
                )
            }
        }
    }

    /** Column N lands at the same x on every row, whatever the row content. */
    @Test
    fun sameColumnLandsAtTheSameXOnEveryRow_regardlessOfRowContent() {
        val w = metrics.cellWidthPx
        val h = metrics.cellHeightPx
        val cols = 40

        // Different content before the last column, which is '#' on both rows.
        val rowA = CharArray(cols) { 'i' }.also { it[cols - 1] = '#' }.concatToString()
        val rowB = CharArray(cols) { 'W' }.also { it[cols - 1] = '#' }.concatToString()

        val inkA = inkRangeOfColumn(rowA, cols - 1, w, h)
        val inkB = inkRangeOfColumn(rowB, cols - 1, w, h)
        assertEquals("column ${cols - 1} must start at the same x on both rows", inkA?.first, inkB?.first)
        assertEquals("column ${cols - 1} must end at the same x on both rows", inkA?.last, inkB?.last)
    }

    /** With a fractional cell width, column N spans exactly `round(N*w)` to `round((N+1)*w)`. */
    @Test
    fun fractionalCellWidth_stillPlacesEachColumnAtItsRoundedGridPosition() {
        val w = 20.16f
        val h = 42f
        val cols = 40
        val bitmap = Bitmap.createBitmap(Math.round(cols * w), h.toInt(), Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        canvas.drawColor(0xff000000.toInt())

        val atlas = GlyphAtlas(cellWidthPx = Math.round(w), cellHeightPx = h.toInt())
        val ops = buildRowDrawOps(
            cells = (0 until cols).map { cell('#') },
            defaultFg = 0xE0E0E0,
            defaultBg = 0x000000,
        )
        // debugSolidBlocks fills each destination rect, exposing exactly the arithmetic under test.
        rasterizeRow(canvas, ops, 0f, w, h, atlas, defaultBg = 0x000000, debugSolidBlocks = true)

        for (column in 0 until cols) {
            val expectedLeft = Math.round(column * w)
            val expectedRight = Math.round((column + 1) * w)
            val ink = inkRangeIn(bitmap, expectedLeft, expectedRight)
            assertEquals("column $column starts at the wrong x", expectedLeft, ink?.first)
            assertEquals("column $column ends at the wrong x", expectedRight - 1, ink?.last)
        }
    }

    /** `Paint` on the intended monospaced face, at the grid's font size. */
    private fun monospacePaint(): Paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        typeface = Typeface.create(Typeface.MONOSPACE, Typeface.NORMAL)
        textSize = metrics.textSizePx
        color = -1
    }

    private fun monospaceAdvanceOf(ch: Char): Float = monospacePaint().measureText(ch.toString())

    /** Reference ink width of the glyph drawn in the monospaced face with room to spare. */
    private fun referenceMonospaceInkWidth(ch: Char): Int {
        val paint = monospacePaint()
        val pad = metrics.cellWidthPx
        val bitmap = Bitmap.createBitmap(pad * 4, metrics.cellHeightPx, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        val fm = paint.fontMetrics
        val baselineY = (metrics.cellHeightPx - fm.ascent - fm.descent) / 2f
        canvas.drawText(ch.toString(), pad.toFloat(), baselineY, paint)
        return inkWidthIn(bitmap, 0, bitmap.width)
    }

    private fun cell(ch: Char) = CellSnapshot.Cell(
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

    private fun inkRangeOfColumn(row: String, column: Int, w: Int, h: Int): IntRange? {
        val bitmap = Bitmap.createBitmap(row.length * w, h, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bitmap)
        canvas.drawColor(0xff000000.toInt())
        val atlas = GlyphAtlas(cellWidthPx = w, cellHeightPx = h, textSizePx = metrics.textSizePx)
        val ops = buildRowDrawOps(row.map { cell(it) }, defaultFg = 0xE0E0E0, defaultBg = 0x000000)
        rasterizeRow(canvas, ops, 0f, w.toFloat(), h.toFloat(), atlas, defaultBg = 0x000000)
        return inkRangeIn(bitmap, column * w, (column + 1) * w)
    }

    /** Ink width within the given band, 0 if there is none. */
    private fun inkWidthIn(bitmap: Bitmap, fromX: Int, toX: Int): Int =
        inkRangeIn(bitmap, fromX, toX)?.let { it.last - it.first + 1 } ?: 0

    /** First and last pixel column with ink (non-background) in the band. */
    private fun inkRangeIn(bitmap: Bitmap, fromX: Int, toX: Int): IntRange? {
        var first = -1
        var last = -1
        for (x in fromX until minOf(toX, bitmap.width)) {
            var hasInk = false
            for (y in 0 until bitmap.height) {
                val p = bitmap.getPixel(x, y)
                // Background is opaque black; any lit channel is ink.
                if ((p and 0x00ffffff) != 0) {
                    hasInk = true
                    break
                }
            }
            if (hasInk) {
                if (first < 0) first = x
                last = x
            }
        }
        return if (first < 0) null else first..last
    }

    private companion object {
        /** Characters with very different widths in a proportional font but equal in a monospaced one. */
        const val SAMPLE = "iWl1m@#Mgt0"
    }
}
