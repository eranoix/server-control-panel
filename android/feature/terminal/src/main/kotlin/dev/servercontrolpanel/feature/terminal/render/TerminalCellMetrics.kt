package dev.servercontrolpanel.feature.terminal.render

import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Typeface
import kotlin.math.ceil
import kotlin.math.roundToInt

const val NORMAL_LINE_SPACING_PX: Int = 0

const val GLYPH_TEXT_SIZE_RATIO: Float = 0.8f

private const val ADVANCE_SAMPLE = "WMm@#gilt1023"

data class TerminalCellMetrics(
    val cellWidthPx: Int,
    val cellHeightPx: Int,
    val textSizePx: Float,
)

fun computeTerminalCellMetrics(
    fontSizePx: Float,
    lineSpacingPx: Int = NORMAL_LINE_SPACING_PX,
    typeface: Typeface = Typeface.MONOSPACE,
): TerminalCellMetrics {
    val bodyHeight = fontSizePx.roundToInt().coerceAtLeast(1)
    val textSizePx = bodyHeight * GLYPH_TEXT_SIZE_RATIO
    val paint = Paint().apply {
        this.typeface = typeface
        this.textSize = textSizePx
    }
    val advance = ADVANCE_SAMPLE.maxOf { paint.measureText(it.toString()) }
    return TerminalCellMetrics(
        cellWidthPx = advance.roundToInt().coerceAtLeast(1),
        cellHeightPx = (bodyHeight + lineSpacingPx)
            .coerceAtLeast(minCellHeightPx(paint)),
        textSizePx = textSizePx,
    )
}

private const val INK_SAMPLE = "ÂÊÍÕÜWMbdfhklt gjpqy ç,;_"

private fun minCellHeightPx(paint: Paint): Int {
    val inbox = Rect()
    var inkAbove = 0f
    var inkBelow = 0f
    for (c in INK_SAMPLE) {
        paint.getTextBounds(c.toString(), 0, 1, inbox)
        if (-inbox.top > inkAbove) inkAbove = -inbox.top.toFloat()
        if (inbox.bottom > inkBelow) inkBelow = inbox.bottom.toFloat()
    }
    val fm = paint.fontMetrics
    val overTop = 2f * inkAbove + fm.ascent + fm.descent
    val underBottom = 2f * inkBelow - fm.ascent - fm.descent
    return ceil(maxOf(overTop, underBottom)).toInt().coerceAtLeast(1)
}
