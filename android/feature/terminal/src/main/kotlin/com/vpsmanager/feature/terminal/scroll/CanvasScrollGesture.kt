package com.vpsmanager.feature.terminal.scroll

import com.vpsmanager.feature.terminal.transport.TerminalDiag
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
 * Destination of a VERTICAL DRAG on the grid — the fourth gesture of the same
 * finger, alongside the short tap, the long press with selection and the tap in
 * mouse mode.
 */
interface CanvasScrollTarget {

    /** The gesture has been claimed: from here on the finger is scrolling. */
    fun onScrollStart()

    /**
     * Scrolled [deltaPx] pixels since the previous event — positive when the
     * finger moves down (and therefore the content shows the PAST).
     *
     * @return whether there is anywhere left to scroll. `false` ends the inertia
     *   at once, instead of letting it grind against the end of the history.
     */
    fun onScroll(deltaPx: Float, position: Offset): Boolean

    /** The finger has left and the inertia (if any) has finished. */
    fun onScrollEnd()
}

/**
 * Recognises the vertical drag on the grid and hands it to [target], with
 * inertia.
 *
 * ## The contest with the other three gestures
 *
 * This detector follows the same discipline as [canvasTapGesture][com.vpsmanager.feature.terminal.selection.canvasTapGesture]:
 * **giving up is the default, consuming is the exception**. It consumes nothing
 * until it is certain the gesture is its own, and that certainty has three
 * conditions, all mandatory:
 *
 * 1. the finger moved further than `touchSlop` — below that it could still be a
 *    tap;
 * 2. it moved **further vertically than horizontally** — a horizontal drag is
 *    not ours and is abandoned without touching anything;
 * 3. this happened **before** `longPressTimeoutMillis` — held still beyond that,
 *    the gesture belongs to long-press selection, and we leave the field.
 *
 * With all three met, only then does it consume the following events. That is
 * what makes the tap detector give up (it stops at the first `isConsumed`)
 * without either of the two needing to know about the other.
 *
 * In the other direction the coexistence is automatic: selection's
 * `detectDragGesturesAfterLongPress` cancels itself when the finger crosses the
 * slop before the long press fires — which is exactly the window in which this
 * gesture claims itself. A quick drag is ours; a drag after holding belongs to
 * selection. Neither needed a referee.
 */
/** How many times the gesture block has been (re)started in this run of the app. */
private val REINICIOS = java.util.concurrent.atomic.AtomicInteger(0)

