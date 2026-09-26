package dev.servercontrolpanel.feature.jira

import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.unit.IntSize
import dev.servercontrolpanel.data.jira.JiraCard

/** Screen edge under the finger during a drag; touching it scrolls boards wider than the screen. */
internal enum class DragEdge { Left, Right }

/**
 * The card being dragged.
 *
 * Dragging starts only after a long press so it does not steal the column's vertical scroll or
 * the board's horizontal scroll. Positions are in root coordinates because the floating card is
 * drawn above everything, outside its column's clip.
 */
@Stable
internal class DragState {

    /** The card being dragged, or null. */
    var card by mutableStateOf<JiraCard?>(null)
        private set

    /** Column the card came from, so undo can put it back. */
    var sourceColumn by mutableStateOf<String?>(null)
        private set

    /** Top-left corner of the original card, in root coordinates. */
    var originInRoot by mutableStateOf(Offset.Zero)
        private set

    /** Finger travel since the card was picked up. */
    var offset by mutableStateOf(Offset.Zero)
        private set

    /** Size of the original card, matched by the floating one. */
    var size by mutableStateOf(IntSize.Zero)
        private set

    /** Width of the board area, used to compute the edges. */
    var rootWidth by mutableStateOf(0)

    /**
     * Horizontal extent of each column in root coordinates. Deliberately not observable:
     * [targetColumn] recomputes from the observable [offset].
     */
    private val bands = LinkedHashMap<String, ClosedFloatingPointRange<Float>>()

    fun registerColumn(label: String, start: Float, end: Float) {
        bands[label] = start..end
    }

    val dragging: Boolean get() = card != null

    /**
     * The column under the finger, or null. Reads the observable [offset], so callers in
     * composition recompose as the finger moves and the highlight follows.
     */
    fun targetColumn(): String? {
        if (!dragging) return null
        val x = centerX
        return bands.entries.firstOrNull { x in it.value }?.key
    }

    fun pick(card: JiraCard, column: String, origin: Offset, size: IntSize) {
        this.card = card
        this.sourceColumn = column
        this.originInRoot = origin
        this.size = size
        this.offset = Offset.Zero
    }

    fun drag(delta: Offset) {
        offset += delta
    }

    fun drop() {
        card = null
        sourceColumn = null
        offset = Offset.Zero
        size = IntSize.Zero
    }

    /** The horizontal centre of the floating card, in root coordinates. */
    val centerX: Float get() = originInRoot.x + offset.x + size.width / 2f

    /**
     * The edge the card is on, if any. The 18% band balances thumb precision against the board
     * scrolling on its own during a vertical move.
     */
    fun edge(): DragEdge? {
        if (!dragging || rootWidth <= 0) return null
        val band = rootWidth * 0.18f
        return when {
            centerX < band -> DragEdge.Left
            centerX > rootWidth - band -> DragEdge.Right
            else -> null
        }
    }
}

/**
 * Makes a card draggable after a long press.
 *
 * [onPick] fires when the card lifts (for haptic feedback confirming the pickup). [onDrop]
 * receives the card when the finger lifts. A false [enabled] disables the gesture, as in
 * multi-select mode.
 */
internal fun Modifier.draggable(
    state: DragState,
    card: JiraCard,
    column: String,
    enabled: Boolean,
    onPick: () -> Unit,
    onDrop: (JiraCard) -> Unit,
): Modifier {
    if (!enabled) return this
    var origin = Offset.Zero
    var size = IntSize.Zero
    return this
        .onGloballyPositioned { coords ->
            origin = coords.positionInRoot()
            size = coords.size
        }
        .pointerInput(card.key, column) {
            detectDragGesturesAfterLongPress(
                onDragStart = {
                    state.pick(card, column, origin, size)
                    onPick()
                },
                onDrag = { change, delta ->
                    change.consume()
                    state.drag(delta)
                },
                onDragEnd = {
                    val picked = state.card
                    state.drop()
                    if (picked != null) onDrop(picked)
                },
                // A system cancellation (incoming call, backgrounding) moves nothing.
                onDragCancel = { state.drop() },
            )
        }
}
