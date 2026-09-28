package dev.servercontrolpanel.feature.terminal.geometry

object GridGeometry {

    fun heightWithoutKeyboard(availableHeightPx: Int, imePx: Int, navBarPx: Int): Int =
        availableHeightPx + (imePx - navBarPx).coerceAtLeast(0)

    fun coveredByKeyboard(imePx: Int, navBarPx: Int): Int =
        (imePx - navBarPx).coerceAtLeast(0)

    fun columns(widthPx: Int, cellWidthPx: Int): Int {
        require(cellWidthPx > 0) { "cellWidthPx must be positive, got $cellWidthPx" }
        return (widthPx / cellWidthPx).coerceAtLeast(1)
    }

    fun lines(heightPx: Int, cellHeightPx: Int): Int {
        require(cellHeightPx > 0) { "cellHeightPx must be positive, got $cellHeightPx" }
        return (heightPx / cellHeightPx).coerceAtLeast(1)
    }
}
