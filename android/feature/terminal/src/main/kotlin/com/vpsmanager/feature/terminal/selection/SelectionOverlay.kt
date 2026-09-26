package com.vpsmanager.feature.terminal.selection

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.drawable.Drawable
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.State
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown

/**
 * The handles' touch target. 24 dp is the radius that yields the 48 dp
 * diameter Android's accessibility guidance demands of any control — the
 * DRAWN handle is smaller than that, and a handle that only responds exactly
 * on top of its drawing is, in practice, a handle that does not work.
 */
private val HANDLE_TOUCH_RADIUS = 24.dp

/**
 * The system's OWN two handle drawables, read from the device theme.
 *
 * They are not this app's icons: `android.R.attr.textSelectHandleLeft`/`Right`
 * are the same resources `TextView` uses, so the terminal's handles have the
 * shape, accent colour and size of the handles on any other text field on the
 * device — including under a manufacturer theme that replaced them.
 */
// `ResourceType`: lint wants a generated `R.styleable.*`, and here the array
// is assembled by hand from PLATFORM attributes — this is how
// `android.widget.Editor` itself looks up the selection handles, and no
// generated `styleable` exists for `android.R.attr` attributes. Suppressing is
// the right answer; changing the pattern would mean giving up the system
// handles.
@SuppressLint("ResourceType")
private class SystemHandles(context: Context) {
    val left: Drawable?
    val right: Drawable?
    val highlightColor: Color

    init {
        val attributes = context.theme.obtainStyledAttributes(
            intArrayOf(
                android.R.attr.textSelectHandleLeft,
                android.R.attr.textSelectHandleRight,
                android.R.attr.textColorHighlight,
            ),
        )
        try {
            left = attributes.getDrawable(0)
            right = attributes.getDrawable(1)
            val highlight = attributes.getColor(2, 0)
            highlightColor = if (highlight == 0) Color(0x6633B5E5) else Color(highlight)
        } finally {
            attributes.recycle()
        }
    }
}

/**
 * The selection highlight and the two draggable handles, laid over the grid.
 *
 * **This overlay is what was missing entirely.** Before, the selection existed
 * as data (`GridSelectionHolder`) and did not exist as an image: the
 * `TerminalCanvas` never drew any highlight, and there were no handles at all.
 * Selecting was a blind gesture — the operator dragged and only found out what
 * they had caught after pasting it somewhere else.
 *
 * The highlight is drawn OVER the glyphs, in the theme's translucent accent
 * colour (`android.R.attr.textColorHighlight`), rather than underneath as in a
 * `TextView`. Drawing underneath would mean touching the grid rasteriser,
 * which is the hot path of the frame; the system colour already carries alpha
 * and the text stays legible through it.
 *
 * The handles position themselves by Android's convention: the left one hangs
 * from the bottom-left edge of the first cell, the right one from the
 * bottom-right of the last, each offset so that its tip falls exactly on the
 * anchor (this is AOSP's `getHorizontalOffset()`: three quarters of the width
 * for the start handle, one quarter for the end one).
 */
