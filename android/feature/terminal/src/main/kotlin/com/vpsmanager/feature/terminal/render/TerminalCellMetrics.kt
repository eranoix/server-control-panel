package com.vpsmanager.feature.terminal.render

import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Typeface
import kotlin.math.ceil
import kotlin.math.roundToInt

/**
 * The line spacing the grid always had: cell height equal to the font size,
 * with nothing added or taken away. It is made explicit so that "normal" is a
 * value with a name, and not the bare `0` of someone who forgot the argument.
 */
const val ENTRELINHA_NORMAL_PX: Int = 0

/**
 * Ratio between the cell height and the font size. It used to live duplicated
 * in [GlyphAtlas] (`cellHeightPx * 0.8f`) and in the screen's metrics
 * calculation; the two MUST use the same number, otherwise the cell width the
 * screen uses to place the columns is measured at a different font size from
 * the one the atlas rasterises the glyphs at — and then no column lands where
 * it should.
 */
const val GLYPH_TEXT_SIZE_RATIO: Float = 0.8f

/**
 * Sample used to measure the cell advance. In a genuinely monospaced font all
 * of these characters have the SAME advance, so the `max` below amounts to
 * measuring any one of them. The sample exists for the degenerate case: if the
 * resolved face should ever not be monospaced (exactly what happened when
 * [GlyphAtlas] silently fell back to Roboto), the `max` guarantees the cell
 * still holds the widest glyph — the text ends up loosely spaced, but stays on
 * the grid and NO glyph is clipped. Measuring a single character ("W") does
 * not have that property.
 */
private const val ADVANCE_SAMPLE = "WMm@#gilt1023"

/**
 * Metrics of one terminal cell, in WHOLE pixels.
 *
 * Whole is the point: the terminal draws each glyph into an atlas slot of
 * `cellWidthPx x cellHeightPx` pixels and then copies that slot to
 * `column * cellWidthPx`. If the cell width were fractional (it was: the raw
 * font advance, e.g. 20.16px), each column's destination rectangle would be
 * rounded differently — columns alternating 20 and 21px wide, with the glyph
 * bitmap stretched unevenly from column to column. With a whole pixel, the
 * atlas slot and the destination are exactly the same size: the copy is 1:1,
 * with no rescaling, and column N always lands at exactly `N * cellWidthPx`.
 */
data class TerminalCellMetrics(
    val cellWidthPx: Int,
    val cellHeightPx: Int,
    val textSizePx: Float,
)

/**
 * The one place that turns a font size into cell dimensions.
 *
 * [fontSizePx] is the size already converted from `sp` to pixels by the
 * screen's density (the conversion belongs to whoever holds the `Density`, not
 * here — that way this function stays pure enough to be measured in a JVM
 * test).
 *
 * The advance is measured with the SAME `Typeface` and the SAME `textSize`
 * that [GlyphAtlas] uses to rasterise, otherwise the screen's grid and the
 * atlas's glyphs disagree on scale.
 */
fun computeTerminalCellMetrics(
    fontSizePx: Float,
    entrelinhaPx: Int = ENTRELINHA_NORMAL_PX,
    typeface: Typeface = Typeface.MONOSPACE,
): TerminalCellMetrics {
    // The "full body" height: what the cell measured before line spacing
    // existed, and what it still measures at [ENTRELINHA_NORMAL].
    val alturaDeCorpo = fontSizePx.roundToInt().coerceAtLeast(1)
    // THE LETTER SIZE DOES NOT DEPEND ON THE LINE SPACING. Before this,
    // `textSizePx` was derived from `cellHeightPx`; had it stayed that way,
    // tightening the line spacing would shrink the glyph along with it — which
    // is exactly what the app's owner did NOT ask for ("less spacing", not
    // "smaller letters"). Now both come from the font size, and only the cell
    // height responds to the line spacing.
    val textSizePx = alturaDeCorpo * GLYPH_TEXT_SIZE_RATIO
    val paint = Paint().apply {
        this.typeface = typeface
        this.textSize = textSizePx
    }
    val advance = ADVANCE_SAMPLE.maxOf { paint.measureText(it.toString()) }
    return TerminalCellMetrics(
        cellWidthPx = advance.roundToInt().coerceAtLeast(1),
        // **A delta in WHOLE pixels, not a fractional factor.** The cell
        // height has to be a whole number (that is the condition for the 1:1
        // blit — see the comment on [TerminalCellMetrics]), and `alturaDeCorpo`
        // is already whole, so adding a whole number never comes near a
        // rounding. A factor such as 0.95 would look more natural and would be
        // worse: at a 14 px body, 0.90 and 0.95 round to the SAME 13 px — two
        // menu items with the same effect, which is how a preference earns a
        // reputation for being broken. It is the same choice Alacritty makes,
        // whose `font.offset.y` is a whole-pixel delta.
        cellHeightPx = (alturaDeCorpo + entrelinhaPx)
            .coerceAtLeast(alturaMinimaDaCelulaPx(paint)),
        textSizePx = textSizePx,
    )
}

