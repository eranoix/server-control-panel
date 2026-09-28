package dev.servercontrolpanel.feature.terminal.render

import android.graphics.Canvas
import dev.servercontrolpanel.terminalengine.CellSnapshot

internal fun rasterizeFrame(
    canvas: Canvas,
    cols: Int,
    rows: Int,
    cellAt: (Int, Int) -> CellSnapshot.Cell,
    cellWidthPx: Float,
    cellHeightPx: Float,
    glyphAtlas: GlyphAtlas,
    defaultFg: Int,
    defaultBg: Int,
    minLumaDelta: Int = 0,
) {
    canvas.drawColor((0xff shl 24) or (defaultBg and 0x00ffffff))
    for (y in 0 until rows) {
        val cells = (0 until cols).map { x -> cellAt(x, y) }
        val ops = buildRowDrawOps(cells, defaultFg, defaultBg, minLumaDelta)
        rasterizeRow(canvas, ops, y * cellHeightPx, cellWidthPx, cellHeightPx, glyphAtlas, defaultBg)
    }
}
