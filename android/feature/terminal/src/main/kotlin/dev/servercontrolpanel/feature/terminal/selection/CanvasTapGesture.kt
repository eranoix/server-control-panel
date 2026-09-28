package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import dev.servercontrolpanel.feature.terminal.mouse.TouchRouting
import kotlinx.coroutines.withTimeoutOrNull

const val SINGLE_TAP = 1

const val DOUBLE_TAP = 2

const val TRIPLE_TAP = 3

fun interface CanvasTapTarget {
    fun onTap(position: Offset, taps: Int)
}

fun routeCanvasTap(
    routing: TouchRouting,
    keyboardTarget: CanvasTapTarget,
    mouseTarget: CanvasTapTarget,
): CanvasTapTarget = CanvasTapTarget { position, taps ->
    val target = if (routing.tapBelongsToApp()) keyboardTarget else mouseTarget
    target.onTap(position, taps)
}

fun Modifier.canvasTapGesture(target: CanvasTapTarget): Modifier = pointerInput(target) {
    val slop = viewConfiguration.touchSlop
    val longPressTimeoutMillis = viewConfiguration.longPressTimeoutMillis
    val doubleTapTimeoutMillis = viewConfiguration.doubleTapTimeoutMillis
    val tapSlopPx = slop * 3f

    var consecutiveTaps = 0
    var previousTapMillis = 0L
    var previousTapPosition = Offset.Zero

    awaitEachGesture {
        val down = awaitFirstDown(requireUnconsumed = false)
        var traveled = Offset.Zero
        val wasShortTap = withTimeoutOrNull(longPressTimeoutMillis) {
            while (true) {
                val event = awaitPointerEvent()
                val change = event.changes.firstOrNull { it.id == down.id } ?: return@withTimeoutOrNull false
                if (change.isConsumed) return@withTimeoutOrNull false
                traveled += change.positionChange()
                if (traveled.getDistance() > slop) return@withTimeoutOrNull false
                if (!change.pressed) return@withTimeoutOrNull true
            }
            @Suppress("UNREACHABLE_CODE")
            false
        }

        if (wasShortTap != true) {
            consecutiveTaps = 0
            return@awaitEachGesture
        }

        val nowMillis = down.uptimeMillis
        val withinTime = nowMillis - previousTapMillis <= doubleTapTimeoutMillis
        val withinPlace = (down.position - previousTapPosition).getDistance() <= tapSlopPx
        consecutiveTaps = if (consecutiveTaps > 0 && withinTime && withinPlace) {
            (consecutiveTaps + 1).coerceAtMost(TRIPLE_TAP)
        } else {
            SINGLE_TAP
        }
        previousTapMillis = nowMillis
        previousTapPosition = down.position

        target.onTap(down.position, consecutiveTaps)
    }
}
