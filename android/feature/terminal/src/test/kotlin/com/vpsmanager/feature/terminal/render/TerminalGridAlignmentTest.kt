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
 * Measures, in PIXELS, whether the terminal draws on a grid. "Grid" means
 * three verifiable things, one per test: the glyph advance equals the cell
 * width (otherwise a gap is left over), no glyph is clipped by the cell, and
 * the same character lands at the same offset inside ANY column. It is the
 * difference between "it looks nice" and "it is a grid" — without a grid, the
 * `ls -l` table, `htop` and a text editor all come out crooked.
 *
 * Runs on the JVM with `@GraphicsMode(NATIVE)`: Robolectric uses real Skia and
 * the real fonts from `android-all`, so `measureText` and `drawText` yield the
 * same metrics as the device. Without NATIVE, `android.graphics` is a stub and
 * the measurement would be worthless.
 *
 * The bug these tests lock down: [GlyphAtlas.paintFor] built the `Paint`
 * inside an `apply { this.typeface = Typeface.create(typeface, ...) }`. In
 * there, the bare identifier `typeface` resolves to the innermost receiver
 * (`Paint.getTypeface()`, null on a fresh Paint), not to the class property —
 * and `Typeface.create(null, style)` returns the DEFAULT face. The atlas
 * rasterised everything in Roboto (proportional: 'i' with an 8px advance, 'W'
 * with 29px) while the screen grid was measured on the monospaced face (20px
 * for everything). A narrow glyph left a gap on the right, a wide glyph was
 * clipped by the `clipRect` — `root@srv...` came out as "r oot @sr v...".
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class TerminalGridAlignmentTest {

    private val fontSizePx = 42f
    private val metrics = computeTerminalCellMetrics(fontSizePx)

    /**
     * The cell must be a whole pixel. If it is fractional (as it was: the
     * font's raw advance), each column's destination rectangle rounds
     * differently and the columns alternate between w and w+1 pixels wide,
     * stretching the glyph bitmap unevenly along the line.
     */
    @Test
    fun cellMetrics_areWholePixels_soTheGridNeverAccumulatesRoundingError() {
        assertTrue("largura de célula deve ser positiva", metrics.cellWidthPx > 0)
        assertTrue("altura de célula deve ser positiva", metrics.cellHeightPx > 0)
        assertEquals(
            "o corpo da fonte tem que ser o MESMO que o GlyphAtlas usa",
            metrics.cellHeightPx * GLYPH_TEXT_SIZE_RATIO,
            metrics.textSizePx,
            0f,
        )
    }

    /**
     * The grid the SCREEN computes has to come from a monospaced face: every
     * character with the same advance, and that advance equal to the cell
     * width. Pins `computeTerminalCellMetrics`.
     */
    @Test
    fun layoutCellWidth_comesFromAMonospacedAdvance() {
        val advances = SAMPLE.associateWith { monospaceAdvanceOf(it) }
        val distinct = advances.values.distinct()
        assertEquals(
            "todo glifo tem que ter o mesmo avanço (fonte monoespaçada); medido: $advances",
            1,
            distinct.size,
        )
        assertEquals(
            "o avanço do glifo tem que ser a largura da célula da grade; medido: $advances",
            metrics.cellWidthPx.toFloat(),
            distinct.single(),
            0.5f,
        )
    }

    /**
     * The test that really catches the bug: the [GlyphAtlas] has to rasterise
     * with the SAME monospaced face the grid was measured from — and that can
     * only be checked by looking at the PIXELS the atlas produces, never by
     * rebuilding the `Paint` on the outside (it was precisely the atlas's
     * `Paint` that was wrong, so a test that builds its own sees nothing).
     *
     * Compares the ink width of each glyph drawn by the atlas with that of a
     * reference drawn explicitly in the monospaced face. Ink width is
     * invariant under translation, so it isolates exactly "which font was
     * used", without mixing in positioning. Uses the `wide` slot (two cells)
     * so that not even the wrong face gets clipped — were it clipped, the
     * widths could coincide by accident.
     *
     * Before the fix it failed by a wide margin: in Roboto 'i' has ~4px of ink
     * and 'W' ~27px, against 15px and 20px in the monospaced face.
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
                divergent += "'$ch': atlas desenhou ${drawn}px de tinta, monoespaçada desenha ${expected}px"
            }
        }
        assertTrue(
            "o atlas não está usando a fonte monoespaçada:\n" + divergent.joinToString("\n"),
            divergent.isEmpty(),
        )
    }

    /**
     * No glyph may be CLIPPED by the cell.
     *
     * The unclipped reference is the atlas itself: the same codepoint
     * rasterised into a `wide` slot (two cells wide), where there is room to
     * spare. If the glyph's ink in the narrow slot is narrower than in the
     * wide slot, a piece is missing — it was clipped. Using the atlas itself
     * as the reference keeps the test on the production path, without
     * rebuilding the `Paint` on the outside.
     *
     * Before the fix the atlas rasterised in Roboto: 'W'/'M'/'@' have an
     * advance of ~29px and did not fit the 20px cell, so the `clipRect` ate
     * the side of the drawing — that is what made the 'm' in "manager" look
     * like "rn" and the '@' come out halved.
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
                "'$ch' perdeu pixels na célula estreita: $narrowInk px contra $wideInk px com espaço de sobra (glifo cortado)",
                wideInk,
                narrowInk,
            )
        }
    }

    /**
     * The POSITION test proper: a line of 80 columns, and for every column N
     * the glyph's ink has to land at the same offset relative to the cell
     * origin, with the origin at exactly `N * cellWidthPx`.
     *
     * This is the definition of a grid: the same character in column 0 and in
     * column 79 occupies the same pixels, up to the translation of 79 cells.
     * Without it, the columns of `ls -l`, of `htop` and of an editor come out
     * crooked. Note: it is NOT claimed that the ink is centred in the cell —
     * DroidSansMono's '1', for instance, has ink at [3..11] of a 20 advance by
     * the font's own design. What the grid demands is that this offset be the
     * SAME in every column.
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
                ?: throw AssertionError("'$ch': a coluna 0 não desenhou nada")
            val expected = (reference.first - 0)..(reference.last - 0)
            for (column in 1 until cols) {
                val left = column * w
                val ink = inkRangeIn(bitmap, left, left + w)
                    ?: throw AssertionError("'$ch' coluna $column: nada desenhado")
                assertEquals(
                    "'$ch' coluna $column: tinta começa em x=${ink.first}, esperado ${left + expected.first}",
                    left + expected.first,
                    ink.first,
                )
                assertEquals(
                    "'$ch' coluna $column: tinta termina em x=${ink.last}, esperado ${left + expected.last}",
                    left + expected.last,
                    ink.last,
                )
            }
        }
    }

    /**
     * The grid has to be the SAME on every line: column N of one line lands at
     * exactly the same x as column N of any other line, whatever the content
     * of the two. That is what makes the columns of `ls -l` and `htop` align.
     */
    @Test
    fun sameColumnLandsAtTheSameXOnEveryRow_regardlessOfRowContent() {
        val w = metrics.cellWidthPx
        val h = metrics.cellHeightPx
        val cols = 40

        // Two lines with completely different content in the preceding
        // columns; column 39 is '#' on both.
        val rowA = CharArray(cols) { 'i' }.also { it[cols - 1] = '#' }.concatToString()
        val rowB = CharArray(cols) { 'W' }.also { it[cols - 1] = '#' }.concatToString()

        val inkA = inkRangeOfColumn(rowA, cols - 1, w, h)
        val inkB = inkRangeOfColumn(rowB, cols - 1, w, h)
        assertEquals("a coluna ${cols - 1} tem que começar no mesmo x nas duas linhas", inkA?.first, inkB?.first)
        assertEquals("a coluna ${cols - 1} tem que terminar no mesmo x nas duas linhas", inkA?.last, inkB?.last)
    }

    /**
     * A fractional cell width must not let the columns drift: column N starts
     * at `round(N*w)` and butts up exactly where N+1 starts. With the old
     * truncation (`toInt()`), column N started at `floor(N*w)` and the widths
     * alternated between w and w+1.
     */
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
        // debugSolidBlocks: each column becomes a solid block of exactly the
        // size of the destination rectangle, which is precisely the arithmetic
        // under test.
        rasterizeRow(canvas, ops, 0f, w, h, atlas, defaultBg = 0x000000, debugSolidBlocks = true)

        for (column in 0 until cols) {
            val expectedLeft = Math.round(column * w)
            val expectedRight = Math.round((column + 1) * w)
            val ink = inkRangeIn(bitmap, expectedLeft, expectedRight)
            assertEquals("coluna $column começa em x errado", expectedLeft, ink?.first)
            assertEquals("coluna $column termina em x errado", expectedRight - 1, ink?.last)
        }
    }

    // ---- measurement helpers ----

    /** `Paint` on the intended monospaced face, at the grid's font size. */
    private fun monospacePaint(): Paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        typeface = Typeface.create(Typeface.MONOSPACE, Typeface.NORMAL)
        textSize = metrics.textSizePx
        color = -1
    }

    private fun monospaceAdvanceOf(ch: Char): Float = monospacePaint().measureText(ch.toString())

    /**
     * Ink width of the glyph drawn in the monospaced face with room to spare —
     * the reference against which what the atlas produced is compared.
     */
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
        /**
         * Characters chosen for having VERY different widths in a proportional
         * font (in Roboto: 'i'=8px, '1'=19px, 'W'=29px, '@'=30px) and
         * identical widths in a monospaced one. It is that difference the
         * tests measure.
         */
        const val SAMPLE = "iWl1m@#Mgt0"
    }
}
