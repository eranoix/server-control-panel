package com.vpsmanager.feature.terminal.render

import android.graphics.Canvas
import com.vpsmanager.terminalengine.CellSnapshot

/**
 * Draws a WHOLE terminal frame: paints the default background over the entire
 * area and rasterises EVERY row, every time.
 *
 * "Every time" is the contract, and it is not negotiable. Both Compose's
 * `Canvas` and `SurfaceView` hand you a surface that has to be repainted from
 * scratch on each draw pass — there is no persistent buffer where what was
 * drawn in the previous frame is still sitting. There used to be a "dirty row"
 * optimisation here (remembering the cells drawn per row and skipping a row
 * whose content had not changed since the last frame) and it was silently
 * wrong: since the first thing each pass does is erase everything with the
 * background, skipping a row did not preserve it — it erased it. In practice
 * the only row left on screen was the one that had changed in that frame,
 * almost always just the cursor's. That is why the terminal showed the prompt
 * and none of the previous command's output.
 *
 * Per-row diffing would only make sense with a buffer that survives between
 * frames; without one, the frame has to be a pure function of the snapshot.
 * This function is that pure function, and it is the app's only frame-drawing
 * path — [TerminalCanvas] and [TerminalSurfaceGrid] call this same
 * implementation.
 *
 * [cellAt] takes `(x, y)` in grid coordinates. It is a lambda rather than a
 * [CellSnapshot] on purpose: [CellSnapshot] has a private constructor and is
 * only born in the native engine, so a JVM test could never build one — with
 * the lambda, whole-frame drawing can be exercised on the JVM.
 */
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
