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
     * Total bytes of atlas pixel storage, fixed for the instance's lifetime:
     * `4 bytes/px * (narrow area + wide area)`. With the defaults (384 narrow ->
     * 400 slots, 128 wide -> 132 slots) and a 16x28 px cell this is about 1.13 MB,
     * regardless of scrollback or glyph variety.
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
            // Centre the glyph in the slot: a no-op for true monospace, but the
            // advance may be fractional or a fallback glyph (emoji, symbols) may
            // not match the cell, and centring keeps the clipRect from cutting
            // only one side.
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
        // Resolve `Typeface.create` outside `apply { }`: inside it, `typeface`
        // would resolve to `Paint.getTypeface()` (null on a new Paint), giving the
        // proportional default face and glyphs clipped by the monospace grid.
        val resolved = Typeface.create(typeface, style)
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            this.typeface = resolved
            this.textSize = textSizePx
            color = Color.WHITE
        }
    }

    data class Slot(val bitmap: Bitmap, val rect: Rect)
}
