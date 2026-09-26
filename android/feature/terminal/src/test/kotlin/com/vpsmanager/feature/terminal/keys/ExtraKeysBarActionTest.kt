package com.vpsmanager.feature.terminal.keys

import android.view.KeyEvent
import com.vpsmanager.feature.terminal.input.RecordingByteSink
import com.vpsmanager.terminalengine.KeyByteEncoder
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Drives [PendingModifiers]' sticky OFF/ARMED/LOCKED cycle and
 * [tapExtraKey] (the row's own direct-tap encoding path), plus the
 * interaction between a sticky modifier and a genuinely physical one
 * arriving through [HardwareKeyHandler].
 */
@RunWith(RobolectricTestRunner::class)
class ExtraKeysBarActionTest {

    @Test
    fun `tapping ctrl once arms it`() {
        val pending = PendingModifiers()
        pending.tapCtrl()
        assertEquals(ModifierArmState.ARMED, pending.ctrl)
    }

    @Test
    fun `tapping ctrl twice locks it`() {
        val pending = PendingModifiers()
        pending.tapCtrl()
        pending.tapCtrl()
        assertEquals(ModifierArmState.LOCKED, pending.ctrl)
    }

    @Test
    fun `tapping ctrl a third time releases it back to off`() {
        val pending = PendingModifiers()
        pending.tapCtrl()
        pending.tapCtrl()
        pending.tapCtrl()
        assertEquals(ModifierArmState.OFF, pending.ctrl)
    }

    @Test
    fun `ctrl and alt cycle independently and combine`() {
        val pending = PendingModifiers()
        pending.tapCtrl() // ARMED
        pending.tapAlt() // ARMED
        pending.tapAlt() // LOCKED -- must not affect ctrl
        assertEquals(ModifierArmState.ARMED, pending.ctrl)
        assertEquals(ModifierArmState.LOCKED, pending.alt)

        val sink = mutableListOf<ByteArray>()
        tapExtraKey(KeyEvent.KEYCODE_MINUS, pending, KeyByteEncoder.CursorMode.NORMAL) { sink.add(it) }

        val expected = KeyByteEncoder.encode(
            KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_MINUS, 0, KeyEvent.META_CTRL_ON or KeyEvent.META_ALT_ON),
        )
        assertArrayEquals(expected, sink.single())
    }

    @Test
    fun `armed sticky is consumed after exactly one keystroke from a direct tap`() {
        val pending = PendingModifiers()
        pending.tapCtrl() // ARMED

        val sink = mutableListOf<ByteArray>()
        tapExtraKey(KeyEvent.KEYCODE_MINUS, pending, KeyByteEncoder.CursorMode.NORMAL) { sink.add(it) }

        assertEquals(ModifierArmState.OFF, pending.ctrl)
        assertTrue(sink.single().isNotEmpty())
    }

    @Test
    fun `locked sticky persists across multiple keystrokes until manually released`() {
        val pending = PendingModifiers()
        pending.tapCtrl()
        pending.tapCtrl() // LOCKED

        val sink = mutableListOf<ByteArray>()
        tapExtraKey(KeyEvent.KEYCODE_MINUS, pending, KeyByteEncoder.CursorMode.NORMAL) { sink.add(it) }
        tapExtraKey(KeyEvent.KEYCODE_TAB, pending, KeyByteEncoder.CursorMode.NORMAL) { sink.add(it) }

        assertEquals(ModifierArmState.LOCKED, pending.ctrl)
        assertEquals(2, sink.size)
    }

    @Test
    fun `a bare arrow tap with no pending modifier sends the plain navigation bytes`() {
        val pending = PendingModifiers()
        val sink = mutableListOf<ByteArray>()

        tapExtraKey(KeyEvent.KEYCODE_DPAD_UP, pending, KeyByteEncoder.CursorMode.NORMAL) { sink.add(it) }

        val expected = KeyByteEncoder.encode(KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DPAD_UP, 0, 0))
        assertArrayEquals(expected, sink.single())
    }

    @Test
    fun `a hardware ctrl press while a sticky ctrl is pending still produces one correct ctrl chord`() {
        val pending = PendingModifiers()
        pending.tapCtrl() // ARMED, independently of any hardware key

        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink, pendingModifiers = pending)
        // A genuine hardware Ctrl+C: the physical event ALREADY carries
        // META_CTRL_ON on its own, on top of the independently-armed sticky.
        val event = KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_C, 0, KeyEvent.META_CTRL_ON, 1, 0)

        val consumed = handler.onKeyEvent(event)

        assertTrue(consumed)
        // Ctrl+C is byte 0x03 regardless of whether Ctrl came from hardware,
        // the sticky arm, or (as here) both at once -- OR-ing is idempotent.
        assertArrayEquals(byteArrayOf(0x03), sink.bytes())
        assertEquals("a real keystroke still consumes the one-shot arm", ModifierArmState.OFF, pending.ctrl)
    }

    @Test
    fun `a bare hardware ctrl press with no second key does not consume a pending sticky arm`() {
        val pending = PendingModifiers()
        pending.tapCtrl() // ARMED

        val sink = RecordingByteSink()
        val handler = HardwareKeyHandler(sink, pendingModifiers = pending)
        val bareCtrl = KeyEvent(0L, 0L, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_CTRL_LEFT, 0, KeyEvent.META_CTRL_ON, 1, 0)

        handler.onKeyEvent(bareCtrl)

        assertEquals("bare modifier press must not waste the pending sticky arm", ModifierArmState.ARMED, pending.ctrl)
        assertTrue(sink.isEmpty())
    }
}
