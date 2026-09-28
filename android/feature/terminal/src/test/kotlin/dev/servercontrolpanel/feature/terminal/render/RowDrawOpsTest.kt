package dev.servercontrolpanel.feature.terminal.render

import dev.servercontrolpanel.terminalengine.CellSnapshot
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

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
        val row = listOf(
            narrowCell('A'.code),
            narrowCell('B'.code),
            wideLead(0x4E2D),
            spacerTail(),
            narrowCell('C'.code),
            spacerHead(),
        )

        val ops = buildRowDrawOps(row, defaultFg = 0xffffff, defaultBg = 0x000000)

        assertEquals(4, ops.size)
        assertEquals(listOf(0, 1, 2, 4), ops.map { it.column })

        val wideOp = ops.single { it.column == 2 }
        assertEquals(0x4E2D, wideOp.codepoint)
        assertTrue(wideOp.wide)

        assertTrue(ops.none { it.column == 3 || it.column == 5 })
    }

    @Test
    fun wrapContinuationRow_usesSameColumnsAsFreshRow_neverReindented() {
        val cells = listOf(narrowCell('i'.code), narrowCell('j'.code))

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
