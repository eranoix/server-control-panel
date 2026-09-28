package dev.servercontrolpanel.terminalengine

import android.view.KeyEvent
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlin.random.Random
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TerminalEngineTest {

    private fun cell(text: String, from: Int = 0): Char =
        text[from]

    private fun row(snapshot: CellSnapshot, y: Int): String {
        val sb = StringBuilder()
        for (x in 0 until snapshot.cols) {
            val cp = snapshot.cellAt(x, y).codepoint
            if (cp != 0) sb.appendCodePoint(cp)
        }
        return sb.toString()
    }

    @Test
    fun write_hello_placesCellsAndCursor() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            engine.write("hello".toByteArray(Charsets.UTF_8))
            val snap = engine.snapshot()
            assertEquals('h', Character.toChars(snap.cellAt(0, 0).codepoint)[0])
            assertEquals('e', Character.toChars(snap.cellAt(1, 0).codepoint)[0])
            assertEquals('l', Character.toChars(snap.cellAt(2, 0).codepoint)[0])
            assertEquals('l', Character.toChars(snap.cellAt(3, 0).codepoint)[0])
            assertEquals('o', Character.toChars(snap.cellAt(4, 0).codepoint)[0])
            assertEquals(5, snap.cursorX)
            assertEquals(0, snap.cursorY)
        } finally {
            engine.close()
        }
    }

    @Test
    fun write_sgr31_setsDistinctForegroundThatResetClears() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            engine.write("[31mred[0mx".toByteArray(Charsets.UTF_8))
            val snap = engine.snapshot()
            val redFg0 = snap.cellAt(0, 0).fg
            val redFg1 = snap.cellAt(1, 0).fg
            val redFg2 = snap.cellAt(2, 0).fg
            val afterResetFg = snap.cellAt(3, 0).fg

            assertTrue("SGR 31 must set a foreground color", redFg0 != null)
            assertEquals(redFg0, redFg1)
            assertEquals(redFg0, redFg2)
            assertTrue("SGR 0 must clear the foreground override", afterResetFg != redFg0)
        } finally {
            engine.close()
        }
    }

    @Test
    fun write_sgr1_4_setsBoldAndUnderline() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            engine.write("[1;4mx".toByteArray(Charsets.UTF_8))
            val cell = engine.snapshot().cellAt(0, 0)
            assertTrue("expected bold", cell.bold)
            assertTrue("expected non-zero underline style", cell.underline != 0)
        } finally {
            engine.close()
        }
    }

    @Test
    fun write_doubleWidthGlyph_leadingCellHoldsCodepointTrailingIsSpacer() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            engine.write("你".toByteArray(Charsets.UTF_8))
            val snap = engine.snapshot()
            val leading = snap.cellAt(0, 0)
            val trailing = snap.cellAt(1, 0)

            assertEquals(0x4F60, leading.codepoint)
            assertEquals(CellSnapshot.Wide.WIDE, leading.wide)
            assertEquals(CellSnapshot.Wide.SPACER_TAIL, trailing.wide)
            assertTrue(
                "trailing cell must be a continuation marker, not a duplicate glyph",
                trailing.codepoint != leading.codepoint,
            )
        } finally {
            engine.close()
        }
    }

    @Test
    fun write_200ColumnsInto80ColumnGrid_wrapsWithContinuationRecorded() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            engine.write("a".repeat(200).toByteArray(Charsets.US_ASCII))
            val snap = engine.snapshot()

            assertEquals(80, row(snap, 0).length)
            assertTrue("row 0 should be marked wrapped", snap.isWrapped(0))
            assertTrue("row 1 should be a wrap continuation", snap.isWrapContinuation(1))

            var total = 0
            for (y in 0 until 3) {
                total += row(snap, y).count { it == 'a' }
            }
            assertEquals("no character truncated across the wrap", 200, total)
        } finally {
            engine.close()
        }
    }

    @Test
    fun resize_reflowsWrappedContentAndUpdatesGeometry() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            engine.write("b".repeat(120).toByteArray(Charsets.US_ASCII))
            val before = engine.snapshot()
            val beforeCount = (0 until before.rows).sumOf { y -> row(before, y).count { it == 'b' } }

            engine.resize(40, 24)
            val after = engine.snapshot()
            assertEquals(40, after.cols)

            val afterCount = (0 until after.rows).sumOf { y -> row(after, y).count { it == 'b' } }
            assertEquals("resize must reflow, not drop, content", beforeCount, afterCount)
        } finally {
            engine.close()
        }
    }

    private fun mixedFixture(): ByteArray {
        val sb = StringBuilder()
        sb.append("plain-text-")
        sb.append("[1;32mgreen[0m-")
        sb.append("你好-")
        sb.append("more plain text after multibyte")
        return sb.toString().toByteArray(Charsets.UTF_8)
    }

    private fun snapshotSignature(snap: CellSnapshot): String {
        val sb = StringBuilder()
        sb.append(snap.cols).append('x').append(snap.rows).append(';')
        sb.append(snap.cursorX).append(',').append(snap.cursorY).append(';')
        for (y in 0 until snap.rows) {
            for (x in 0 until snap.cols) {
                val c = snap.cellAt(x, y)
                sb.append(c.codepoint).append(':').append(c.wide).append(':')
                    .append(c.bold).append(':').append(c.fg).append(':').append(c.bg).append('|')
            }
        }
        return sb.toString()
    }

    @Test
    fun splitWriteInvariance_wholeOneByteAndRandomSplitsYieldIdenticalSnapshots() {
        val fixture = mixedFixture()

        fun runWith(chunks: List<ByteArray>): String {
            val engine = TerminalEngine.create(cols = 80, rows = 24)
            try {
                chunks.forEach { engine.write(it) }
                return snapshotSignature(engine.snapshot())
            } finally {
                engine.close()
            }
        }

        val whole = runWith(listOf(fixture))
        val oneByte = runWith(fixture.map { byteArrayOf(it) })

        val random = Random(42)
        val randomChunks = mutableListOf<ByteArray>()
        var i = 0
        while (i < fixture.size) {
            val remaining = fixture.size - i
            val len = 1 + random.nextInt(remaining)
            randomChunks.add(fixture.copyOfRange(i, i + len))
            i += len
        }
        val randomSplit = runWith(randomChunks)

        assertEquals("one call vs 1-byte calls must be identical", whole, oneByte)
        assertEquals("one call vs random-split calls must be identical", whole, randomSplit)
    }

    @Test
    fun fuzz_10000RandomWritesInterleavedWithSnapshotAndResize_doesNotCrash() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        val random = Random(1337)
        try {
            for (i in 0 until 10_000) {
                val len = random.nextInt(1, 64)
                val bytes = ByteArray(len) { random.nextInt(0, 256).toByte() }
                engine.write(bytes)
                if (i % 50 == 0) engine.snapshot()
                if (i % 500 == 0) {
                    val cols = 40 + random.nextInt(0, 80)
                    val rows = 12 + random.nextInt(0, 30)
                    engine.resize(cols, rows)
                }
            }
            engine.snapshot()
        } finally {
            engine.close()
        }
    }

    @Test
    fun snapshot_isImmutableAfterSubsequentWrite() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            engine.write("A".toByteArray(Charsets.US_ASCII))
            val first = engine.snapshot()
            assertEquals('A'.code, first.cellAt(0, 0).codepoint)

            engine.write("[HZ".toByteArray(Charsets.US_ASCII))
            val second = engine.snapshot()
            assertEquals('Z'.code, second.cellAt(0, 0).codepoint)

            assertEquals(
                "previously returned snapshot must not mutate after a later write",
                'A'.code,
                first.cellAt(0, 0).codepoint,
            )
        } finally {
            engine.close()
        }
    }

    @Test
    fun twoThreadContention_writerAndSnapshotReader_60SecondsNoCrashSelfConsistentRows() {
        runContentionScenario(injectSleepInCopyWindow = false)
    }

    @Test
    fun twoThreadContention_widenedRaceWithSleepInCopyWindow_60SecondsNoCrashSelfConsistentRows() {
        runContentionScenario(injectSleepInCopyWindow = true)
    }

    private fun runContentionScenario(injectSleepInCopyWindow: Boolean) {
        val cols = 80
        val rows = 24
        val engine = TerminalEngine.create(cols = cols, rows = rows)
        val generation = AtomicInteger(0)
        val running = AtomicBoolean(true)
        val failure = AtomicReference<Throwable?>(null)
        val durationMillis = 60_000L

        val writer = Thread {
            try {
                val random = Random(7)
                while (running.get()) {
                    val gen = generation.incrementAndGet()
                    val tag = (gen % 1_000_000).toString().padStart(6, '0')
                    val fill = tag.repeat((cols / tag.length) + 1).take(cols)
                    val colorCode = 30 + (gen % 8)
                    val sb = StringBuilder()
                    sb.append("[H")
                    sb.append("[").append(colorCode).append('m')
                    sb.append(fill)
                    if (injectSleepInCopyWindow && gen % 37 == 0) {
                        val controlPrefix = sb.length - fill.length
                        engine.write(sb.substring(0, controlPrefix).toByteArray(Charsets.US_ASCII))
                        Thread.sleep(1)
                        engine.write(sb.substring(controlPrefix).toByteArray(Charsets.US_ASCII))
                    } else {
                        engine.write(sb.toString().toByteArray(Charsets.US_ASCII))
                    }
                    if (random.nextInt(200) == 0) {
                        engine.resize(cols, rows)
                    }
                }
            } catch (t: Throwable) {
                failure.compareAndSet(null, t)
            }
        }

        val reader = Thread {
            try {
                while (running.get()) {
                    val snap = engine.snapshot()
                    assertEquals(cols, snap.cols)
                    assertEquals(rows, snap.rows)

                    val text = row(snap, 0)
                    if (text.length >= 6) {
                        val firstTag = text.substring(0, 6)
                        if (firstTag.all { it.isDigit() }) {
                            var offset = 0
                            while (offset + 6 <= text.length) {
                                val tag = text.substring(offset, offset + 6)
                                if (tag.all { it.isDigit() }) {
                                    assertEquals(
                                        "row 0 mixed two write generations within one snapshot",
                                        firstTag,
                                        tag,
                                    )
                                }
                                offset += 6
                            }
                        }
                    }
                }
            } catch (t: Throwable) {
                failure.compareAndSet(null, t)
            }
        }

        writer.start()
        reader.start()
        Thread.sleep(durationMillis)
        running.set(false)
        writer.join(5_000)
        reader.join(5_000)
        engine.close()

        failure.get()?.let { throw AssertionError("contention scenario failed", it) }
    }

    @Test
    fun encodeKey_matchesKeyByteEncoderTestFixtures() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            fun keyDown(code: Int, metaState: Int = 0): KeyEvent =
                KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, code, 0, metaState)

            assertArrayEquals(
                byteArrayOf(0x1b, 0x5b, 0x41),
                engine.encodeKey(keyDown(KeyEvent.KEYCODE_DPAD_UP), KeyByteEncoder.CursorMode.NORMAL),
            )
            assertArrayEquals(
                byteArrayOf(0x1b, 0x4f, 0x41),
                engine.encodeKey(keyDown(KeyEvent.KEYCODE_DPAD_UP), KeyByteEncoder.CursorMode.APPLICATION),
            )
            assertArrayEquals(
                byteArrayOf(0x03),
                engine.encodeKey(keyDown(KeyEvent.KEYCODE_C, KeyEvent.META_CTRL_ON)),
            )
            for (offset in 0..25) {
                val code = KeyEvent.KEYCODE_A + offset
                assertArrayEquals(
                    "letter offset $offset",
                    byteArrayOf((offset + 1).toByte()),
                    engine.encodeKey(keyDown(code, KeyEvent.META_CTRL_ON)),
                )
            }
            assertArrayEquals(byteArrayOf(0x09), engine.encodeKey(keyDown(KeyEvent.KEYCODE_TAB)))
            assertArrayEquals(byteArrayOf(0x1b), engine.encodeKey(keyDown(KeyEvent.KEYCODE_ESCAPE)))
            assertArrayEquals(byteArrayOf(0x0d), engine.encodeKey(keyDown(KeyEvent.KEYCODE_ENTER)))
            assertArrayEquals(byteArrayOf(0x7f), engine.encodeKey(keyDown(KeyEvent.KEYCODE_DEL)))
            assertArrayEquals(
                byteArrayOf(0x1b, 0x5b, 0x48),
                engine.encodeKey(keyDown(KeyEvent.KEYCODE_MOVE_HOME)),
            )
            assertArrayEquals(
                byteArrayOf(0x1b, 0x5b, 0x46),
                engine.encodeKey(keyDown(KeyEvent.KEYCODE_MOVE_END)),
            )
            assertArrayEquals(
                byteArrayOf(0x1b, '['.code.toByte(), '5'.code.toByte(), '~'.code.toByte()),
                engine.encodeKey(keyDown(KeyEvent.KEYCODE_PAGE_UP)),
            )
            assertArrayEquals(
                byteArrayOf(0x1b, '['.code.toByte(), '6'.code.toByte(), '~'.code.toByte()),
                engine.encodeKey(keyDown(KeyEvent.KEYCODE_PAGE_DOWN)),
            )
            assertNull(engine.encodeKey(keyDown(KeyEvent.KEYCODE_UNKNOWN)))
        } finally {
            engine.close()
        }
    }

    @Test
    fun exactlyOneBufferAllocatedPerEngineInstanceUnlessResized() {
        val engine = TerminalEngine.create(cols = 80, rows = 24)
        try {
            assertEquals(1, engine.debugBufferAllocationCount())
            engine.snapshot()
            engine.snapshot()
            engine.write("no-op for buffer count".toByteArray())
            engine.snapshot()
            assertEquals(
                "snapshot()/write() must never allocate a new buffer",
                1,
                engine.debugBufferAllocationCount(),
            )

            engine.resize(40, 24)
            assertEquals(
                "a dimension-changing resize is the only case allowed to reallocate",
                2,
                engine.debugBufferAllocationCount(),
            )
            assertFalse(false)
        } finally {
            engine.close()
        }
    }
}
