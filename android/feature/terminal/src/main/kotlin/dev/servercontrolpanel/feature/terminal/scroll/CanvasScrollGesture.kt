package dev.servercontrolpanel.feature.terminal.scroll

import dev.servercontrolpanel.feature.terminal.transport.TerminalDiag
import androidx.compose.animation.core.AnimationState
import androidx.compose.animation.core.animateDecay
import androidx.compose.animation.splineBasedDecay
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.unit.Density
import kotlin.math.abs
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

interface CanvasScrollTarget {

    fun onScrollStart()

    fun onScroll(deltaPx: Float, position: Offset): Boolean

    fun onScrollEnd()
}

private val RESTARTS = java.util.concurrent.atomic.AtomicInteger(0)

fun Modifier.canvasScrollGesture(target: CanvasScrollTarget): Modifier = pointerInput(target) {
    val generation = RESTARTS.incrementAndGet()
    TerminalDiag.log("scroll: pointerInput STARTED generation=$generation")

    val slop = viewConfiguration.touchSlop
    val longPressTimeoutMillis = viewConfiguration.longPressTimeoutMillis
    val density: Density = this

    coroutineScope {
        var flingJob: Job? = null

        awaitEachGesture {
            val down = awaitFirstDown(requireUnconsumed = false)
            TerminalDiag.log(
                "scroll: DOWN generation=$generation consumed=${down.isConsumed} " +
                    "pos=${down.position.x.toInt()},${down.position.y.toInt()}",
            )

            val flingInProgress = flingJob
            flingJob = null
            if (flingInProgress != null && flingInProgress.isActive) {
                flingInProgress.cancel()
                target.onScrollEnd()
            }
            val tracker = VelocityTracker()
            tracker.addPosition(down.uptimeMillis, down.position)

            var traveled = Offset.Zero

            val claimed = withTimeoutOrNull(longPressTimeoutMillis) {
                while (true) {
                    val event = awaitPointerEvent()
                    val change = event.changes.firstOrNull { it.id == down.id }
                        ?: return@withTimeoutOrNull false
                    if (change.isConsumed) return@withTimeoutOrNull false
                    if (!change.pressed) return@withTimeoutOrNull false
                    traveled += change.positionChange()
                    tracker.addPosition(change.uptimeMillis, change.position)
                    if (traveled.getDistance() > slop) {
                        val vertical = abs(traveled.y) > abs(traveled.x)
                        TerminalDiag.log(
                            "scroll: past slop generation=$generation vertical=$vertical " +
                                "dx=${traveled.x.toInt()} dy=${traveled.y.toInt()}",
                        )
                        return@withTimeoutOrNull vertical
                    }
                }
                @Suppress("UNREACHABLE_CODE")
                false
            } == true

            if (!claimed) return@awaitEachGesture

            target.onScrollStart()
            var canMove = target.onScroll(traveled.y, down.position)

            var lastPosition = down.position
            while (true) {
                val event = awaitPointerEvent()
                val change = event.changes.firstOrNull { it.id == down.id } ?: break
                lastPosition = change.position
                if (!change.pressed) {
                    change.consume()
                    break
                }
                tracker.addPosition(change.uptimeMillis, change.position)
                val delta = change.positionChange().y
                if (canMove || delta != 0f) {
                    canMove = target.onScroll(delta, change.position)
                }
                change.consume()
            }

            val velocity = tracker.calculateVelocity().y
            if (canMove && abs(velocity) > MIN_FLING_VELOCITY_PX_S) {
                val finalPosition = lastPosition
                flingJob = launch {
                    fling(velocity, density, finalPosition, target)
                    target.onScrollEnd()
                }
            } else {
                target.onScrollEnd()
            }
        }
    }
}

private suspend fun fling(
    initialVelocity: Float,
    density: Density,
    position: Offset,
    target: CanvasScrollTarget,
) {
    val decay = splineBasedDecay<Float>(density)
    var previous = 0f
    AnimationState(initialValue = 0f, initialVelocity = initialVelocity)
        .animateDecay(decay) {
            val delta = value - previous
            previous = value
            if (!target.onScroll(delta, position)) cancelAnimation()
        }
}

private const val MIN_FLING_VELOCITY_PX_S = 50f
