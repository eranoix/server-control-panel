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

internal enum class DragEdge { Left, Right }

@Stable
internal class DragState {

    var card by mutableStateOf<JiraCard?>(null)
        private set

    var sourceColumn by mutableStateOf<String?>(null)
        private set

    var originInRoot by mutableStateOf(Offset.Zero)
        private set

    var offset by mutableStateOf(Offset.Zero)
        private set

    var size by mutableStateOf(IntSize.Zero)
        private set

    var rootWidth by mutableStateOf(0)

    private val bands = LinkedHashMap<String, ClosedFloatingPointRange<Float>>()

    fun registerColumn(label: String, start: Float, end: Float) {
        bands[label] = start..end
    }

    val dragging: Boolean get() = card != null

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

    fun release(): Pair<JiraCard, String?>? {
        val picked = card ?: return null
        val target = targetColumn()
        drop()
        return picked to target
    }

    fun drop() {
        card = null
        sourceColumn = null
        offset = Offset.Zero
        size = IntSize.Zero
    }

    val centerX: Float get() = originInRoot.x + offset.x + size.width / 2f

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

internal fun Modifier.draggable(
    state: DragState,
    card: JiraCard,
    column: String,
    enabled: Boolean,
    onPick: () -> Unit,
    onDrop: (card: JiraCard, column: String) -> Unit,
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
                    state.release()?.let { (picked, target) ->
                        if (target != null) onDrop(picked, target)
                    }
                },
                onDragCancel = { state.drop() },
            )
        }
}
