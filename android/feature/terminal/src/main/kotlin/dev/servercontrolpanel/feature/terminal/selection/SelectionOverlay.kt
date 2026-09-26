package dev.servercontrolpanel.feature.terminal.selection

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
 * The handles' touch radius: 24 dp gives the 48 dp minimum target; the drawn
 * handle is smaller.
 */
private val HANDLE_TOUCH_RADIUS = 24.dp

/**
 * The system's own handle drawables (`android.R.attr.textSelectHandleLeft`/`Right`,
 * as `TextView` uses), so they match the device theme, including OEM themes.
 */
// `ResourceType`: the array is built from platform attributes, as
// `android.widget.Editor` does; no generated `styleable` exists for them.
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
 * The highlight is drawn over the glyphs in the theme's translucent
 * `textColorHighlight` rather than underneath, to keep the grid rasteriser (the
 * hot path) untouched. Handles follow AOSP's `getHorizontalOffset()`: the tip
 * sits on the anchor, offset by three quarters of the width for the start handle
 * and one quarter for the end one.
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

    // The magnifier copies the window surface, so the host must be the Compose
    // root that contains the drawn grid, not this layer.
    val composeRoot = LocalView.current
    val magnifier = remember(composeRoot) { HandleMagnifier(composeRoot) }
    DisposableEffect(magnifier) { onDispose { magnifier.discard() } }

    // Gesture coordinates are local to this layer; the magnifier needs root
    // coordinates, which differ when the grid does not start at the top.
    var coordinates by remember { mutableStateOf<LayoutCoordinates?>(null) }

    Canvas(
        modifier = modifier
            .fillMaxSize()
            .clipToBounds()
            .onGloballyPositioned { coordinates = it }
            .pointerInput(hitTesterProvider, radiusPx) {
                awaitEachGesture {
                    // Do not require "unconsumed": see the touch first and consume
                    // only once it is on a handle, or grid touches would be stolen.
                    val down = awaitFirstDown(requireUnconsumed = false)
                    val selection = selectionState.value ?: return@awaitEachGesture
                    val anchors = handleAnchors(selection, hitTesterProvider())
                    val handle = handleAt(down.position, anchors, radiusPx) ?: return@awaitEachGesture

                    // The gesture is ours: consuming every event makes the grid's
                    // tap and long-press drag recognisers give up.
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
 * Puts the magnifier over the cell being chosen, with Y at the cell centre (as in
 * `TextView`) so it does not tremble and shows a whole line. Does nothing until
 * [coordinates] are measured.
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
