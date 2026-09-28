package dev.servercontrolpanel.feature.terminal.keys

import android.view.KeyCharacterMap
import android.view.KeyEvent
import dev.servercontrolpanel.feature.terminal.input.ByteSink
import dev.servercontrolpanel.terminalengine.KeyByteEncoder

class HardwareKeyHandler(
    private val sink: ByteSink,
    var cursorMode: KeyByteEncoder.CursorMode = KeyByteEncoder.CursorMode.NORMAL,
    private val pendingModifiers: PendingModifiers? = null,
) {

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
