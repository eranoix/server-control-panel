package com.vpsmanager.feature.terminal.render

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import com.vpsmanager.terminalengine.CellSnapshot
import kotlinx.coroutines.delay

/**
 * Compose Canvas renderer for a terminal grid. This composable's own body
 * never reads [snapshotState] or the cursor blink flag -- both are only
 * read inside the `Canvas { ... }` draw lambda, which Compose tracks as a
 * draw-phase snapshot read: a new terminal frame or a blink toggle
 * invalidates and repaints this Canvas without recomposing anything above
 * or below it in the tree.
 *
 * IME insets are intentionally not touched here (no `imePadding()`, no
 * `consumeWindowInsets` call): the single point that consumes them is
 * upstream, on the input-focused view (`TerminalInputView` / its
 * `WindowInsetsCompat` handling). Adding a second consumption point
 * here would double-apply the inset and is exactly the bug class that
 * centralizing it already avoided — this renderer only ever draws the
 * grid.
 *
 * @param deslocamentoYPx how many pixels of the frame are hidden ABOVE the
 *   visible area. It exists because the grid has a FIXED number of rows, tied
 *   to the unobstructed height of the window (see `TerminalRoute`): when the
 *   keyboard comes up the node shrinks but the grid does not — so the frame
 *   becomes taller than the area it has to fit into, and drawing it from the
 *   top would hide precisely the last rows, which is where the cursor is. With
 *   the offset, the drawing is anchored by its FOOT and the cursor stays
 *   visible just above the keyboard.
 *
 *   This is a DRAWING translation. No composable changes size because of it,
 *   and so it cannot feed back into measurement — which is exactly what broke
 *   the previous attempt, made with a fixed height plus a layout `offset`. The
 *   `clipToBounds` below guarantees that the excess is clipped away instead of
 *   spilling into the key bar.
 *
 * @param cellWidthPx/[cellHeightPx] fixed monospace cell metrics in pixels,
 *   computed once by the caller from the chosen text size (not from this
 *   file, so a resize/font-size change is a single upstream recomputation
 *   rather than something this renderer infers per frame).
 */
@Composable
fun TerminalCanvas(
    snapshotState: State<CellSnapshot?>,
    cellWidthPx: Float,
    cellHeightPx: Float,
    glyphAtlas: GlyphAtlas,
    modifier: Modifier = Modifier,
    palette: TerminalPalette = PaletaTerminalEscura,
    defaultFg: Int = palette.defaultFg,
    defaultBg: Int = palette.defaultBg,
) {
    val cursorBlinkOn = rememberCursorBlink()

    // `clipToBounds()` is NOT decoration. Compose does not clip drawing to the
    // node's bounds by default, and the first thing in every frame is a
    // `Canvas.drawColor` — which paints the WHOLE CLIP, not this node's
    // rectangle. Without the clip, the terminal background covered the entire
    // screen: the control rows above the grid and the key bar below it existed
    // in the composition, answered to touch, and showed up in not a single
    // pixel. The clip confines the clearing to the grid's area without
    // touching the contract of [rasterizeFrame] (draw the whole frame, always).
    Canvas(modifier.fillMaxSize().clipToBounds()) {
        val snapshot = snapshotState.value ?: return@Canvas
        val blinkOn = cursorBlinkOn.value

        // Anchoring by the foot (see [deslocamentoYPx]) wraps the WHOLE
        // frame — glyphs and cursor in the same translation. Separating the
        // two would be the easiest way for the cursor highlight to land on a
        // row that is not its own as soon as the keyboard came up.
        // The whole frame, every pass. See the KDoc of [rasterizeFrame] on why
        // there is no per-row cache here: the Compose surface is repainted
        // from scratch on every draw, so skipping an "unchanged" row does not
        // preserve it -- it erases it.
        drawIntoCanvas { canvas ->
            rasterizeFrame(
                canvas = canvas.nativeCanvas,
                cols = snapshot.cols,
                rows = snapshot.rows,
                cellAt = snapshot::cellAt,
                cellWidthPx = cellWidthPx,
                cellHeightPx = cellHeightPx,
                glyphAtlas = glyphAtlas,
                defaultFg = defaultFg,
                defaultBg = defaultBg,
                minLumaDelta = palette.minLumaDelta,
            )
        }

        desenharCursor(snapshot, blinkOn, cellWidthPx, cellHeightPx, palette)
    }
}

/**
 * The cursor highlight. Extracted from the body of [TerminalCanvas] once the
 * drawing started happening inside a translation: nesting one more level of
 * `if` inside the `translate` would leave the draw lambda four levels deep in
 * indentation, with the `cursorWideTail` rule unreadable in the middle of it.
 */
private fun DrawScope.desenharCursor(
    snapshot: CellSnapshot,
    blinkOn: Boolean,
    cellWidthPx: Float,
    cellHeightPx: Float,
    palette: TerminalPalette,
) {
    if (!snapshot.cursorVisible || !snapshot.cursorViewportValid || !blinkOn) return

    // cursorWideTail means the logical cursor column is the trailing half of a
    // wide glyph; the highlight box is drawn one column to the left so it still
    // covers the whole glyph rather than just its spacer half.
    val startCol = if (snapshot.cursorWideTail) snapshot.cursorX - 1 else snapshot.cursorX
    val cellAtCursor = snapshot.cellAt(snapshot.cursorX.coerceIn(0, snapshot.cols - 1), snapshot.cursorY)
    val widthCells = if (snapshot.cursorWideTail || cellAtCursor.wide == CellSnapshot.Wide.WIDE) 2 else 1
    drawRect(
        // Colour taken from the palette, NEVER a hard-coded white: a
        // translucent white block over the light background is invisible —
        // the cursor would simply cease to exist in the light theme.
        palette.cursor,
        topLeft = Offset(startCol * cellWidthPx, snapshot.cursorY * cellHeightPx),
        size = Size(cellWidthPx * widthCells, cellHeightPx),
    )
}

@Composable
private fun rememberCursorBlink(periodMillis: Long = 530): State<Boolean> {
    val blinkOn = remember { mutableStateOf(true) }
    LaunchedEffect(Unit) {
        while (true) {
            delay(periodMillis)
            blinkOn.value = !blinkOn.value
        }
    }
    return blinkOn
}