/**
 * The ink sample used to find the cell's floor: the letters that REALLY cannot
 * be clipped — tall stems, Portuguese accents and the legs that drop below the
 * baseline.
 *
 * **What is left out, and why.** The box-drawing characters (`─│┌┘█`) are
 * designed on purpose to fill the whole cell, top to bottom, so that they tile
 * seamlessly between neighbouring rows. Measuring them would give a floor EQUAL
 * to the cell height that already exists (measured on the emulator: maximum ink
 * 41 px in a 42 px cell) and the compact line spacing would have nowhere to go.
 * They are left out, and the consequence is accepted and stated in the
 * interface: tighten the line spacing and the box-drawing characters may show a
 * hairline gap between rows. It is the same trade-off `kitty` documents under
 * `modify_font cell_height` ("decreasing the cell size might cause rendering
 * artifacts, so use with care") and that iTerm2 allows all the way down to
 * 0.5x. **No letter is clipped at any step** — that is what this floor
 * guarantees.
 */
private const val AMOSTRA_DE_TINTA = "ÂÊÍÕÜWMbdfhklt gjpqy ç,;_"

/**
 * The floor for the cell height, MEASURED on the font — not guessed.
 *
 * [GlyphAtlas] centres the baseline by the font's box
 * (`baselineY = top + (height - ascent - descent) / 2`), so what decides
 * whether a glyph fits is the height of its INK against the height of the
 * cell. Below the largest value in [AMOSTRA_DE_TINTA] the centring starts to
 * produce a rectangle smaller than the ink, and the letter is clipped at the
 * top and the bottom at once.
 *
 * `getTextBounds`, not `descent - ascent`: the font's typographic box carries
 * the slack the designer reserved for accents this face may not even have, and
 * using it as the floor would leave the compact line spacing with no room at
 * all (measured: a 40 px box in a 42 px cell). The real ink is what the person
 * sees.
 *
 * `ceil`, not `round`: rounding down would return a floor that already clips
 * half a pixel. The value comes from the `Paint` the atlas uses to rasterise,
 * with the same `textSize` and the same `Typeface` — measuring on another one
 * would be measuring another font.
 */
private fun alturaMinimaDaCelulaPx(paint: Paint): Int {
    val caixa = Rect()
    var tintaAcima = 0f // how far the tallest glyph rises above the baseline
    var tintaAbaixo = 0f // how far the lowest glyph drops below it
    for (c in AMOSTRA_DE_TINTA) {
        paint.getTextBounds(c.toString(), 0, 1, caixa)
        // A text `Rect` is relative to the baseline: `top` is negative above
        // it, `bottom` positive below.
        if (-caixa.top > tintaAcima) tintaAcima = -caixa.top.toFloat()
        if (caixa.bottom > tintaAbaixo) tintaAbaixo = caixa.bottom.toFloat()
    }
    // Solves the SAME sum [GlyphAtlas] uses to place the baseline:
    //
    //     base = (h - ascent - descent) / 2          (ascent < 0 < descent)
    //
    // The ink may not run past either edge of the cell:
    //
    //     base - tintaAcima  >= 0   ⇒  h >= 2·tintaAcima + ascent + descent
    //     base + tintaAbaixo <= h   ⇒  h >= 2·tintaAbaixo - ascent - descent
    //
    // This is exact, not a safety margin: it is the height at which the most
    // extreme letter in the sample touches the edge without crossing it. Note
    // that the baseline is placed by the FONT's metrics while the constraint
    // is on the INK — which is why the two quantities appear together.
    val fm = paint.fontMetrics
    val porCima = 2f * tintaAcima + fm.ascent + fm.descent
    val porBaixo = 2f * tintaAbaixo - fm.ascent - fm.descent
    return ceil(maxOf(porCima, porBaixo)).toInt().coerceAtLeast(1)
}
