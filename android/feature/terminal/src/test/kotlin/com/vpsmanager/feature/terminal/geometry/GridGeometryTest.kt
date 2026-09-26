package com.vpsmanager.feature.terminal.geometry

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The contract of this calculation is the antidote to a defect that came back
 * four times: the terminal history duplicated itself every time the keyboard
 * opened. That is why the tests below are written as QUESTIONS ABOUT THE
 * SERVER — "how many distinct grid sizes would it receive?" — and not as
 * checks on return values: it is the number of `SIGWINCH` that causes the
 * copy, not the number itself.
 *
 * The previous version of this class kept the tallest height ever seen and
 * passed the duplication tests — and still wiped the operator's screen in
 * 0.1.17, because a maximum also keeps the spurious measurements taken during
 * navigation animations. The duplication tests are still here; what changed is
 * that they now describe a MEASUREMENT, not a memory, and so there is no state
 * left that can be wrong.
 */
class GridGeometryTest {

    private val heightWithKeyboardClosedPx = 1840
    private val navBarPx = 63
    private val cellH = 40
    private val cellW = 16

    /**
     * The height the layout offers when the IME has an inset of [imePx] — it is
     * what the `imePadding()` in `AppNavHost` lets through.
     */
    private fun offeredHeight(imePx: Int) =
        heightWithKeyboardClosedPx - (imePx - navBarPx).coerceAtLeast(0)

    /**
     * The case that produced the duplication: ten intermediate insets on the
     * way open and ten on the way closed. The server must see NONE of them.
     */
    @Test
    fun `a animacao inteira do teclado nao muda o numero de linhas`() {
        val rowsSeen = mutableSetOf<Int>()

        // From keyboard closed (0) to open (740), frame by frame, and back.
        val insets = listOf(0, 120, 260, 400, 510, 600, 660, 700, 720, 740)
        (insets + insets.reversed()).forEach { ime ->
            val full = GridGeometry.heightWithoutKeyboard(
                availableHeightPx = offeredHeight(ime),
                imePx = ime,
                navBarPx = navBarPx,
            )
            rowsSeen += GridGeometry.lines(full, cellH)
        }

        assertEquals(
            "cada linha a mais neste conjunto é um SIGWINCH, e cada SIGWINCH é uma cópia do histórico",
            setOf(heightWithKeyboardClosedPx / cellH),
            rowsSeen,
        )
    }

    /**
     * With the keyboard closed the calculation has to be the IDENTITY. A
     * correction that stays on when it is not needed is one that fails
     * silently.
     */
    @Test
    fun `sem teclado nada e somado e nada e tapado`() {
        assertEquals(
            heightWithKeyboardClosedPx,
            GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx),
        )
        assertEquals(0, GridGeometry.coveredByKeyboard(0, navBarPx))
    }

    /**
     * The IME inset includes the navigation bar area, and `imePadding()`
     * discounts what has already been consumed above. Adding the RAW inset
     * would count the bar twice and give the grid rows that do not exist —
     * which is exactly how an oversized grid disappears off the top of the
     * screen.
     */
    @Test
    fun `a barra de navegacao nao e contada duas vezes`() {
        val ime = 740
        val full = GridGeometry.heightWithoutKeyboard(offeredHeight(ime), ime, navBarPx)

        assertEquals(heightWithKeyboardClosedPx, full)
        assertEquals(ime - navBarPx, GridGeometry.coveredByKeyboard(ime, navBarPx))
    }

    /**
     * A keyboard smaller than the navigation bar (or a device with no bar) must
     * not produce a negative offset — that would push the grid DOWN, hiding the
     * cursor behind the keyboard instead of above it.
     */
    @Test
    fun `inset menor que a barra nao vira deslocamento negativo`() {
        assertEquals(0, GridGeometry.coveredByKeyboard(imePx = 20, navBarPx = 63))
        assertEquals(
            heightWithKeyboardClosedPx,
            GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 20, 63),
        )
    }

    /**
     * The transient furniture of the screen — connection banner, autocorrect
     * strip, attachment bar — changes the height FOR REAL, and the grid has to
     * follow it. The previous rule, which kept the maximum, treated that as an
     * obstruction and left ghost rows behind.
     */
    @Test
    fun `mudanca real de altura muda o numero de linhas`() {
        val withBand = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx - 120, 0, navBarPx)
        val withoutBand = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx)

        assertEquals((heightWithKeyboardClosedPx - 120) / cellH, GridGeometry.lines(withBand, cellH))
        assertEquals(heightWithKeyboardClosedPx / cellH, GridGeometry.lines(withoutBand, cellH))
    }

    /**
     * No memory: a spurious measurement from a navigation animation affects its
     * own frame and nothing else. It was the absence of this that wiped the
     * screen in 0.1.17.
     */
    @Test
    fun `uma medida espuria nao contamina as seguintes`() {
        GridGeometry.heightWithoutKeyboard(9_000, 0, navBarPx)
        val after = GridGeometry.heightWithoutKeyboard(heightWithKeyboardClosedPx, 0, navBarPx)

        assertEquals(heightWithKeyboardClosedPx, after)
    }

    /** Changing the font size CHANGES the grid on purpose. */
    @Test
    fun `mudar o tamanho da celula muda o numero de linhas`() {
        assertEquals(heightWithKeyboardClosedPx / 20, GridGeometry.lines(heightWithKeyboardClosedPx, 20))
        assertEquals(heightWithKeyboardClosedPx / 60, GridGeometry.lines(heightWithKeyboardClosedPx, 60))
    }

    /** A degenerate window must not produce a grid of zero rows. */
    @Test
    fun `uma janela menor que uma celula ainda rende uma linha e uma coluna`() {
        assertEquals(1, GridGeometry.columns(4, cellW))
        assertEquals(1, GridGeometry.lines(4, cellH))
    }
}
