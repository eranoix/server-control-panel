package com.vpsmanager.feature.terminal.keys

import android.view.KeyCharacterMap
import android.view.KeyEvent
import com.vpsmanager.feature.terminal.input.ByteSink
import com.vpsmanager.terminalengine.KeyByteEncoder

/**
 * Adapts [KeyEvent]s from a genuinely physical (Bluetooth/USB) keyboard into
 * terminal bytes, reusing [KeyByteEncoder] — the exact same encoder table
 * `TerminalInputConnection`'s IME path already calls. No second key->bytes
 * table is written here.
 *
 * Wired via `View.setOnKeyListener` on `TerminalInputView` from `TerminalRoute`,
 * not by subclassing/overriding `TerminalInputView` itself: `View.dispatchKeyEvent`
 * is the pathway a real hardware keyboard's [KeyEvent]s travel through, but the
 * SAME pathway also carries [KeyEvent]s an IME synthesizes via
 * `InputMethodManager.dispatchKeyEventFromInputMethod` (some soft keyboards
 * simulate raw key presses instead of calling `commitText`). Those two origins
 * are indistinguishable by `keyCode`/`metaState` alone, so this handler filters
 * on [KeyEvent.getDeviceId]: a genuine physical device reports its own
 * non-negative device id, while every software-originated event carries
 * [KeyCharacterMap.VIRTUAL_KEYBOARD] (-1). Events that fail that check are left
 * untouched here — `TerminalInputConnection.sendKeyEvent` (with its
 * IME-commit dedup guard) is the path that owns those, so nothing is
 * double-sent to [sink].
 *
 * [pendingModifiers], when supplied, merges [ExtraKeysBar]'s sticky Ctrl/Alt
 * state into this event's effective ctrl/alt flags BEFORE encoding — so a
 * sticky modifier armed from the on-screen row applies to the next physical
 * keystroke too, not only to the row's own direct key taps. A bare hardware
 * modifier press (Ctrl/Alt alone, no second key) never reaches
 * [PendingModifiers.consumeAfterKeystroke] since [KeyByteEncoder] maps
 * nothing for it — see that method's own doc comment for why that means a
 * bare hardware modifier press never "wastes" a pending sticky arm.
 */
class HardwareKeyHandler(
    private val sink: ByteSink,
    var cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
    private val pendingModifiers: PendingModifiers? = null,
) {

    /**
     * @return true if this event belongs to the terminal (claimed, whether or
     *   not it produced bytes) so the view hierarchy does not also try to
     *   act on it; false to let the system/other handlers process it — e.g.
     *   volume keys, or a software-keyboard-origin event this handler
     *   deliberately ignores.
     */
    fun onKeyEvent(event: KeyEvent): Boolean {
        if (event.deviceId == KeyCharacterMap.VIRTUAL_KEYBOARD) return false

        val effective = withPendingModifiers(event)
        val bytes = KeyByteEncoder.encode(effective, cursorMode) ?: return false
        if (event.action == KeyEvent.ACTION_DOWN) {
            sink.send(bytes)
            pendingModifiers?.consumeAfterKeystroke()
        }
        return true
    }

    /**
     * Returns [event] unchanged unless a sticky modifier is pending and not
     * already reflected in the event's own metaState (a real physical
     * Ctrl/Alt press already sets these bits; OR-ing in the sticky ones is
     * therefore idempotent and never double-applies anything).
     */
    private fun withPendingModifiers(event: KeyEvent): KeyEvent {
        val pending = pendingModifiers ?: return event
        var metaState = event.metaState
        if (pending.isCtrlPending()) metaState = metaState or KeyEvent.META_CTRL_ON
        if (pending.isAltPending()) metaState = metaState or KeyEvent.META_ALT_ON
        if (metaState == event.metaState) return event
        return KeyEvent(
            event.downTime,
            event.eventTime,
            event.action,
            event.keyCode,
            event.repeatCount,
            metaState,
            event.deviceId,
            event.scanCode,
            event.flags,
            event.source,
        )
    }
}
