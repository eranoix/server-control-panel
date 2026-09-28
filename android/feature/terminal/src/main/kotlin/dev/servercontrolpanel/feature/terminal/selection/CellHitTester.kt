package dev.servercontrolpanel.feature.terminal.selection

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import kotlin.math.floor

class CellHitTester(
    private val cellWidthPx: Float,
    private val cellHeightPx: Float,
    val cols: Int,
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

    data class Cell(val row: Int, val col: Int)

    fun hitTest(offset: Offset): Cell {
        val col = floorDiv(offset.x - originX, cellWidthPx).coerceIn(0, cols - 1)
        val row = floorDiv(offset.y - originY, cellHeightPx).coerceIn(0, rows - 1)
        return Cell(row = row, col = col)
    }

    fun cellRect(row: Int, col: Int): Rect {
        val left = originX + col * cellWidthPx
        val top = originY + row * cellHeightPx
        return Rect(left = left, top = top, right = left + cellWidthPx, bottom = top + cellHeightPx)
    }

    private fun floorDiv(value: Float, size: Float): Int = floor(value / size).toInt()
}
