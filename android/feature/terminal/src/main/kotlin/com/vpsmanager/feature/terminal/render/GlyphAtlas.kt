package com.vpsmanager.feature.terminal.render

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.PorterDuff
import android.graphics.Rect
import android.graphics.Typeface
import kotlin.math.ceil
import kotlin.math.sqrt

/**
 * Rasterized-glyph cache backing [TerminalCanvas]. Two fixed-size ARGB_8888
 * bitmaps (one grid of narrow-cell slots, one grid of double-width-cell
 * slots) back every glyph the renderer draws; [GlyphSlotAllocator] decides
 * which slot a [GlyphKey] owns and evicts the least-recently-used glyph when
 * full, so [byteSize] never changes after construction no matter how many
 * distinct glyphs a session paints.
 *
 * Rasterization (an `android.graphics.Canvas` + `Paint.drawText` call) only
 * happens on a slot miss; a hit is a single `Rect` lookup. Glyphs are drawn
 * once in plain white so the same slot can be tinted to any foreground color
 * at draw time via `BlendMode`/`ColorFilter` in [TerminalCanvas], instead of
 * needing one cached slot per (codepoint, color) pair.
 */
class GlyphAtlas(
    private val cellWidthPx: Int,
    private val cellHeightPx: Int,
    private val typeface: Typeface = Typeface.MONOSPACE,
    private val textSizePx: Float = cellHeightPx * GLYPH_TEXT_SIZE_RATIO,
    narrowCapacity: Int = 384,
    wideCapacity: Int = 128,
) {
    private val narrowCols = ceil(sqrt(narrowCapacity.toDouble())).toInt().coerceAtLeast(1)
    private val narrowRows = ceil(narrowCapacity.toDouble() / narrowCols).toInt()
    private val wideCols = ceil(sqrt(wideCapacity.toDouble())).toInt().coerceAtLeast(1)
    private val wideRows = ceil(wideCapacity.toDouble() / wideCols).toInt()

    private val narrowAllocator = GlyphSlotAllocator(narrowCols * narrowRows)
    private val wideAllocator = GlyphSlotAllocator(wideCols * wideRows)

    val narrowBitmap: Bitmap = Bitmap.createBitmap(
        narrowCols * cellWidthPx,
        narrowRows * cellHeightPx,
        Bitmap.Config.ARGB_8888,
    )
    val wideBitmap: Bitmap = Bitmap.createBitmap(
        wideCols * cellWidthPx * 2,
        wideRows * cellHeightPx,
        Bitmap.Config.ARGB_8888,
    )

    private val narrowCanvas = Canvas(narrowBitmap)
    private val wideCanvas = Canvas(wideBitmap)
    private val paintCache = HashMap<Pair<Boolean, Boolean>, Paint>()

    /**
     * Total bytes committed to atlas pixel storage for the lifetime of this
     * instance: `4 bytes/px * (narrowBitmap area + wideBitmap area)`. The
     * requested capacities are rounded up to the nearest square-ish grid, so
     * with the defaults above (384 narrow -> a 20x20 = 400-slot grid, 128
     * wide -> a 12x11 = 132-slot grid) at a representative 16x28px monospace
     * cell that is a 320x560px narrow bitmap (716,800 bytes) plus a
     * 384x308px wide bitmap (473,088 bytes) = 1,189,888 bytes (~1.13MB),
     * fixed regardless of scrollback length or how many distinct glyphs a
     * session ever draws.
     */
    fun byteSize(): Long =
        4L * narrowBitmap.width * narrowBitmap.height + 4L * wideBitmap.width * wideBitmap.height

    /** Source bitmap + rect to draw for [key], rasterizing into that slot first on a cache miss. */
    fun slotFor(key: GlyphKey): Slot {
        val allocator = if (key.wide) wideAllocator else narrowAllocator
        val cols = if (key.wide) wideCols else narrowCols
        val cellW = if (key.wide) cellWidthPx * 2 else cellWidthPx
        val bitmap = if (key.wide) wideBitmap else narrowBitmap
        val canvas = if (key.wide) wideCanvas else narrowCanvas

        val acquisition = allocator.acquire(key)
        val row = acquisition.slot / cols
        val col = acquisition.slot % cols
        val rect = Rect(col * cellW, row * cellHeightPx, (col + 1) * cellW, (row + 1) * cellHeightPx)

        if (acquisition.needsRasterize) {
            canvas.save()
            canvas.clipRect(rect)
            canvas.drawColor(Color.TRANSPARENT, PorterDuff.Mode.CLEAR)
            val paint = paintFor(key.bold, key.italic)
            val text = String(Character.toChars(key.codepoint))
            val metrics = paint.fontMetrics
            val baselineY = rect.top + (rect.height() - metrics.ascent - metrics.descent) / 2f
            // Centre the glyph in the slot instead of butting it against
            // the left edge. In a monospaced face the advance equals the cell
            // width and this is a no-op (offset 0). It earns its keep for the
            // rest: the cell is a whole number of pixels while the advance may
            // be fractional (20 vs 20.16), and glyphs coming from a FALLBACK
            // (emoji, a symbol the monospaced face does not cover) may not
            // respect the cell metric. In those cases, centring splits the
            // difference across both sides instead of throwing it all to the
            // right, where the clipRect would cut off just one half of the
            // drawing.
            val advance = paint.measureText(text)
            val glyphX = rect.left + (rect.width() - advance) / 2f
            canvas.drawText(text, glyphX, baselineY, paint)
            canvas.restore()
        }

        return Slot(bitmap, rect)
    }

    /** Number of distinct glyphs currently resident, for diagnostics/tests. */
    fun residentCount(): Int = narrowAllocator.size() + wideAllocator.size()

    private fun paintFor(bold: Boolean, italic: Boolean): Paint = paintCache.getOrPut(bold to italic) {
        val style = when {
            bold && italic -> Typeface.BOLD_ITALIC
            bold -> Typeface.BOLD
            italic -> Typeface.ITALIC
            else -> Typeface.NORMAL
        }
        // `Typeface.create` is resolved OUT HERE, on purpose. Inside an
        // `apply { }` over a Paint, the bare identifier `typeface` resolves to
        // the innermost receiver — `Paint.getTypeface()`, which is null on a
        // freshly created Paint — and not to this class property. That was the
        // bug: `Typeface.create(null, style)` returns the DEFAULT face
        // (Roboto, proportional), so the atlas rasterized everything in Roboto
        // while the on-screen grid was measured in the monospaced face. The
        // result: a narrow glyph ('i', 'r', '1') left space to spare on the
        // right of the cell and a wide one ('m', 'W', '@') overflowed the cell
        // and was CUT by the clipRect — the text came out with gaps in the
        // middle of words. Keeping the creation outside the `apply` makes the
        // shadowing impossible to reintroduce.
        val resolved = Typeface.create(typeface, style)
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            this.typeface = resolved
            this.textSize = textSizePx
            color = Color.WHITE
        }
    }

    data class Slot(val bitmap: Bitmap, val rect: Rect)
}
