package com.vpsmanager.feature.jira

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
import com.vpsmanager.data.jira.JiraCard

/**
 * The screen edge the finger is resting on during a drag.
 *
 * It exists because on a board with more columns than fit the screen,
 * dragging without it only reaches the neighbouring column: the destination
 * stays out of sight, and the thumb has no way to bring it in. Touching the
 * edge turns the board.
 */
internal enum class DragEdge { Left, Right }

/**
 * The card currently being carried by the finger.
 *
 * ## Why a LONG press, and never an immediate drag
 *
 * The column scrolls vertically and the board turns horizontally. A card that
 * starts moving at the first millimetre of finger travel steals both gestures:
 * the column locks up and the board will not turn. The long press separates
 * "I am reading" from "I am moving" with no ambiguity, and it is the same
 * contract every reorderable list on Android uses.
 *
 * ## Why the position is kept in ROOT coordinates
 *
 * The floating card is drawn over everything, outside the column it came
 * from — were it inside, it would vanish the moment it crossed the column's
 * bounds, which is exactly the movement that matters. Drawing on top requires
 * knowing where the card was on the whole screen, not where it was inside the
 * list.
 */
@Stable
internal class DragState {

    /** The card in flight, or null when nobody is dragging anything. */
    var card by mutableStateOf<JiraCard?>(null)
        private set

    /** Which column it left — so undo knows the way back. */
    var sourceColumn by mutableStateOf<String?>(null)
        private set

    /** Top-left corner of the original card, in root coordinates. */
    var originInRoot by mutableStateOf(Offset.Zero)
        private set

    /** How far the finger has travelled since it picked the card up. */
    var offset by mutableStateOf(Offset.Zero)
        private set

    /** Size of the original card — the floating one matches it. */
    var size by mutableStateOf(IntSize.Zero)
        private set

    /** Width of the board area, so we know what counts as an "edge". */
    var rootWidth by mutableStateOf(0)

    /**
     * Where each column starts and ends, in root coordinates.
     *
     * This is what makes the drag target OBVIOUS: with the three columns on
     * screen at the same time, the destination column is simply the one under
     * the finger. It is deliberately not an observable state map — what is
     * observed is [offset], and [targetColumn] recomputes from it.
     */
    private val bands = LinkedHashMap<String, ClosedFloatingPointRange<Float>>()

    fun registerColumn(label: String, start: Float, end: Float) {
        bands[label] = start..end
    }

    val dragging: Boolean get() = card != null

    /**
     * The column under the finger right now, or null if it is outside them
     * all.
     *
     * It reads [offset], which is observable state — so whoever calls
     * this inside a composition recomposes on every movement of the finger,
     * which is exactly what makes the column highlight follow the gesture.
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
     * Which edge the card is on, if it is on one at all.
     *
     * The band is 18% of the width on each side. Any narrower and the thumb
     * needs precision it does not have while holding a card; any wider and the
     * board turns by itself in the middle of a vertical movement.
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
 * Makes a card pickable by long press.
 *
 * [onPick] fires the instant the card is lifted — that is where the haptic
 * happens, because without it there is no way to know the card has been picked
 * up before moving the finger, and the person drags through thin air thinking
 * they are dragging.
 *
 * [onDrop] receives the card and decides where it goes; the screen answers
 * with whichever column is in view at the moment the finger lifts.
 *
 * A false [enabled] turns the whole gesture off — which is what happens
 * during multiple selection in marking mode, where the touch has another
 * owner.
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
                // A cancellation moves NOTHING: the system took the
                // gesture out of our hands (an incoming call, the app going to
                // the background), and moving in that case would mean acting
                // on a gesture that never finished.
                onDragCancel = { state.drop() },
            )
        }
}
