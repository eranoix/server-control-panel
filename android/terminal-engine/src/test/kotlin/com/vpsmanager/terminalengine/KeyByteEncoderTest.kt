package com.vpsmanager.terminalengine

import android.view.KeyEvent
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class KeyByteEncoderTest {

    private fun keyDown(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, code, 0, metaState)

    @Test
    fun `arrow up encodes to ESC bracket A in normal cursor mode`() {
        val bytes = KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_DPAD_UP))
        assertArrayEquals(byteArrayOf(0x1b, 0x5b, 0x41), bytes)
    }

    @Test
    fun `arrow up encodes to ESC O A in application cursor mode`() {
        val bytes = KeyByteEncoder.encode(
            keyDown(KeyEvent.KEYCODE_DPAD_UP),
            KeyByteEncoder.CursorMode.APPLICATION,
        )
        assertArrayEquals(byteArrayOf(0x1b, 0x4f, 0x41), bytes)
    }

    @Test
    fun `ctrl c encodes to 0x03`() {
        val bytes = KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_C, KeyEvent.META_CTRL_ON))
        assertArrayEquals(byteArrayOf(0x03), bytes)
    }

    @Test
    fun `ctrl a through ctrl z cover the full 0x01 to 0x1a range`() {
        for (offset in 0..25) {
            val code = KeyEvent.KEYCODE_A + offset
            val bytes = KeyByteEncoder.encode(keyDown(code, KeyEvent.META_CTRL_ON))
            assertArrayEquals(
                "letter offset $offset",
                byteArrayOf((offset + 1).toByte()),
                bytes,
            )
        }
    }

    @Test
    fun `alt f encodes to ESC f`() {
        val bytes = KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_F, KeyEvent.META_ALT_ON))
        assertArrayEquals(byteArrayOf(0x1b, 0x66), bytes)
    }

    @Test
    fun `tab encodes to 0x09`() {
        assertArrayEquals(byteArrayOf(0x09), KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_TAB)))
    }

    @Test
    fun `escape encodes to 0x1b`() {
        assertArrayEquals(byteArrayOf(0x1b), KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_ESCAPE)))
    }

    @Test
    fun `enter encodes to 0x0d`() {
        assertArrayEquals(byteArrayOf(0x0d), KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_ENTER)))
    }

    @Test
    fun `backspace encodes to 0x7f`() {
        assertArrayEquals(byteArrayOf(0x7f), KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_DEL)))
    }

    @Test
    fun `home and end encode to xterm sequences`() {
        assertArrayEquals(
            byteArrayOf(0x1b, 0x5b, 0x48),
            KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_MOVE_HOME)),
        )
        assertArrayEquals(
            byteArrayOf(0x1b, 0x5b, 0x46),
            KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_MOVE_END)),
        )
    }

    @Test
    fun `page up and page down encode to xterm tilde sequences`() {
        assertArrayEquals(
            "ESC [ 5 ~".toByteArray(Charsets.US_ASCII).let { byteArrayOf(0x1b, '['.code.toByte(), '5'.code.toByte(), '~'.code.toByte()) },
            KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_PAGE_UP)),
        )
        assertArrayEquals(
            byteArrayOf(0x1b, '['.code.toByte(), '6'.code.toByte(), '~'.code.toByte()),
            KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_PAGE_DOWN)),
        )
    }

    @Test
    fun `a bare printable hardware key with no modifiers and no unicode mapping returns null`() {
        // A key code with neither a control mapping nor a resolvable unicode
        // char (unmapped in Robolectric's default virtual keyboard layout)
        // must return null so the caller can decide what, if anything, to do.
        assertNull(KeyByteEncoder.encode(keyDown(KeyEvent.KEYCODE_UNKNOWN)))
    }
}
