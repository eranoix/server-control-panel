package com.vpsmanager.feature.terminal.selection

import com.vpsmanager.terminalengine.CellSnapshot

/**
 * Builds a real [CellSnapshot] via reflection, the same workaround
 * `TerminalViewModelTest.trivialSnapshot()` uses — [CellSnapshot.fromBuffer]
 * is `internal` to `:terminal-engine`, invisible across the module boundary.
 * Unlike that 1x1 fixture, this one accepts a full row/col grid of narrow
 * codepoints (0 = "never written", matching the real engine's padding
 * convention) plus per-row wrap flags, so exact-copy behavior can be
 * exercised against wrapped, blank-padded, and wide-glyph rows.
 */
internal fun buildSnapshot(
    cols: Int,
    rows: Int,
    rowFlags: ByteArray = ByteArray(rows),
    cell: (row: Int, col: Int) -> CellSnapshot.Cell,
): CellSnapshot {
    val cells = Array(cols * rows) { index ->
        val row = index / cols
        val col = index % cols
        cell(row, col)
    }
    val constructor = CellSnapshot::class.java.getDeclaredConstructor(
        Int::class.javaPrimitiveType,
        Int::class.javaPrimitiveType,
        Int::class.javaPrimitiveType,
        Int::class.javaPrimitiveType,
        Boolean::class.javaPrimitiveType,
        Boolean::class.javaPrimitiveType,
        Boolean::class.javaPrimitiveType,
        ByteArray::class.java,
        Array<CellSnapshot.Cell>::class.java,
    )
    constructor.isAccessible = true
    return constructor.newInstance(cols, rows, 0, 0, false, false, false, rowFlags, cells)
}

internal fun narrowCell(codepoint: Int): CellSnapshot.Cell = CellSnapshot.Cell(
    codepoint = codepoint,
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

internal fun wideCell(codepoint: Int): CellSnapshot.Cell = narrowCell(codepoint).copy(wide = CellSnapshot.Wide.WIDE)
/** The tail of a wide character: it carries no codepoint of its own. */
internal fun spacerTailCell(): CellSnapshot.Cell = narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_TAIL)
