package dev.servercontrolpanel.feature.terminal.render

import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Typeface
import kotlin.math.ceil
import kotlin.math.roundToInt

/** The default line spacing: cell height equal to the font size, nothing added or removed. */
const val NORMAL_LINE_SPACING_PX: Int = 0

/**
 * Ratio between font size and text size. [GlyphAtlas] and the screen metrics
 * MUST share it, or columns are measured at a different size than glyphs are
 * rasterised.
 */
const val GLYPH_TEXT_SIZE_RATIO: Float = 0.8f

/**
 * Sample used to measure the cell advance. In a true monospace font all have the
 * same advance; if the resolved face is not monospace, `max` keeps the widest
 * glyph inside the cell so nothing is clipped.
 */
private const val ADVANCE_SAMPLE = "WMm@#gilt1023"

/**
 * Metrics of one terminal cell, in WHOLE pixels, so the atlas slot and the
 * destination rectangle have the same size: the copy is 1:1 and column N always
 * lands at exactly `N * cellWidthPx` (fractional widths made columns alternate
 * between 20 and 21 px).
 */
data class TerminalCellMetrics(
    val cellWidthPx: Int,
    val cellHeightPx: Int,
    val textSizePx: Float,
)

/**
 * The one place that turns a font size into cell dimensions. [fontSizePx] is
 * already converted from `sp` by the caller, keeping this JVM-testable. The
 * advance is measured with the same `Typeface` and `textSize` [GlyphAtlas] uses.
 */
fun computeTerminalCellMetrics(
    fontSizePx: Float,
    lineSpacingPx: Int = NORMAL_LINE_SPACING_PX,
    typeface: Typeface = Typeface.MONOSPACE,
): TerminalCellMetrics {
    // The body height: the cell height at [NORMAL_LINE_SPACING_PX].
    val bodyHeight = fontSizePx.roundToInt().coerceAtLeast(1)
    // The text size derives from the body, not the cell height, so tighter
    // spacing does not shrink the letters.
    val textSizePx = bodyHeight * GLYPH_TEXT_SIZE_RATIO
    val paint = Paint().apply {
        this.typeface = typeface
        this.textSize = textSizePx
    }
    val advance = ADVANCE_SAMPLE.maxOf { paint.measureText(it.toString()) }
    return TerminalCellMetrics(
        cellWidthPx = advance.roundToInt().coerceAtLeast(1),
        // A whole-pixel delta keeps the height whole for the 1:1 blit; a factor
        // like 0.90 vs 0.95 would round to the same height at small sizes.
        cellHeightPx = (bodyHeight + lineSpacingPx)
            .coerceAtLeast(minCellHeightPx(paint)),
        textSizePx = textSizePx,
    )
}

/**
 * Ink sample for the cell floor: tall stems, accents and descenders that must
 * never be clipped.
 *
 * Box-drawing characters are excluded: they are designed to fill the whole cell
 * (41 px ink in a 42 px cell), which would leave no room for compact spacing.
 * The accepted cost is a hairline gap between box rows at tight spacing, as
 * `kitty` documents for `modify_font cell_height`. No letter is ever clipped.
 */
private const val INK_SAMPLE = "ÂÊÍÕÜWMbdfhklt gjpqy ç,;_"

/**
 * The minimum cell height, measured on the font. [GlyphAtlas] centres the
 * baseline by the font box, so what matters is ink height against cell height.
 * Uses `getTextBounds` (real ink) rather than the typographic box, which carries
 * slack and would leave no room for compact spacing, and `ceil` so the floor never
 * clips half a pixel. Measures with the same `Paint` the atlas uses.
 */
private fun minCellHeightPx(paint: Paint): Int {
    val inbox = Rect()
    var inkAbove = 0f // how far the tallest glyph rises above the baseline
    var inkBelow = 0f // how far the lowest glyph drops below it
    for (c in INK_SAMPLE) {
        paint.getTextBounds(c.toString(), 0, 1, inbox)
        // Text bounds are relative to the baseline: `top` is negative above it.
        if (-inbox.top > inkAbove) inkAbove = -inbox.top.toFloat()
        if (inbox.bottom > inkBelow) inkBelow = inbox.bottom.toFloat()
    }
    // Solve the placement [GlyphAtlas] uses:
    //
    //     base = (h - ascent - descent) / 2          (ascent < 0 < descent)
    //
    // The ink must not cross either edge:
    //
    //     base - inkAbove >= 0  =>  h >= 2*inkAbove + ascent + descent
    //     base + inkBelow <= h  =>  h >= 2*inkBelow - ascent - descent
    //
    // This is exact: the height where the most extreme glyph touches the edge.
    val fm = paint.fontMetrics
    val overTop = 2f * inkAbove + fm.ascent + fm.descent
    val underBottom = 2f * inkBelow - fm.ascent - fm.descent
    return ceil(maxOf(overTop, underBottom)).toInt().coerceAtLeast(1)
}