@Composable
fun SelectionOverlay(
    selectionState: State<GridSelection?>,
    hitTesterProvider: () -> CellHitTester,
    onDragHandle: (SelectionHandle, Offset) -> Unit,
    onHandleDragEnd: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val handles = remember(context) { SystemHandles(context) }
    val radiusPx = with(LocalDensity.current) { HANDLE_TOUCH_RADIUS.toPx() }

    // The magnifier magnifies the window's SURFACE, so the host has to be the
    // view containing the drawn grid — the Compose root, not this layer.
    val composeRoot = LocalView.current
    val magnifier = remember(composeRoot) { HandleMagnifier(composeRoot) }
    DisposableEffect(magnifier) { onDispose { magnifier.discard() } }

    // The gesture's coordinates are local to this layer; the magnifier wants
    // them relative to the root. Without this conversion the magnifier
    // magnifies the wrong place as soon as the grid does not start at the top
    // of the window (with the key row open, for instance).
    var coordinates by remember { mutableStateOf<LayoutCoordinates?>(null) }

    Canvas(
        modifier = modifier
            .fillMaxSize()
            .clipToBounds()
            .onGloballyPositioned { coordinates = it }
            .pointerInput(hitTesterProvider, radiusPx) {
                awaitEachGesture {
                    // Never requires "unconsumed": this detector has to see
                    // the touch before deciding whether it is its own, and only
                    // then consumes. Consuming before knowing would steal from
                    // the grid every touch that was not on a handle.
                    val down = awaitFirstDown(requireUnconsumed = false)
                    val selection = selectionState.value ?: return@awaitEachGesture
                    val anchors = handleAnchors(selection, hitTesterProvider())
                    val handle = handleAt(down.position, anchors, radiusPx) ?: return@awaitEachGesture

                    // From here on the gesture is OURS. Consuming every event
                    // is what stops the grid's tap recogniser and its
                    // drag-after-long-press recogniser from acting on the same
                    // finger — both give up once they see the event consumed.
                    down.consume()
                    while (true) {
                        val event = awaitPointerEvent()
                        val change = event.changes.firstOrNull { it.id == down.id } ?: break
                        change.consume()
                        if (!change.pressed) break
                        onDragHandle(handle, change.position)
                        showMagnifier(magnifier, coordinates, hitTesterProvider(), change.position)
                    }
                    magnifier.hide()
                    onHandleDragEnd()
                }
            },
    ) {
        val selection = selectionState.value ?: return@Canvas
        val hitTester = hitTesterProvider()
        drawHighlight(selection, hitTester, handles.highlightColor)
        drawHandles(selection, hitTester, handles)
    }
}

private fun DrawScope.drawHighlight(
    selection: GridSelection,
    hitTester: CellHitTester,
    color: Color,
) {
    val ordered = inReadingOrder(selection)
    selectionRowRanges(selection, hitTester.cols).forEachIndexed { index, band ->
        val line = ordered.startRow + index
        val first = hitTester.cellRect(line, band.first)
        val last = hitTester.cellRect(line, band.last)
        drawRect(
            color = color,
            topLeft = Offset(first.left, first.top),
            size = Size(last.right - first.left, first.height),
        )
    }
}

private fun DrawScope.drawHandles(
    selection: GridSelection,
    hitTester: CellHitTester,
    handles: SystemHandles,
) {
    val anchors = handleAnchors(selection, hitTester)
    drawIntoCanvas { canvas ->
        handles.left?.let { drawing ->
            val width = drawing.intrinsicWidth
            val height = drawing.intrinsicHeight
            val left = (anchors.start.x - width * 3 / 4f).toInt()
            val top = anchors.start.y.toInt()
            drawing.setBounds(left, top, left + width, top + height)
            drawing.draw(canvas.nativeCanvas)
        }
        handles.right?.let { drawing ->
            val width = drawing.intrinsicWidth
            val height = drawing.intrinsicHeight
            val left = (anchors.end.x - width / 4f).toInt()
            val top = anchors.end.y.toInt()
            drawing.setBounds(left, top, left + width, top + height)
            drawing.draw(canvas.nativeCanvas)
        }
    }
}

/**
 * Puts the magnifier over the cell the finger is choosing.
 *
 * The Y goes to the CENTRE OF THE CELL, not to the finger: that is what stops
 * the magnifier trembling vertically as the hand wavers, and what makes it
 * show a whole line instead of half of one above and half of one below. Same
 * behaviour as `TextView`, and for the same reason — whoever is dragging needs
 * to read the line.
 *
 * With [coordinates] not yet measured there is no way to convert to the root,
 * and magnifying the wrong place is worse than not magnifying at all.
 */
private fun showMagnifier(
    magnifier: HandleMagnifier,
    coordinates: LayoutCoordinates?,
    hitTester: CellHitTester,
    position: Offset,
) {
    val coords = coordinates ?: return
    val cell = hitTester.hitTest(position)
    val rect = hitTester.cellRect(cell.row, cell.col)
    val inRoot = coords.localToRoot(Offset(position.x, rect.center.y))
    magnifier.show(inRoot.x, inRoot.y)
}
