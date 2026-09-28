package dev.servercontrolpanel.feature.terminal.selection

import dev.servercontrolpanel.terminalengine.CellSnapshot

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
internal fun spacerTailCell(): CellSnapshot.Cell = narrowCell(0).copy(wide = CellSnapshot.Wide.SPACER_TAIL)
