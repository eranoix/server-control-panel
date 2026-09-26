package com.vpsmanager.feature.terminal.geometria

/**
 * The terminal grid's arithmetic: how many columns and rows it has, and how
 * much of the keyboard is covering the area where it is drawn.
 *
 * ## The defect this arithmetic exists to prevent
 *
 * Raising the on-screen keyboard shrinks the grid node — `AppNavHost`'s
 * `imePadding()` subtracts the IME band from the whole subtree. While the row
 * count was derived from the CURRENT height, every frame of the slide
 * animation became a new grid size, and each of those a `SIGWINCH` on the
 * server. Measured in the server log, a single tap to open the keyboard
 * produced ten resizes (46 → 42 → 39 → … → 30) and another ten on closing.
 *
 * A differential renderer — Ink, the one Claude Code uses — repaints the whole
 * frame on every `SIGWINCH`, walking the cursor up with `ESC[nA`. That command
 * saturates at the first line of the SCREEN and never reaches the scrollback:
 * a frame taller than the screen **cannot erase itself**, so each repaint
 * leaves the previous copy above it. Twenty resizes, twenty copies.
 *
 * ## Why MEASURE, rather than guess
 *
 * The first fix kept the tallest height ever seen in the window and used that
 * as the reference. It passed twelve tests and six keyboard cycles on the
 * emulator — and then **wiped the operator's screen** in 0.1.17. The
 * instrumentation showed why: navigation animations produce spurious
 * measurements (`1080x1621px` in the middle of a transition, five times over
 * five entries), and a maximum KEEPS the spurious one forever. Once a height
 * that is too large has been kept, the grid ends up with rows that do not fit
 * and the content spills out over the visible area.
 *
 * There was a deeper error beneath that one: a maximum treats ANY shrink as
 * obstruction. The connection banner, the autocorrect composition band and
 * the attachment bar change the height for real — the grid should follow
 * them. Only the keyboard is an obstruction, and a rule that cannot tell the
 * difference has to guess.
 *
 * So this version guesses nothing: [alturaSemTeclado] returns the height the
 * grid would have with the keyboard closed by adding back **exactly** what
 * `imePadding()` took away. No memory, hence no spurious measurement kept; a
 * bad transition affects one frame and passes.
 */
object GeometriaDaGrade {

    /**
     * The height the grid would have with the keyboard closed.
     *
     * ## The arithmetic, and why it is exact rather than approximate
     *
     * The tree above this screen is, in `AppNavHost`:
     *
     * ```
     * NavHost(modifier = Modifier
     *     .padding(innerPadding)            // innerPadding.bottom = navigation bar
     *     .consumeWindowInsets(innerPadding) // consumes that bar
     *     .imePadding())                     // applies max(0, ime − consumed)
     * ```
     *
     * `imePadding()` applies the IME inset **minus what has already been
     * consumed**. Since what is consumed is exactly the navigation bar, what
     * the subtree lost is `max(0, ime − barraDeNavegação)` — neither the raw
     * IME inset (which would count the navigation bar twice) nor anything
     * else. Adding that back returns the keyboard-free height to the pixel.
     *
     * With the keyboard closed `imePx` is 0 and this function is the identity:
     * nothing happens, no correction stays switched on when it is not needed.
     *
     * @param alturaDisponivelPx the height the layout has just offered the grid.
     * @param imePx the IME's bottom inset, including the navigation bar area.
     * @param barraDeNavegacaoPx the navigation bar's bottom inset.
     */
    fun alturaSemTeclado(alturaDisponivelPx: Int, imePx: Int, barraDeNavegacaoPx: Int): Int =
        alturaDisponivelPx + (imePx - barraDeNavegacaoPx).coerceAtLeast(0)

    /**
     * How many pixels of the grid the keyboard is covering.
     *
     * It is the same number [alturaSemTeclado] added back, and it is what
     * anchors the grid by its BOTTOM while the keyboard is up — without it the
     * keyboard would cover precisely the cursor's line.
     */
    fun tapadoPeloTeclado(imePx: Int, barraDeNavegacaoPx: Int): Int =
        (imePx - barraDeNavegacaoPx).coerceAtLeast(0)

    /** Never zero: a degenerate window still has to yield a valid grid. */
    fun colunas(larguraPx: Int, cellWidthPx: Int): Int {
        require(cellWidthPx > 0) { "cellWidthPx tem que ser positivo, veio $cellWidthPx" }
        return (larguraPx / cellWidthPx).coerceAtLeast(1)
    }

    /** See [colunas] on the floor of 1. */
    fun linhas(alturaPx: Int, cellHeightPx: Int): Int {
        require(cellHeightPx > 0) { "cellHeightPx tem que ser positivo, veio $cellHeightPx" }
        return (alturaPx / cellHeightPx).coerceAtLeast(1)
    }
}
