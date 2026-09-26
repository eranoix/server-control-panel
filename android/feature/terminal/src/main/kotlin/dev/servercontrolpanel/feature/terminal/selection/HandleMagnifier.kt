package dev.servercontrolpanel.feature.terminal.selection

import android.view.View
import android.widget.Magnifier

/**
 * The system magnifier shown while a selection handle is dragged; a finger covers
 * many grid cells, so without it picking a column is guesswork.
 *
 * Uses the plain `android.widget.Magnifier` constructor so the device keeps its
 * manufacturer look (a `Magnifier.Builder` with custom sizes would override it).
 * As in `TextView`, Y is pinned to the centre of the row so the magnifier does not
 * shake and shows a whole line; X follows the finger.
 *
 * @param host the `View` containing the drawn grid; the magnifier copies from its
 *   window surface.
 */
class HandleMagnifier(private val host: View) {

    private var magnifier: Magnifier? = null

    /**
     * Shows or repositions the magnifier over [xInHost], with Y pinned to the row
     * centre [rowY]. Called repeatedly during the drag, like `TextView` does.
     */
    fun show(xInHost: Float, rowY: Float) {
        // The Magnifier copies from the window surface; a drag can outlive the
        // window by a frame.
        if (!host.isAttachedToWindow) return
        val current = magnifier ?: Magnifier(host).also { magnifier = it }
        current.show(xInHost, rowY)
    }

    /** Hides the magnifier. Idempotent: called at drag end and on dispose, possibly both. */
    fun hide() {
        magnifier?.dismiss()
    }

    /** Releases the magnifier when the screen dies (`dismiss` alone is not enough). */
    fun discard() {
        magnifier?.dismiss()
        magnifier = null
    }
}
