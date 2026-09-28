package dev.servercontrolpanel.feature.terminal

import android.graphics.Bitmap
import android.graphics.Canvas
import android.util.Log
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.servercontrolpanel.feature.terminal.render.GlyphAtlas
import dev.servercontrolpanel.feature.terminal.render.buildRowDrawOps
import dev.servercontrolpanel.feature.terminal.render.rasterizeRow
import dev.servercontrolpanel.terminalengine.TerminalEngine
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class SoakTest {

    @Test
    fun sustainedOutput_survivesFullDurationWithBoundedAtlas() {
        val hours = soakHours()
        Log.i(TAG, "SoakTest starting: hours=$hours (smoke=${hours <= DEFAULT_SMOKE_HOURS})")

        val cellWidthPx = 16
        val cellHeightPx = 28
        val geometryA = 80 to 24
        val geometryB = 132 to 43

        val engine = TerminalEngine.create(geometryA.first, geometryA.second)
        val atlas = GlyphAtlas(cellWidthPx, cellHeightPx)
        val initialAtlasBytes = atlas.byteSize()
        val atlasCapacity = ATLAS_NARROW_CAPACITY + ATLAS_WIDE_CAPACITY

        val canvasBitmap = Bitmap.createBitmap(
            geometryB.first * cellWidthPx,
            geometryB.second * cellHeightPx,
            Bitmap.Config.ARGB_8888,
        )
        val renderCanvas = Canvas(canvasBitmap)

        try {
            val startNanos = System.nanoTime()
            val endNanos = startNanos + (hours * NANOS_PER_HOUR).toLong()
            var nextBurstAtNanos = startNanos + BURST_INTERVAL_NANOS
            var nextResizeAtNanos = startNanos + RESIZE_INTERVAL_NANOS
            var nextLogAtNanos = startNanos + LOG_INTERVAL_NANOS
            var geometryIsA = true
            var seq = 0L
            var chunkCount = 0L

            while (System.nanoTime() < endNanos) {
                val chunkStartNanos = System.nanoTime()

                engine.write(SoakContent.chunk(seq, SUSTAINED_CHUNK_BYTES))
                seq++
                chunkCount++

                if (chunkCount % RENDER_EVERY_N_CHUNKS == 0L) {
                    renderSnapshot(engine, atlas, renderCanvas, cellWidthPx, cellHeightPx)
                }

                val now = System.nanoTime()

                if (now >= nextBurstAtNanos) {
                    burst(engine, seq)
                    nextBurstAtNanos = now + BURST_INTERVAL_NANOS
                }

                if (now >= nextResizeAtNanos) {
                    geometryIsA = !geometryIsA
                    val target = if (geometryIsA) geometryA else geometryB
                    engine.resize(target.first, target.second)
                    nextResizeAtNanos = now + RESIZE_INTERVAL_NANOS
                }

                if (now >= nextLogAtNanos) {
                    val elapsedMinutes = (now - startNanos) / 60_000_000_000.0
                    Log.i(
                        TAG,
                        "soak progress: elapsedMin=%.1f chunks=%d atlasResident=%d/%d atlasBytes=%d"
                            .format(elapsedMinutes, chunkCount, atlas.residentCount(), atlasCapacity, atlas.byteSize()),
                    )
                    nextLogAtNanos = now + LOG_INTERVAL_NANOS
                }

                val elapsedThisChunkNanos = System.nanoTime() - chunkStartNanos
                val sleepNanos = SUSTAINED_INTERVAL_NANOS - elapsedThisChunkNanos
                if (sleepNanos > 0) {
                    Thread.sleep(sleepNanos / 1_000_000, (sleepNanos % 1_000_000).toInt())
                }
            }

            assertEquals(
                "glyph atlas byte size must never change after construction",
                initialAtlasBytes,
                atlas.byteSize(),
            )
            assertTrue(
                "glyph atlas resident count exceeded its fixed capacity",
                atlas.residentCount() <= atlasCapacity,
            )
            Log.i(TAG, "SoakTest completed: chunks=$chunkCount durationHours=$hours")
        } finally {
            engine.close()
        }
    }

    private fun renderSnapshot(
        engine: TerminalEngine,
        atlas: GlyphAtlas,
        canvas: Canvas,
        cellWidthPx: Int,
        cellHeightPx: Int,
    ) {
        val snapshot = engine.snapshot()
        for (y in 0 until snapshot.rows) {
            val cells = (0 until snapshot.cols).map { x -> snapshot.cellAt(x, y) }
            val ops = buildRowDrawOps(cells, defaultFg = 0xE0E0E0, defaultBg = 0x000000)
            rasterizeRow(
                canvas,
                ops,
                rowTopPx = y * cellHeightPx.toFloat(),
                cellWidthPx = cellWidthPx.toFloat(),
                cellHeightPx = cellHeightPx.toFloat(),
                glyphAtlas = atlas,
                defaultBg = 0x000000,
            )
        }
    }

    private fun burst(engine: TerminalEngine, seq: Long) {
        var remaining = BURST_TOTAL_BYTES
        var n = seq
        while (remaining > 0) {
            val size = minOf(BURST_SUBCHUNK_BYTES, remaining)
            engine.write(SoakContent.chunk(n, size))
            remaining -= size
            n++
        }
    }

    private fun soakHours(): Double {
        val raw = InstrumentationRegistry.getArguments().getString(SOAK_HOURS_ARG)
        val parsed = raw?.toDoubleOrNull()
        if (parsed == null) {
            Log.w(
                TAG,
                "instrumentation argument '$SOAK_HOURS_ARG' not supplied (or unparsable): '$raw' -- " +
                    "running a $DEFAULT_SMOKE_HOURS h smoke pass only. This does NOT satisfy the memory-soak " +
                    ">=4h gate; pass -Psoak.hours=4 (or more) for the real run.",
            )
        }
        return parsed ?: DEFAULT_SMOKE_HOURS
    }

    private object SoakContent {
        private val WIDE_CODEPOINTS = intArrayOf(0x4E2D, 0x6587, 0x1F600, 0x1F680)
        private const val FILLER = "the quick brown fox jumps over the lazy dog 0123456789 "

        fun chunk(seqStart: Long, targetBytes: Int): ByteArray {
            val sb = StringBuilder(targetBytes + 128)
            var n = seqStart
            while (sb.length < targetBytes) {
                sb.append(pieceFor(n))
                n++
            }
            return sb.toString().toByteArray(Charsets.UTF_8)
        }

        private fun pieceFor(n: Long): String = when ((n % 9).toInt()) {
            0 -> sgr16Line(n)
            1 -> sgr256Line(n)
            2 -> truecolorLine(n)
            3 -> attrLine(n)
            4 -> wideGlyphLine(n)
            5 -> scrollRegionBurst()
            6 -> altScreenBurst()
            7 -> eraseBurst()
            else -> plainLine(n)
        }

        private fun sgr16Line(n: Long): String {
            val fg = 30 + (n % 8).toInt()
            val bg = 40 + ((n / 8) % 8).toInt()
            return "[${fg};${bg}m$FILLER[0m\r\n"
        }

        private fun sgr256Line(n: Long): String {
            val color = (n % 256).toInt()
            return "[38;5;${color}m$FILLER[0m\r\n"
        }

        private fun truecolorLine(n: Long): String {
            val r = (n % 255).toInt()
            val g = ((n / 255) % 255).toInt()
            val b = ((n / 65_025) % 255).toInt()
            return "[38;2;${r};${g};${b}m$FILLER[0m\r\n"
        }

        private fun attrLine(n: Long): String {
            val codes = intArrayOf(1, 2, 3, 4, 5, 7, 9)
            val code = codes[(n % codes.size).toInt()]
            return "[${code}m$FILLER[0m\r\n"
        }

        private fun wideGlyphLine(n: Long): String {
            val codepoint = WIDE_CODEPOINTS[(n % WIDE_CODEPOINTS.size).toInt()]
            return String(Character.toChars(codepoint)).repeat(20) + "\r\n"
        }

        private fun scrollRegionBurst(): String =
            "[5;20r" + FILLER.repeat(3) + "\r\n" + "[r"

        private fun altScreenBurst(): String =
            "[?1049h" + FILLER + "\r\n" + "[?1049l"

        private fun eraseBurst(): String =
            "[H" + FILLER + "[2K[J"

        private fun plainLine(n: Long): String = "$FILLER seq=$n\r\n"
    }

    private companion object {
        const val TAG = "SoakTest"
        const val SOAK_HOURS_ARG = "soak.hours"

        const val DEFAULT_SMOKE_HOURS = 0.02

        const val NANOS_PER_HOUR = 3_600_000_000_000.0

        const val SUSTAINED_CHUNK_BYTES = 20_000
        val SUSTAINED_INTERVAL_NANOS = 100_000_000L

        const val BURST_TOTAL_BYTES = 5 * 1024 * 1024
        const val BURST_SUBCHUNK_BYTES = 256 * 1024
        val BURST_INTERVAL_NANOS = 10L * 60 * 1_000_000_000L

        val RESIZE_INTERVAL_NANOS = 15L * 60 * 1_000_000_000L

        val LOG_INTERVAL_NANOS = 60L * 1_000_000_000L

        const val ATLAS_NARROW_CAPACITY = 384
        const val ATLAS_WIDE_CAPACITY = 128

        const val RENDER_EVERY_N_CHUNKS = 5L
    }
}
