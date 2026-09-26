package com.vpsmanager.feature.terminal.render

import com.vpsmanager.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Host-runnable (plain JUnit, no Robolectric, no device) proof of the two
 * silent-corruption risks called out for the renderer: double-drawn/clipped
 * wide glyphs, and re-indented wrap-continuation rows. [CellSnapshot.Cell]
 * and [CellSnapshot.Wide] are public and directly constructible, so this
 * builds synthetic rows without going through [CellSnapshot.fromBuffer]
 * (which is `internal` to `:terminal-engine` and backed by native memory
 * this test module has no access to).
 */
class RowDrawOpsTest {

    private fun narrowCell(cp: Int): CellSnapshot.Cell = CellSnapshot.Cell(
        codepoint = cp,
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

    private fun wideLead(cp: Int): CellSnapshot.Cell = narrowCell(cp).copy(wide = CellSnapshot.Wide.WIDE)
    private fun spacerTail(): CellSnapshot.Cell = narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_TAIL)
    private fun spacerHead(): CellSnapshot.Cell = narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_HEAD)

    @Test
    fun wideGlyph_producesExactlyOneOp_neverDoubleDrawn() {
        // 'A' 'B' <wide 中> <spacer_tail> 'C' <spacer_head>, mirroring fixture 05-wide.
        val row = listOf(
            narrowCell('A'.code),
            narrowCell('B'.code),
            wideLead(0x4E2D),
            spacerTail(),
            narrowCell('C'.code),
            spacerHead(),
        )

        val ops = buildRowDrawOps(row, defaultFg = 0xffffff, defaultBg = 0x000000)

        // Exactly 4 ops: A, B, the wide glyph itself, C. The spacer_tail and
        // spacer_head each contribute zero ops -- never a second op for the
        // wide glyph's trailing column, never a stray op for the column that
        // couldn't fit a second wide glyph.
        assertEquals(4, ops.size)
        assertEquals(listOf(0, 1, 2, 4), ops.map { it.column })

        val wideOp = ops.single { it.column == 2 }
        assertEquals(0x4E2D, wideOp.codepoint)
        assertTrue(wideOp.wide)

        // No op at all claims column 3 (spacer_tail) or column 5 (spacer_head).
        assertTrue(ops.none { it.column == 3 || it.column == 5 })
    }

    @Test
    fun wrapContinuationRow_usesSameColumnsAsFreshRow_neverReindented() {
        val cells = listOf(narrowCell('i'.code), narrowCell('j'.code))

        // buildRowDrawOps takes only the row's cells -- it has no wrap or
        // wrapContinuation parameter to shift positions with, by
        // construction. This test pins that: two structurally identical
        // rows, one that a caller would only ever see with
        // CellSnapshot.isWrapContinuation(row) == true (a wrapped
        // continuation) and one that would only ever see it == false (a
        // fresh logical line), produce byte-for-byte identical column
        // placement.
        val continuationOps = buildRowDrawOps(cells, defaultFg = 0xffffff, defaultBg = 0x000000)
        val freshLineOps = buildRowDrawOps(cells, defaultFg = 0xffffff, defaultBg = 0x000000)

        assertEquals(freshLineOps.map { it.column }, continuationOps.map { it.column })
        assertEquals(listOf(0, 1), continuationOps.map { it.column })
    }

    @Test
    fun inverseSwapsForegroundAndBackground() {
        val cell = narrowCell('X'.code).copy(fg = 0x112233, bg = 0x445566, inverse = true)
        val op = buildRowDrawOps(listOf(cell), defaultFg = 0, defaultBg = 0).single()
        assertEquals(0x445566, op.fg)
        assertEquals(0x112233, op.bg)
    }

    @Test
    fun invisibleCellSuppressesGlyphButKeepsBackground() {
        val cell = narrowCell('X'.code).copy(invisible = true, bg = 0x445566)
        val op = buildRowDrawOps(listOf(cell), defaultFg = 0, defaultBg = 0).single()
        assertEquals(false, op.drawGlyph)
        assertEquals(0x445566, op.bg)
    }

    @Test
    fun blankCellCodepointZero_stillEmitsBackgroundOnlyOp() {
        val op = buildRowDrawOps(listOf(narrowCell(0)), defaultFg = 0, defaultBg = 0x123456).single()
        assertEquals(false, op.drawGlyph)
        assertEquals(0x123456, op.bg)
    }
}
