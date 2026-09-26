package com.vpsmanager.feature.terminal.selection

import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import com.vpsmanager.feature.terminal.mouse.RoteamentoDeToque
import kotlinx.coroutines.withTimeoutOrNull

/** A single tap. */
const val TOQUE_SIMPLES = 1

/** Double tap — in terminal idiom, selects the WORD under the finger. */
const val TOQUE_DUPLO = 2

/** Triple tap — selects the whole logical LINE. */
const val TOQUE_TRIPLO = 3

/**
 * Exclusive destination of a SHORT TAP on the grid — the sibling of
 * [CanvasDragTarget] for the other gesture the same finger can mean.
 *
 * [taps] is how many consecutive short taps, in the same place, formed this
 * gesture: 1, 2 ([TOQUE_DUPLO], word) or 3 ([TOQUE_TRIPLO], line). The detector
 * delivers EVERY tap in the sequence as it happens — number 1 goes out at once,
 * and only afterwards does number 2 follow if there is one. That is deliberate:
 * holding the first tap back until we know whether a second is coming would cost
 * ~300 ms of latency on every tap, including the click destined for `htop`,
 * where the immediate response is the whole point.
 */
fun interface CanvasTapTarget {
    fun onTap(position: Offset, taps: Int)
}

/**
 * Routes a short tap to exactly one of [keyboardTarget] or [mouseTarget],
 * decided by [roteamento] at the instant of the tap.
 *
 * **The criterion changed its source.** It used to be a manual switch in the
 * app; today it is the REAL state of the terminal — the remote program either
 * asked for mouse tracking (DECSET 1000/1002/1003) or it did not. No preference
 * survives on top of that: see [RoteamentoDeToque] on why the local switch was
 * wrong by construction and why the preference that succeeded it went too.
 *
 * **Why the tap does NOT raise the keyboard when the mouse belongs to the
 * program.** The operator is inside an `htop`/`vim`/`less` that treats a click
 * as a command: a tap there means "click this cell", not "I want to type".
 * Raising the keyboard in that mode would steal the gesture from the remote
 * program and eat half the screen it is using as well. It is the same decision
 * Termux makes (`TerminalView.onSingleTapUp`: with `isMouseTrackingActive()` the
 * tap becomes a mouse event and the keyboard does NOT come up). The route to the
 * keyboard in that case is explicit and off the grid: the "Show keyboard" button
 * in the options sheet.
 */
fun routeCanvasTap(
    roteamento: RoteamentoDeToque,
    keyboardTarget: CanvasTapTarget,
    mouseTarget: CanvasTapTarget,
): CanvasTapTarget = CanvasTapTarget { position, taps ->
    val target = if (roteamento.toqueEDoApp()) keyboardTarget else mouseTarget
    target.onTap(position, taps)
}

/**
 * Recognises the short tap on the grid — single, double or triple — and hands it
 * to [target].
 *
 * **This detector consumes NOTHING** — not the `down`, not the moves, not the
 * `up`. That is deliberate, and it is what lets it coexist with
 * [canvasDragGestures] on the same `Modifier` without regressing long-press
 * selection (the instrumented `SelectionComposeIndependenceTest` proves that
 * gesture under a real touch). Compose's `detectTapGestures` would NOT do here:
 * it consumes the `down` and, when the tap turns long, calls `consumeUntilUp()`
 * — which would eat exactly the events
 * `detectDragGesturesAfterLongPress` needs in order to drag the selection. It is
 * also why the tap count is done by hand here, rather than coming from Compose's
 * `onDoubleTap`.
 *
 * Disambiguation is entirely by giving up, never by consuming: a touch only
 * counts as a tap if the finger lifts BEFORE `longPressTimeoutMillis` and
 * without travelling further than `touchSlop`. Past the time, moved too far, or
 * somebody consumed the event (the selection drag consumes its own, and so do
 * the selection handles) — this detector simply fires nothing, and the gesture
 * belongs entirely to the other recogniser.
 */
fun Modifier.canvasTapGesture(target: CanvasTapTarget): Modifier = pointerInput(target) {
    val slop = viewConfiguration.touchSlop
    val limiteToqueLongoMillis = viewConfiguration.longPressTimeoutMillis
    val limiteToqueDuploMillis = viewConfiguration.doubleTapTimeoutMillis
    // Android requires the taps of a sequence to land NEAR one another; two
    // quick taps in opposite corners of the screen are two single taps.
    val folgaEntreToquesPx = slop * 3f

    var toquesSeguidos = 0
    var instanteDoToqueAnteriorMillis = 0L
    var posicaoDoToqueAnterior = Offset.Zero

    awaitEachGesture {
        // `requireUnconsumed = false`: the same `down` is observed by both
        // detectors, and neither has the right to exclude the other before the
        // gesture has made up its mind.
        val down = awaitFirstDown(requireUnconsumed = false)
        var percorrido = Offset.Zero
        val foiToqueCurto = withTimeoutOrNull(limiteToqueLongoMillis) {
            while (true) {
                val evento = awaitPointerEvent()
                val change = evento.changes.firstOrNull { it.id == down.id } ?: return@withTimeoutOrNull false
                if (change.isConsumed) return@withTimeoutOrNull false
                percorrido += change.positionChange()
                if (percorrido.getDistance() > slop) return@withTimeoutOrNull false
                if (!change.pressed) return@withTimeoutOrNull true
            }
            @Suppress("UNREACHABLE_CODE")
            false
        }

        if (foiToqueCurto != true) {
            // A long press, a drag or a stolen gesture breaks the sequence:
            // the next short tap starts again from 1.
            toquesSeguidos = 0
            return@awaitEachGesture
        }

        // The clock is the EVENT'S own (`uptimeMillis`), not the system's:
        // it is the only one the virtual clock of the gesture tests also moves.
        val agoraMillis = down.uptimeMillis
        val dentroDoTempo = agoraMillis - instanteDoToqueAnteriorMillis <= limiteToqueDuploMillis
        val dentroDoLugar = (down.position - posicaoDoToqueAnterior).getDistance() <= folgaEntreToquesPx
        toquesSeguidos = if (toquesSeguidos > 0 && dentroDoTempo && dentroDoLugar) {
            // Above three there is no gesture defined in terminal idiom, and
            // letting the counter grow would only create meaningless states.
            (toquesSeguidos + 1).coerceAtMost(TOQUE_TRIPLO)
        } else {
            TOQUE_SIMPLES
        }
        instanteDoToqueAnteriorMillis = agoraMillis
        posicaoDoToqueAnterior = down.position

        target.onTap(down.position, toquesSeguidos)
    }
}
