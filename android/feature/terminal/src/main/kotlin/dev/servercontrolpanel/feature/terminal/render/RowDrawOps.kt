package dev.servercontrolpanel.feature.terminal.render

import dev.servercontrolpanel.terminalengine.CellSnapshot

internal data class GlyphDrawOp(
    val column: Int,
    val codepoint: Int,
    val fg: Int,
    val bg: Int,
    val bold: Boolean,
    val italic: Boolean,
    val underline: Int,
    val strikethrough: Boolean,
    val overline: Boolean,
    val wide: Boolean,
    val drawGlyph: Boolean,
)

internal fun buildRowDrawOps(
    cells: List<CellSnapshot.Cell>,
    defaultFg: Int,
    defaultBg: Int,
    minLumaDelta: Int = 0,
): List<GlyphDrawOp> {
    val ops = ArrayList<GlyphDrawOp>(cells.size)
    for (column in cells.indices) {
        val cell = cells[column]
        if (cell.wide == CellSnapshot.Wide.SPACER_TAIL || cell.wide == CellSnapshot.Wide.SPACER_HEAD) {
            continue
        }

        var fg = cell.fg ?: defaultFg
        var bg = cell.bg ?: defaultBg
        if (cell.inverse) {
            val tmp = fg
            fg = bg
            bg = tmp
        }
        fg = adjustForContrast(fg, bg, minLumaDelta)
        if (cell.faint) {
            fg = blend(fg, bg, 0.5f)
        }

        ops += GlyphDrawOp(
            column = column,
            codepoint = cell.codepoint,
            fg = fg,
            bg = bg,
            bold = cell.bold,
            italic = cell.italic,
            underline = cell.underline,
            strikethrough = cell.strikethrough,
            overline = cell.overline,
            wide = cell.wide == CellSnapshot.Wide.WIDE,
            drawGlyph = !cell.invisible && cell.codepoint != 0,
        )
    }
    return ops
}

private fun blend(a: Int, b: Int, t: Float): Int {
    val ar = (a shr 16) and 0xff
    val ag = (a shr 8) and 0xff
    val ab = a and 0xff
    val br = (b shr 16) and 0xff
    val bg = (b shr 8) and 0xff
    val bb = b and 0xff
    val r = (ar + (br - ar) * t).toInt().coerceIn(0, 255)
    val g = (ag + (bg - ag) * t).toInt().coerceIn(0, 255)
    val bl = (ab + (bb - ab) * t).toInt().coerceIn(0, 255)
    return (r shl 16) or (g shl 8) or bl
}
