package dev.servercontrolpanel.feature.terminal.render

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.PorterDuff
import android.graphics.Rect
import android.graphics.Typeface
import kotlin.math.ceil
import kotlin.math.sqrt

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

    fun byteSize(): Long =
        4L * narrowBitmap.width * narrowBitmap.height + 4L * wideBitmap.width * wideBitmap.height

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
            val advance = paint.measureText(text)
            val glyphX = rect.left + (rect.width() - advance) / 2f
            canvas.drawText(text, glyphX, baselineY, paint)
            canvas.restore()
        }

        return Slot(bitmap, rect)
    }

    fun residentCount(): Int = narrowAllocator.size() + wideAllocator.size()

    private fun paintFor(bold: Boolean, italic: Boolean): Paint = paintCache.getOrPut(bold to italic) {
        val style = when {
            bold && italic -> Typeface.BOLD_ITALIC
            bold -> Typeface.BOLD
            italic -> Typeface.ITALIC
            else -> Typeface.NORMAL
        }
        val resolved = Typeface.create(typeface, style)
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            this.typeface = resolved
            this.textSize = textSizePx
            color = Color.WHITE
        }
    }

    data class Slot(val bitmap: Bitmap, val rect: Rect)
}
