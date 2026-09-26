package com.vpsmanager.feature.terminal.render

import com.vpsmanager.terminalengine.CellSnapshot

/**
 * One glyph worth of drawing work for a single row, already resolved to
 * concrete colors (inverse/faint/invisible baked in) and a column position.
 *
 * [wide] cells occupy two grid columns; the atlas slot backing them is
 * rasterized at double cell width up front (see [GlyphAtlas]), so drawing a
 * wide glyph is exactly one draw call at one column position — there is no
 * per-frame stretching and nothing to double-draw.
 *
 * A [CellSnapshot.Wide.SPACER_TAIL] or [CellSnapshot.Wide.SPACER_HEAD] cell
 * never produces a [GlyphDrawOp] at all (see [buildRowDrawOps]): the spacer
 * exists purely so column arithmetic and cursor placement stay correct, it
 * carries no glyph of its own to draw.
 */
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

/**
 * Pure, host-testable draw-op builder for one terminal row.
 *
 * This is deliberately independent of [CellSnapshot] itself (which can only
 * be constructed by the native-backed [com.vpsmanager.terminalengine.TerminalEngine]
 * on a device) — it takes the row's cells as a plain list of the already-public
 * [CellSnapshot.Cell] data class, so [RowDrawOpsTest] can build synthetic rows
 * on the host JVM and assert the two silent-corruption risks called out for
 * this renderer directly, with no emulator involved:
 *
 * 1. A wide glyph's trailing [CellSnapshot.Wide.SPACER_TAIL] must never emit
 *    a second draw op (double-draw) and must never be skipped in a way that
 *    clips the wide glyph's own op.
 * 2. Column positions come only from index-in-row; wrap/wrap-continuation
 *    status must never shift them, so a continuation row's content lands at
 *    the exact same columns a fresh (non-continuation) row with identical
 *    cells would use — continuation rows are never re-indented.
 *
 * [defaultFg]/[defaultBg] are the resolved colors used when a cell leaves
 * `fg`/`bg` null (terminal default foreground/background), packed 0xRRGGBB.
 *
 * [minLumaDelta] is the light theme's legibility guard (see
 * [TerminalPalette]): the ANSI palette the emulator hands back is made for a
 * black background, so pure white/yellow/cyan vanish on a light one. Zero —
 * the dark theme's value, and the default — leaves every colour exactly as it
 * arrived.
 */
internal fun buildRowDrawOps(
    cells: List<CellSnapshot.Cell>,
    defaultFg: Int,
    defaultBg: Int,
    minLumaDelta: Int = 0,
): List<GlyphDrawOp> {
    val ops = ArrayList<GlyphDrawOp>(cells.size)
    for (column in cells.indices) {
        val cell = cells[column]
        // Spacer cells carry codepoint 0 and exist only to reserve column
        // width for the wide glyph that owns them; the WIDE cell itself is
        // the only one that ever gets a draw op.
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
        // The guard comes BEFORE the faint pass, never after. `faint` ASKS
        // for less contrast — it is how the remote program says "this is
        // secondary" — and a guard applied afterwards would undo exactly the
        // effect it asked for, leaving faint text as strong as normal text.
        // Before, the faint pass starts from a colour that is already legible
        // and arrives at something discreet AND visible; after, it would
        // become a colour with no purpose.
        fg = ajustaParaContraste(fg, bg, minLumaDelta)
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
