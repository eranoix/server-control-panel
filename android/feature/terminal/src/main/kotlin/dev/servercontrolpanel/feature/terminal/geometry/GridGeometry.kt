package dev.servercontrolpanel.feature.terminal.geometry

/**
 * The terminal grid arithmetic: columns, rows, and how much of the grid the
 * keyboard covers.
 *
 * Raising the keyboard shrinks the grid node (`AppNavHost` applies `imePadding()`),
 * and deriving rows from the current height turned every animation frame into a
 * `SIGWINCH` (ten resizes per keyboard open). Differential renderers such as Ink
 * repaint the whole frame on each resize using `ESC[nA`, which stops at the top of
 * the screen, so frames taller than the screen leave duplicate copies in the
 * scrollback.
 *
 * Keeping the tallest height ever seen is not an option: navigation transitions
 * produce spurious measurements that a maximum keeps forever, and real shrinks
 * (banner, composition strip, attachment bar) must be followed. So
 * [heightWithoutKeyboard] adds back exactly what `imePadding()` removed, with no memory.
 */
object GridGeometry {

    /**
     * The height the grid would have with the keyboard closed.
     *
     * In `AppNavHost` the NavHost has `.padding(innerPadding)` (bottom = navigation
     * bar), `.consumeWindowInsets(innerPadding)` and `.imePadding()`, which applies
     * the IME inset minus what was consumed. So the subtree lost exactly
     * `max(0, ime - navBar)`, and adding that back is exact. With the keyboard
     * closed this is the identity.
     *
     * @param availableHeightPx the height the layout just offered the grid.
     * @param imePx the IME bottom inset, including the navigation bar area.
     * @param navBarPx the navigation bar bottom inset.
     */
    fun heightWithoutKeyboard(availableHeightPx: Int, imePx: Int, navBarPx: Int): Int =
        availableHeightPx + (imePx - navBarPx).coerceAtLeast(0)

    /**
     * How many grid pixels the keyboard covers (what [heightWithoutKeyboard] adds
     * back). Used to anchor the grid by its bottom so the cursor line stays visible.
     */
    fun coveredByKeyboard(imePx: Int, navBarPx: Int): Int =
        (imePx - navBarPx).coerceAtLeast(0)

    /** Never zero: a degenerate window still has to yield a valid grid. */
    fun columns(widthPx: Int, cellWidthPx: Int): Int {
        require(cellWidthPx > 0) { "cellWidthPx must be positive, got $cellWidthPx" }
        return (widthPx / cellWidthPx).coerceAtLeast(1)
    }

    /** See [columns] on the floor of 1. */
    fun lines(heightPx: Int, cellHeightPx: Int): Int {
        require(cellHeightPx > 0) { "cellHeightPx must be positive, got $cellHeightPx" }
        return (heightPx / cellHeightPx).coerceAtLeast(1)
    }
}
