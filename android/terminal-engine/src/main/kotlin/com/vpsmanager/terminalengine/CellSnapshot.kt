package com.vpsmanager.terminalengine

import java.nio.ByteBuffer
import java.nio.ByteOrder

/**
 * Immutable, JVM-owned copy of a terminal viewport at one point in time.
 *
 * Every field here is a plain Kotlin value or array copied out of the
 * engine's single reused native snapshot buffer at construction time —
 * nothing in this class ever aliases native memory. A later [TerminalEngine.write]
 * or [TerminalEngine.snapshot] call reuses and overwrites that native buffer,
 * but it cannot change a [CellSnapshot] that has already been built, because
 * the copy already happened. This is the Kotlin-side half of the
 * lock-then-copy discipline: the native side copies out of the live grid
 * under a lock into the reused buffer, and this class copies out of that
 * buffer into independent, immutable storage.
 *
 * This is the only type Wave 4's Compose renderer needs to read — it never
 * touches the native buffer or any terminal-library type directly.
 */
class CellSnapshot private constructor(
    val cols: Int,
    val rows: Int,
    val cursorX: Int,
    val cursorY: Int,
    val cursorVisible: Boolean,
    val cursorViewportValid: Boolean,
    val cursorWideTail: Boolean,
    private val rowFlags: ByteArray,
    private val cells: Array<Cell>,
) {

    enum class Wide { NARROW, WIDE, SPACER_TAIL, SPACER_HEAD }

    /** One grid cell. `fg`/`bg` are packed 0xRRGGBB, or null when unset (caller uses its own default). */
    data class Cell(
        val codepoint: Int,
        val fg: Int?,
        val bg: Int?,
        val bold: Boolean,
        val italic: Boolean,
        val faint: Boolean,
        val blink: Boolean,
        val inverse: Boolean,
        val invisible: Boolean,
        val strikethrough: Boolean,
        val overline: Boolean,
        val underline: Int,
        val wide: Wide,
    )

    fun cellAt(x: Int, y: Int): Cell {
        require(x in 0 until cols && y in 0 until rows) { "cell ($x, $y) outside ${cols}x$rows grid" }
        return cells[y * cols + x]
    }

    fun isWrapped(row: Int): Boolean = (rowFlags[row].toInt() and 0x01) != 0
    fun isWrapContinuation(row: Int): Boolean = (rowFlags[row].toInt() and 0x02) != 0

    companion object {
        private const val HEADER_SIZE = 16
        private const val CELL_STRIDE = 16

        private fun rowFlagsBytes(rows: Int): Int = (rows + 3) and 0x03.inv()

        /**
         * Parses the layout written by the native JNI shim's `nativeSnapshot`
         * (see that source file's header comment for the exact byte layout).
         * `buffer` is the engine's single reused direct buffer; this only
         * reads it and never advances its shared position (every access is
         * an absolute index), so it is safe to call right after
         * `nativeSnapshot()` without disturbing the buffer for later reuse.
         */
        internal fun fromBuffer(buffer: ByteBuffer, cols: Int, rows: Int): CellSnapshot {
            val b = buffer.duplicate().order(ByteOrder.LITTLE_ENDIAN)

            val cursorX = b.getShort(4).toInt() and 0xffff
            val cursorY = b.getShort(6).toInt() and 0xffff
            val cursorVisible = b.get(8).toInt() != 0
            val cursorViewportValid = b.get(9).toInt() != 0
            val cursorWideTail = b.get(10).toInt() != 0

            val flagsBytes = rowFlagsBytes(rows)
            val rowFlags = ByteArray(rows)
            for (y in 0 until rows) {
                rowFlags[y] = b.get(HEADER_SIZE + y)
            }

            val cellsBase = HEADER_SIZE + flagsBytes
            val cells = Array(cols * rows) { index ->
                val offset = cellsBase + index * CELL_STRIDE
                val codepoint = b.getInt(offset)
                val fgValid = b.get(offset + 4).toInt() != 0
                val fg = if (fgValid) packRgb(b, offset + 5) else null
                val bgValid = b.get(offset + 8).toInt() != 0
                val bg = if (bgValid) packRgb(b, offset + 9) else null
                val attrs = b.get(offset + 12).toInt()
                val underline = b.get(offset + 13).toInt() and 0xff
                val wide = Wide.entries[b.get(offset + 14).toInt() and 0xff]

                Cell(
                    codepoint = codepoint,
                    fg = fg,
                    bg = bg,
                    bold = (attrs and (1 shl 0)) != 0,
                    italic = (attrs and (1 shl 1)) != 0,
                    faint = (attrs and (1 shl 2)) != 0,
                    blink = (attrs and (1 shl 3)) != 0,
                    inverse = (attrs and (1 shl 4)) != 0,
                    invisible = (attrs and (1 shl 5)) != 0,
                    strikethrough = (attrs and (1 shl 6)) != 0,
                    overline = (attrs and (1 shl 7)) != 0,
                    underline = underline,
                    wide = wide,
                )
            }

            return CellSnapshot(
                cols = cols,
                rows = rows,
                cursorX = cursorX,
                cursorY = cursorY,
                cursorVisible = cursorVisible,
                cursorViewportValid = cursorViewportValid,
                cursorWideTail = cursorWideTail,
                rowFlags = rowFlags,
                cells = cells,
            )
        }

        private fun packRgb(b: ByteBuffer, offset: Int): Int {
            val r = b.get(offset).toInt() and 0xff
            val g = b.get(offset + 1).toInt() and 0xff
            val blue = b.get(offset + 2).toInt() and 0xff
            return (r shl 16) or (g shl 8) or blue
        }
    }
}
