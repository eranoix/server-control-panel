package dev.servercontrolpanel.feature.terminal.transport

import org.junit.Assert.assertEquals
import org.junit.Test

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

    @Test
    fun `enter becomes a symbol and does not break the line`() {
        val r = typingSummary("ls", byteArrayOf(0x0D))
        assertEquals("ls⏎", r)
        assertEquals(false, r.contains('\n'))
    }

    @Test
    fun `control characters use the usual caret notation`() {
        assertEquals("^C", typingSummary("", byteArrayOf(0x03)))
        assertEquals("^D", typingSummary("", byteArrayOf(0x04)))
    }

    @Test
    fun `an escape sequence does not become garbage in the strip`() {
        val arrowUp = byteArrayOf(0x1B, '['.code.toByte(), 'A'.code.toByte())
        assertEquals("ls", typingSummary("ls", arrowUp))
    }

    @Test
    fun `a lone escape also disappears`() {
        assertEquals("ls", typingSummary("ls", byteArrayOf(0x1B, 'O'.code.toByte())))
    }

    @Test
    fun `accents survive because UTF-8 is decoded whole`() {
        assertEquals("naïve", until("na", "ï", "ve"))
    }

    @Test
    fun `long text is trimmed from the front with an ellipsis`() {
        val long = "x".repeat(200)
        val r = typingSummary("", long.toByteArray())

        assertEquals(true, r.startsWith("…"))
        assertEquals(121, r.length)
        assertEquals(true, r.endsWith("x"))
    }
}
