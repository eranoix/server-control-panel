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

/**
 * Target of a vertical drag on the grid, one of the four gestures of the same
 * finger (tap, long-press selection, mouse-mode tap and this).
 */
interface CanvasScrollTarget {

    /** The gesture was claimed: from here on the finger is scrolling. */
    fun onScrollStart()

    /**
     * Scrolled [deltaPx] pixels since the previous event; positive when the finger
     * moves down (showing the past).
     *
     * @return whether there is room left to scroll. `false` stops the inertia at once.
     */
    fun onScroll(deltaPx: Float, position: Offset): Boolean

    /** The finger lifted and any inertia has finished. */
    fun onScrollEnd()
}

/** How many times the gesture block has been (re)started in this run of the app. */
private val RESTARTS = java.util.concurrent.atomic.AtomicInteger(0)

/**
 * Recognises a vertical drag on the grid and hands it to [target], with inertia.
 *
 * Like [canvasTapGesture][dev.servercontrolpanel.feature.terminal.selection.canvasTapGesture],
 * it consumes nothing until the gesture is certainly its own: the finger moved
 * past `touchSlop`, more vertically than horizontally, and before
 * `longPressTimeoutMillis`. Only then does it consume, which makes the tap
 * detector give up. Long-press selection cancels itself when the finger crosses
 * the slop early, so a quick drag scrolls and a drag after holding selects.
 */
fun Modifier.canvasScrollGesture(target: CanvasScrollTarget): Modifier = pointerInput(target) {
    // Diagnostics: a restart counter shows whether this `pointerInput` restarts
    // on recomposition (which would drop the gesture in flight), and the `down`
    // log shows whether events reach this detector at all.
    val generation = RESTARTS.incrementAndGet()
    TerminalDiag.log("scroll: pointerInput STARTED generation=$generation")

    val slop = viewConfiguration.touchSlop
    val longPressTimeoutMillis = viewConfiguration.longPressTimeoutMillis
    val density: Density = this

    coroutineScope {
        var flingJob: Job? = null

        awaitEachGesture {
            // Wait for the finger BEFORE cancelling inertia: `awaitEachGesture`
            // restarts this block right after the previous gesture ends, so
            // cancelling first would kill the fling that was just launched
            // (see `verticalDrag_endsGestureOnFingerUp`).
            val down = awaitFirstDown(requireUnconsumed = false)
            TerminalDiag.log(
                "scroll: DOWN generation=$generation consumed=${down.isConsumed} " +
                    "pos=${down.position.x.toInt()},${down.position.y.toInt()}",
            )

            // A new finger stops the inertia, as in any Android list.
            val flingInProgress = flingJob
            flingJob = null
            if (flingInProgress != null && flingInProgress.isActive) {
                flingInProgress.cancel()
                // The cancelled fling will never call `onScrollEnd`, so end it here.
                target.onScrollEnd()
            }
            val tracker = VelocityTracker()
            tracker.addPosition(down.uptimeMillis, down.position)

            var traveled = Offset.Zero

            // Phase 1: not ours yet, consume nothing.
            val claimed = withTimeoutOrNull(longPressTimeoutMillis) {
                while (true) {
                    val event = awaitPointerEvent()
                    val change = event.changes.firstOrNull { it.id == down.id }
                        ?: return@withTimeoutOrNull false
                    // Someone else claimed it first (e.g. selection handles).
                    if (change.isConsumed) return@withTimeoutOrNull false
                    if (!change.pressed) return@withTimeoutOrNull false
                    traveled += change.positionChange()
                    tracker.addPosition(change.uptimeMillis, change.position)
                    if (traveled.getDistance() > slop) {
                        // Horizontal is not ours: leave without touching anything.
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
            // The movement so far already counts as scroll.
            var canMove = target.onScroll(traveled.y, down.position)

            // Phase 2: the gesture is ours, so consume.
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

/**
 * Inertia ("fling") using the system deceleration curve ([splineBasedDecay]),
 * so scrolling feels like any Android list.
 */
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
            // End of history: stop now instead of grinding against the wall.
            if (!target.onScroll(delta, position)) cancelAnimation()
        }
}

/** Below this the finger was practically still on lift-off, so no inertia. */
private const val MIN_FLING_VELOCITY_PX_S = 50f
