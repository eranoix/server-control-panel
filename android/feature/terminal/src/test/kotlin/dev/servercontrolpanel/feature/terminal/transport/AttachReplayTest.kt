package dev.servercontrolpanel.feature.terminal.transport

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Calibration of the non-reproducible replay detector. The thresholds come from
 * 30 real session logs over the same 128 KiB window the server re-emits (see
 * [AttachReplay]).
 */
class AttachReplayTest {

    private fun cuu(lines: String): ByteArray = "[${lines}A".toByteArray()

    private fun stream(times: Int, lines: String): ByteArray {
        val output = ArrayList<Byte>()
        repeat(times) {
            output.addAll("some line of text\r\n".toByteArray().toList())
            output.addAll(cuu(lines).toList())
        }
        return output.toByteArray()
    }

    @Test
    fun `append-only shell output is not a repaint`() {
        val shell = "$ ls -l\r\ntotal 4\r\ndrwxr-xr-x 2 root root 4096 dir\r\n$ ".toByteArray()
        assertEquals(0, AttachReplay.countBlockCuu(shell))
        assertFalse(AttachReplay.isDiffRepaint(shell))
    }

    @Test
    fun `a readline prompt redraw does not count as a repaint`() {
        // readline redraws a two-line prompt with `ESC[1A` and `ESC[A`; real shells
        // emitted up to 34 of these and none of two lines or more.
        val readline = ByteArray(0) +
            "[1A".toByteArray().let { one -> ByteArray(0) + List(40) { one.toList() }.flatten().toByteArray() } +
            "[A".toByteArray().let { one -> ByteArray(0) + List(40) { one.toList() }.flatten().toByteArray() }
        assertEquals(0, AttachReplay.countBlockCuu(readline))
        assertFalse(AttachReplay.isDiffRepaint(readline))
    }

    @Test
    fun `an empty or zero parameter means one line, not two`() {
        // ECMA-48: an omitted or 0 parameter means the default, which for CUU is 1.
        assertEquals(0, AttachReplay.countBlockCuu("[A[0A[A".toByteArray()))
        assertEquals(1, AttachReplay.countBlockCuu("[2A".toByteArray()))
    }

    @Test
    fun `a differential renderer is recognized`() {
        // The quietest real TUI had 33 multi-line CUUs in 128 KiB; 25 clears the
        // threshold of 20 and stays below that.
        val ink = stream(times = 25, lines = "7")
        assertTrue(AttachReplay.countBlockCuu(ink) >= AttachReplay.REPAINT_THRESHOLD)
        assertTrue(AttachReplay.isDiffRepaint(ink))
    }

    @Test
    fun `the measured gap between shell and TUI holds on both sides`() {
        // 11 = the worst real shell measured. 33 = the quietest real TUI measured.
        assertFalse(AttachReplay.isDiffRepaint(stream(times = 11, lines = "4")))
        assertTrue(AttachReplay.isDiffRepaint(stream(times = 33, lines = "4")))
    }

    @Test
    fun `a sequence truncated at the end of the block does not overflow`() {
        // The server cuts the replay at 128 KiB, so a sequence can be truncated;
        // scanning must not read past the end of the array.
        assertEquals(0, AttachReplay.countBlockCuu("text[12".toByteArray()))
        assertEquals(0, AttachReplay.countBlockCuu("text".toByteArray()))
        assertEquals(0, AttachReplay.countBlockCuu(ByteArray(0)))
    }

    @Test
    fun `an absurdly long parameter saturates instead of overflowing`() {
        val absurd = ("[" + "9".repeat(400) + "A").toByteArray()
        assertEquals(1, AttachReplay.countBlockCuu(absurd))
    }

    @Test
    fun `other CSI sequences ending in a different letter do not count`() {
        // `ESC[2J` (clear screen), `ESC[10B` (move down), `ESC[3C` (right).
        val others = "[2J[10B[3C[5D".toByteArray()
        assertEquals(0, AttachReplay.countBlockCuu(others))
    }
}
