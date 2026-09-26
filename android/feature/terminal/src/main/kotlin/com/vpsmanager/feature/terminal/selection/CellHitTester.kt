package com.vpsmanager.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import kotlin.math.floor

/**
 * Pure pixel-to-cell coordinate mapping for the terminal grid — the single
 * source of truth both [SelectionGestureController] (grid selection) and the
 * mouse-reporting drag path build on. Neither is allowed to compute its own
 * pixel-to-cell math independently: one mapping, one rounding convention.
 *
 * Deliberately has zero dependency on Compose runtime, `foundation`, or any
 * Android framework class beyond the pure geometry types [Offset]/[Rect] —
 * it takes fixed cell metrics and a grid origin as constructor parameters,
 * recomputed by the caller ([com.vpsmanager.feature.terminal.ui.TerminalRoute])
 * whenever font size or layout changes; this class never reads either itself.
 *
 * Rounding convention: a pixel maps to a cell by flooring `(pixel - origin) /
 * cellSize`, so a point exactly on a cell's left/top edge belongs to that
 * cell (the one to the right/below the boundary), never the previous one.
 * Points outside the grid clamp to the nearest valid cell rather than
 * producing a negative or out-of-bounds index.
 */
class CellHitTester(
    private val cellWidthPx: Float,
    private val cellHeightPx: Float,
    /** Grid width in cells. Public because the selection highlight needs to know how far a row runs. */
    val cols: Int,
    /** Grid height in cells. */
    val rows: Int,
    private val originX: Float = 0f,
    private val originY: Float = 0f,
) {
    init {
        require(cellWidthPx > 0f) { "cellWidthPx must be positive, was $cellWidthPx" }
        require(cellHeightPx > 0f) { "cellHeightPx must be positive, was $cellHeightPx" }
        require(cols > 0) { "cols must be positive, was $cols" }
        require(rows > 0) { "rows must be positive, was $rows" }
    }

    /** A grid cell coordinate — always clamped in-bounds by [hitTest]. */
    data class Cell(val row: Int, val col: Int)

    /** Maps a pixel offset to its grid cell, clamped to `[0, cols)` x `[0, rows)`. */
    fun hitTest(offset: Offset): Cell {
        val col = floorDiv(offset.x - originX, cellWidthPx).coerceIn(0, cols - 1)
        val row = floorDiv(offset.y - originY, cellHeightPx).coerceIn(0, rows - 1)
        return Cell(row = row, col = col)
    }

    /**
     * Inverse of [hitTest]: the pixel rectangle a given cell occupies, used
     * to position selection handles and to draw mouse-report feedback. Its
     * center, fed back into [hitTest], always returns the same `(row, col)`.
     */
    fun cellRect(row: Int, col: Int): Rect {
        val left = originX + col * cellWidthPx
        val top = originY + row * cellHeightPx
        return Rect(left = left, top = top, right = left + cellWidthPx, bottom = top + cellHeightPx)
    }

    private fun floorDiv(value: Float, size: Float): Int = floor(value / size).toInt()
}
