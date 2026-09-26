package dev.servercontrolpanel.feature.terminal.transport

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The pending-typing strip shows what was typed while the connection is down. It
 * must show exactly what will be sent, or the user reads one command and another runs.
 */
class PendingTypingTest {

    private fun until(vararg pieces: String): String {
        var acc = ""
        pieces.forEach { acc = typingSummary(acc, it.toByteArray(Charsets.UTF_8)) }
        return acc
    }

    @Test
    fun `plain text appears as typed`() {
        assertEquals("ls -la", until("l", "s", " ", "-", "l", "a"))
    }

    /** Backspace really deletes, so the strip never shows a command that will not be sent. */
    @Test
    fun `backspace removes the last character instead of becoming a symbol`() {
        val withBackspace = typingSummary("ls -laa", byteArrayOf(0x08))
        assertEquals("ls -la", withBackspace)

        val withDelete = typingSummary("ls -laa", byteArrayOf(0x7F))
        assertEquals("ls -la", withDelete)
    }

    @Test
    fun `backspace on empty text does not break`() {
        assertEquals("", typingSummary("", byteArrayOf(0x08)))
    }

    /** The strip is one line; a real newline would push the grid upwards. */
    @Test
    fun `enter becomes a symbol and does not break the line`() {
        val r = typingSummary("ls", byteArrayOf(0x0D))
        assertEquals("ls⏎", r)
        assertEquals(false, r.contains('\n'))
    }

    /** A queued `^C` changes what happens on reconnect, so the user must see it. */
    @Test
    fun `control characters use the usual caret notation`() {
        assertEquals("^C", typingSummary("", byteArrayOf(0x03)))
        assertEquals("^D", typingSummary("", byteArrayOf(0x04)))
    }

    /** Escape sequences (e.g. arrow keys) are still sent, but are not rendered in the strip. */
    @Test
    fun `an escape sequence does not become garbage in the strip`() {
        val arrowUp = byteArrayOf(0x1B, '['.code.toByte(), 'A'.code.toByte())
        assertEquals("ls", typingSummary("ls", arrowUp))
    }

    @Test
    fun `a lone escape also disappears`() {
        assertEquals("ls", typingSummary("ls", byteArrayOf(0x1B, 'O'.code.toByte())))
    }

    /** Decoding byte by byte would split the two UTF-8 bytes of "ï" into two wrong characters. */
    @Test
    fun `accents survive because UTF-8 is decoded whole`() {
        assertEquals("naïve", until("na", "ï", "ve"))
    }

    /** The end is where the cursor is, so long text is trimmed from the front. */
    @Test
    fun `long text is trimmed from the front with an ellipsis`() {
        val long = "x".repeat(200)
        val r = typingSummary("", long.toByteArray())

        assertEquals(true, r.startsWith("…"))
        assertEquals(121, r.length)
        assertEquals(true, r.endsWith("x"))
    }
}
