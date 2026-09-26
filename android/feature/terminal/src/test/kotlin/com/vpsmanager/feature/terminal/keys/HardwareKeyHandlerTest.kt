package com.vpsmanager.feature.terminal.keys

import android.view.KeyCharacterMap
import android.view.KeyEvent
import com.vpsmanager.feature.terminal.input.RecordingByteSink
import com.vpsmanager.terminalengine.KeyByteEncoder
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Drives [HardwareKeyHandler] with synthetic [KeyEvent]s carrying a
 * non-virtual `deviceId`, the same signal a real Bluetooth/USB keyboard's
 * events carry, to distinguish them from IME-synthesized ones (see the
 * handler's own doc comment).
 */
@RunWith(RobolectricTestRunner::class)
class HardwareKeyHandlerTest {

    private val hardwareDeviceId = 1

    private fun hardwareKeyDown(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, code, 0, metaState, hardwareDeviceId, 0)

    private fun hardwareKeyUp(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_UP, code, 0, metaState, hardwareDeviceId, 0)

    private fun virtualKeyDown(code: Int, metaState: Int = 0): KeyEvent =
        KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, code, 0, metaState, KeyCharacterMap.VIRTUAL_KEYBOARD, 0)

    @Test
    fun `a plain printable hardware key produces the same bytes KeyByteEncoder produces for it`() {
        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink)
        val event = hardwareKeyDown(KeyEvent.KEYCODE_A)
        val expected = KeyByteEncoder.encode(event)

        val consumed = handler.onKeyEvent(event)

        assertTrue("must claim a key it can map", consumed)
        assertArrayEquals(expected, sink.bytes())
    }

    @Test
    fun `ctrl a through ctrl z produce the C0 control byte range`() {
        for (offset in 0..25) {
            val sink = RecordingByteSink()
            val handler = HardwareKeyHandler(sink)
            val code = KeyEvent.KEYCODE_A + offset
            handler.onKeyEvent(hardwareKeyDown(code, KeyEvent.META_CTRL_ON))
            assertArrayEquals(
                "letter offset $offset",
                byteArrayOf((offset + 1).toByte()),
                sink.bytes(),
            )
        }
    }

    @Test
    fun `alt plus a key produces the ESC-prefixed sequence KeyByteEncoder already defines`() {
        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink)
        val event = hardwareKeyDown(KeyEvent.KEYCODE_F, KeyEvent.META_ALT_ON)
        val expected = KeyByteEncoder.encode(event)

        handler.onKeyEvent(event)

        assertArrayEquals(expected, sink.bytes())
    }

    @Test
    fun `a bare modifier key-down with no second key produces no output`() {
        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink)

        val consumedCtrl = handler.onKeyEvent(hardwareKeyDown(KeyEvent.KEYCODE_CTRL_LEFT, KeyEvent.META_CTRL_ON))
        val consumedAlt = handler.onKeyEvent(hardwareKeyDown(KeyEvent.KEYCODE_ALT_LEFT, KeyEvent.META_ALT_ON))

        assertFalse("bare Ctrl must not be claimed as a mapped key", consumedCtrl)
        assertFalse("bare Alt must not be claimed as a mapped key", consumedAlt)
        assertTrue("no bytes for a modifier-only press", sink.isEmpty())
    }

    @Test
    fun `arrow keys and navigation keys are consumed and forwarded`() {
        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink)
        val event = hardwareKeyDown(KeyEvent.KEYCODE_DPAD_UP)
        val expected = KeyByteEncoder.encode(event)

        val consumed = handler.onKeyEvent(event)

        assertTrue(consumed)
        assertArrayEquals(expected, sink.bytes())
    }

    @Test
    fun `key-up of a mapped key is consumed but sends nothing on its own`() {
        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink)

        val consumed = handler.onKeyEvent(hardwareKeyUp(KeyEvent.KEYCODE_A))

        assertTrue("still claims the event so it does not leak elsewhere", consumed)
        assertTrue("no duplicate send on key-up", sink.isEmpty())
    }

    @Test
    fun `an unmapped system key is left unconsumed for the system to handle`() {
        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink)

        val consumed = handler.onKeyEvent(hardwareKeyDown(KeyEvent.KEYCODE_VOLUME_UP))

        assertFalse("volume keys are not a terminal concern", consumed)
        assertTrue(sink.isEmpty())
    }

    @Test
    fun `an IME-synthesized (virtual) key event is ignored -- TerminalInputConnection owns it instead`() {
        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink)

        val consumed = handler.onKeyEvent(virtualKeyDown(KeyEvent.KEYCODE_A))

        assertFalse("virtual-keyboard-origin events must not be double-handled here", consumed)
        assertTrue(sink.isEmpty())
    }
}
