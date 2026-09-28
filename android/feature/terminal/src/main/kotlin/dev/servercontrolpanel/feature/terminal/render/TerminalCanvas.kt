package dev.servercontrolpanel.feature.terminal.render

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
import dev.servercontrolpanel.terminalengine.CellSnapshot
import kotlinx.coroutines.delay

@Composable
fun TerminalCanvas(
    snapshotState: State<CellSnapshot?>,
    cellWidthPx: Float,
    cellHeightPx: Float,
    glyphAtlas: GlyphAtlas,
    modifier: Modifier = Modifier,
    palette: TerminalPalette = DarkTerminalPalette,
    defaultFg: Int = palette.defaultFg,
    defaultBg: Int = palette.defaultBg,
) {
    val cursorBlinkOn = rememberCursorBlink()

    Canvas(modifier.fillMaxSize().clipToBounds()) {
        val snapshot = snapshotState.value ?: return@Canvas
        val blinkOn = cursorBlinkOn.value

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

        drawCursor(snapshot, blinkOn, cellWidthPx, cellHeightPx, palette)
    }
}

private fun DrawScope.drawCursor(
    snapshot: CellSnapshot,
    blinkOn: Boolean,
    cellWidthPx: Float,
    cellHeightPx: Float,
    palette: TerminalPalette,
) {
    if (!snapshot.cursorVisible || !snapshot.cursorViewportValid || !blinkOn) return

    val startCol = if (snapshot.cursorWideTail) snapshot.cursorX - 1 else snapshot.cursorX
    val cellAtCursor = snapshot.cellAt(snapshot.cursorX.coerceIn(0, snapshot.cols - 1), snapshot.cursorY)
    val widthCells = if (snapshot.cursorWideTail || cellAtCursor.wide == CellSnapshot.Wide.WIDE) 2 else 1
    drawRect(
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
