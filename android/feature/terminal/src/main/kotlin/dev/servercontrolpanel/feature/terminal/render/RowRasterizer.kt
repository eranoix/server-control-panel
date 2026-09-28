package dev.servercontrolpanel.feature.terminal.render

import android.graphics.BlendMode
import android.graphics.BlendModeColorFilter
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Rect

internal fun rasterizeRow(
    canvas: Canvas,
    ops: List<GlyphDrawOp>,
    rowTopPx: Float,
    cellWidthPx: Float,
    cellHeightPx: Float,
    glyphAtlas: GlyphAtlas,
    defaultBg: Int,
    debugSolidBlocks: Boolean = false,
) {
    val bgPaint = Paint()
    val linePaint = Paint().apply { strokeWidth = 1.5f }

    for (op in ops) {
        val widthCells = if (op.wide) 2 else 1
        val x = op.column * cellWidthPx
        val cellWidth = cellWidthPx * widthCells

        if (op.bg != defaultBg) {
            bgPaint.color = argb(op.bg)
            canvas.drawRect(x, rowTopPx, x + cellWidth, rowTopPx + cellHeightPx, bgPaint)
        }

        if (op.drawGlyph) {
            if (debugSolidBlocks) {
                bgPaint.color = argb(op.fg)
                canvas.drawRect(x, rowTopPx, x + cellWidth, rowTopPx + cellHeightPx, bgPaint)
            } else {
                val slot = glyphAtlas.slotFor(GlyphKey(op.codepoint, op.bold, op.italic, op.wide))
                val paint = Paint().apply {
                    colorFilter = BlendModeColorFilter(argb(op.fg), BlendMode.SRC_IN)
                }
                val dst = Rect(
                    Math.round(x),
                    Math.round(rowTopPx),
                    Math.round(x + cellWidth),
                    Math.round(rowTopPx + cellHeightPx),
                )
                canvas.drawBitmap(slot.bitmap, slot.rect, dst, paint)
            }
        }

        if (op.underline > 0) {
            linePaint.color = argb(op.fg)
            val lineY = rowTopPx + cellHeightPx - 2f
            canvas.drawLine(x, lineY, x + cellWidth, lineY, linePaint)
        }
        if (op.strikethrough) {
            linePaint.color = argb(op.fg)
            val lineY = rowTopPx + cellHeightPx / 2f
            canvas.drawLine(x, lineY, x + cellWidth, lineY, linePaint)
        }
        if (op.overline) {
            linePaint.color = argb(op.fg)
            canvas.drawLine(x, rowTopPx, x + cellWidth, rowTopPx, linePaint)
        }
    }
}

private fun argb(rgb: Int): Int = (0xff shl 24) or (rgb and 0x00ffffff)
