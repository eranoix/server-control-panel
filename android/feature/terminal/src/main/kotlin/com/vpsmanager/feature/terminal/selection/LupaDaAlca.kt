package com.vpsmanager.feature.terminal.selection

import android.view.View
import android.widget.Magnifier

/**
 * The magnifier that appears while a selection handle is being dragged — the
 * **system's own**, not one drawn by us.
 *
 * ## Why it is needed here more than in a text field
 *
 * In a terminal grid a cell is a few pixels wide and a finger covers a dozen
 * of them. Without a magnifier, placing the handle on the right column is
 * guesswork: you let go, check what ended up selected, get it wrong, and start
 * again. It is the same problem `TextView` solved long ago — and in the same
 * way.
 *
 * ## Why `android.widget.Magnifier`, and not a magnifier of our own
 *
 * Because it is the device's. `Magnifier` copies pixels from the window's
 * surface and draws them with the shape, the shadow, the zoom and the
 * animation **the manufacturer defined** — on One UI, Samsung's magnifier; on
 * AOSP, the AOSP one. A magnifier drawn by us would be a foreign object in the
 * middle of gestures a person already knows, and it would have to reimplement
 * clipping, elevation and the give-and-take at the edge of the screen. The
 * plain constructor here is deliberate: going through `Magnifier.Builder` with
 * sizes of our own would override the very manufacturer appearance we want to
 * preserve.
 *
 * ## Sticking to the row
 *
 * The source Y is **pinned to the centre of the cell**, never to the finger.
 * Two reasons, both stolen from `TextView`'s behaviour: the magnifier stops
 * shaking vertically while the finger wobbles, and it shows a whole row
 * instead of half a row from above and half from below. Whoever is dragging
 * needs to read the line, not the pixel. X follows the finger, because X is
 * what is being chosen.
 *
 * @param host the `View` whose window will be magnified. It has to be the one
 *   containing the drawn grid — the magnifier copies from the SURFACE, so a
 *   sibling view would magnify the right place on the wrong screen.
 */
class LupaDaAlca(private val host: View) {

    private var lupa: Magnifier? = null

    /**
     * Shows (or repositions) the magnifier over [xNaHost], with Y pinned to
     * the centre of the row at [yDaLinha].
     *
     * Calling it again during the drag repositions it — `show` is idempotent
     * in that sense, and it is how `TextView` itself follows the finger.
     */
    fun mostrar(xNaHost: Float, yDaLinha: Float) {
        // The view has to be alive and attached to a window: the Magnifier
        // copies from the surface, and with no window there is no surface to
        // copy from. It really does happen — a drag can outlive the screen by
        // a frame.
        if (!host.isAttachedToWindow) return
        val atual = lupa ?: Magnifier(host).also { lupa = it }
        atual.show(xNaHost, yDaLinha)
    }

    /**
     * Takes the magnifier off the screen.
     *
     * Idempotent on purpose: it is called at the end of the drag AND when the
     * screen is disposed, and both can happen in the same sequence of events.
     */
    fun esconder() {
        lupa?.dismiss()
    }

    /** Releases the resource when the screen dies — `dismiss` is not enough. */
    fun descartar() {
        lupa?.dismiss()
        lupa = null
    }
}