fun Modifier.canvasScrollGesture(target: CanvasScrollTarget): Modifier = pointerInput(target) {
    // INSTRUMENTATION. The operator reports that dragging the history "only
    // unlocks once the keyboard appears for the first time". The missing-focus
    // hypothesis has been ruled out (a release addressing it changed nothing,
    // and the view does enter focused). Three suspicions remain, and none of
    // them can be settled by eye:
    //   1. the AndroidView on top consumes the gesture until some state changes;
    //   2. this `pointerInput` restarts on every recomposition — and a block
    //      that restarts LOSES the gesture in flight, which would give exactly
    //      this symptom;
    //   3. the target depends on state that only exists after the first layout.
    // The counter below separates (2) from the others: if the number climbs on
    // every frame before the keyboard and settles afterwards, it is (2). The
    // `down` says whether the event even REACHES here, which separates (1) from
    // (3).
    val geracao = REINICIOS.incrementAndGet()
    TerminalDiag.log("scroll: pointerInput INICIADO geracao=$geracao")

    val slop = viewConfiguration.touchSlop
    val limiteToqueLongoMillis = viewConfiguration.longPressTimeoutMillis
    val densidade: Density = this

    coroutineScope {
        var inercia: Job? = null

        awaitEachGesture {
            // The `awaitFirstDown` comes BEFORE cancelling the inertia, and
            // the order is the fix for a real defect: `awaitEachGesture`
            // restarts this block as soon as the previous gesture ends, without
            // waiting for any finger at all. Cancelling at the top killed the
            // freshly launched inertia in the very instant it was born — the
            // fling never happened, and the gesture was not even ended. Measured
            // on the emulator
            // (`arrasteVertical_encerraOGestoAoLevantarODedo`).
            val down = awaitFirstDown(requireUnconsumed = false)
            TerminalDiag.log(
                "scroll: DOWN geracao=$geracao consumido=${down.isConsumed} " +
                    "pos=${down.position.x.toInt()},${down.position.y.toInt()}",
            )

            // Now it holds: a genuinely NEW finger on screen interrupts the
            // inertia, as in any Android list — without this, the tap meaning
            // "hold the page" would be ignored while it is still gliding.
            val deslizeEmCurso = inercia
            inercia = null
            if (deslizeEmCurso != null && deslizeEmCurso.isActive) {
                deslizeEmCurso.cancel()
                // The previous gesture died here, so this is where it ends:
                // the cancelled fling's `onScrollEnd` would never have run.
                target.onScrollEnd()
            }
            val rastreador = VelocityTracker()
            rastreador.addPosition(down.uptimeMillis, down.position)

            var percorrido = Offset.Zero

            // Phase 1 — not ours yet. Nothing gets consumed here.
            val reivindicado = withTimeoutOrNull(limiteToqueLongoMillis) {
                while (true) {
                    val evento = awaitPointerEvent()
                    val change = evento.changes.firstOrNull { it.id == down.id }
                        ?: return@withTimeoutOrNull false
                    // Someone decided first (the selection handles consume):
                    // the gesture is theirs.
                    if (change.isConsumed) return@withTimeoutOrNull false
                    if (!change.pressed) return@withTimeoutOrNull false
                    percorrido += change.positionChange()
                    rastreador.addPosition(change.uptimeMillis, change.position)
                    if (percorrido.getDistance() > slop) {
                        // Horizontal is not ours: leave without touching anything.
                        val vertical = abs(percorrido.y) > abs(percorrido.x)
                        TerminalDiag.log(
                            "scroll: passou o slop geracao=$geracao vertical=$vertical " +
                                "dx=${percorrido.x.toInt()} dy=${percorrido.y.toInt()}",
                        )
                        return@withTimeoutOrNull vertical
                    }
                }
                @Suppress("UNREACHABLE_CODE")
                false
            } == true

            if (!reivindicado) return@awaitEachGesture

            target.onScrollStart()
            // The displacement up to this point is not lost: it is already scroll.
            var temParaOnde = target.onScroll(percorrido.y, down.position)

            // Phase 2 — now it is ours, and that is why we consume.
            var ultimaPosicao = down.position
            while (true) {
                val evento = awaitPointerEvent()
                val change = evento.changes.firstOrNull { it.id == down.id } ?: break
                ultimaPosicao = change.position
                if (!change.pressed) {
                    change.consume()
                    break
                }
                rastreador.addPosition(change.uptimeMillis, change.position)
                val delta = change.positionChange().y
                if (temParaOnde || delta != 0f) {
                    temParaOnde = target.onScroll(delta, change.position)
                }
                change.consume()
            }

            val velocidade = rastreador.calculateVelocity().y
            if (temParaOnde && abs(velocidade) > VELOCIDADE_MINIMA_INERCIA_PX_S) {
                val posicaoFinal = ultimaPosicao
                inercia = launch {
                    deslizar(velocidade, densidade, posicaoFinal, target)
                    target.onScrollEnd()
                }
            } else {
                target.onScrollEnd()
            }
        }
    }
}

/**
 * The inertia — the "fling" of any Android list. It uses the system's own
 * deceleration curve ([splineBasedDecay]) so that terminal scrolling does not
 * feel like it came from another device.
 */
private suspend fun deslizar(
    velocidadeInicial: Float,
    densidade: Density,
    posicao: Offset,
    target: CanvasScrollTarget,
) {
    val curva = splineBasedDecay<Float>(densidade)
    var anterior = 0f
    AnimationState(initialValue = 0f, initialVelocity = velocidadeInicial)
        .animateDecay(curva) {
            val delta = value - anterior
            anterior = value
            // Reached the end of the history: stop now, rather than grinding
            // against the wall until the curve runs out on its own.
            if (!target.onScroll(delta, posicao)) cancelAnimation()
        }
}

/**
 * Below this the finger was practically still on lift-off, and gliding would be
 * motion the owner never asked for.
 */
private const val VELOCIDADE_MINIMA_INERCIA_PX_S = 50f
