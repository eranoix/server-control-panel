package com.vpsmanager.feature.terminal.render

import android.graphics.BlendMode
import android.graphics.BlendModeColorFilter
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Rect

/**
 * Draws one row's [GlyphDrawOp]s onto a plain `android.graphics.Canvas`.
 * This is the single rasterization path used both by [TerminalCanvas]'s
 * Compose draw lambda (via `drawIntoCanvas`) and by `VtConformanceTest`'s
 * pixel-level golden-PNG assertions -- there is exactly one place that
 * turns draw ops into pixels, so a pixel-level test genuinely exercises the
 * same code the production renderer runs, not a reimplementation of it.
 *
 * When [debugSolidBlocks] is true, each op is drawn as a solid
 * [GlyphDrawOp.fg]-colored rectangle spanning its full cell width instead
 * of the real rasterized glyph -- this is what the golden-PNG conformance
 * test uses, because real system-font anti-aliased text is not
 * byte-for-byte reproducible across devices/OS versions without a device
 * to capture the golden from. It still exercises the exact column/width
 * arithmetic this file is responsible for (a wide glyph's rect spans
 * exactly two cells, a spacer contributes no op and therefore no pixels of
 * its own), which is what the plan's double-width risk is actually about.
 */
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
                // Round, never truncate. With `toInt()` (truncation) a
                // fractional cell width makes column N start at floor(N*w) and
                // end at floor(N*w + w): the columns alternate between w and
                // w+1 pixels wide, and the glyph's bitmap — which is exactly
                // the width of the atlas slot — is rescaled differently in
                // each column. Rounding both edges, column N starts at
                // round(N*w) and lands exactly where column N+1 begins (no
                // seam in the background), and with the whole-pixel cell width
                // that `computeTerminalCellMetrics` now guarantees, the
                // destination is the same size as the slot: a 1:1 copy, with
                // no rescaling at all.
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
