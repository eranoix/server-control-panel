package com.vpsmanager.terminalengine

import android.view.KeyEvent

/**
 * Maps a hardware/IME-synthesized [KeyEvent] to the outbound terminal byte
 * sequence it represents. Pure keyCode + metaState table — no PTY, no
 * networking, no native calls.
 *
 * This table stays a pure Kotlin function so it keeps running under
 * Robolectric, where a bionic-ABI native library cannot load. The real
 * terminal-library key encoder lives behind [TerminalEngine.encodeKey]
 * instead, exercised only by the instrumented suite where the native
 * library is actually available; this object never calls into it.
 *
 * Ctrl+letter and the plain control keys are resolved from [KeyEvent.getKeyCode]
 * rather than [KeyEvent.getUnicodeChar], because those combinations must mean
 * the same byte on every physical keyboard layout. Alt+key and bare printable
 * keys fall back to the layout's own unicode mapping, since there is no
 * layout-independent notion of "the letter under this key" beyond that.
 */
object KeyByteEncoder {

    enum class CursorMode { NORMAL, APPLICATION }

    private const val ESC = 0x1b.toByte()

    fun encode(event: KeyEvent, cursorMode: CursorMode = CursorMode.NORMAL): ByteArray? {
        val code = event.keyCode
        val ctrl = event.isCtrlPressed
        val alt = event.isAltPressed

        if (ctrl && code in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z) {
            return byteArrayOf((code - KeyEvent.KEYCODE_A + 1).toByte())
        }

        val base = navigationBytes(code, cursorMode)
            ?: controlKeyBytes(code)
            ?: printableFallback(event)
            ?: return null

        return if (alt) byteArrayOf(ESC, *base) else base
    }

    private fun navigationBytes(code: Int, cursorMode: CursorMode): ByteArray? {
        val applicationMode = cursorMode == CursorMode.APPLICATION
        return when (code) {
            KeyEvent.KEYCODE_DPAD_UP -> if (applicationMode) esc("OA") else esc("[A")
            KeyEvent.KEYCODE_DPAD_DOWN -> if (applicationMode) esc("OB") else esc("[B")
            KeyEvent.KEYCODE_DPAD_RIGHT -> if (applicationMode) esc("OC") else esc("[C")
            KeyEvent.KEYCODE_DPAD_LEFT -> if (applicationMode) esc("OD") else esc("[D")
            KeyEvent.KEYCODE_MOVE_HOME -> esc("[H")
            KeyEvent.KEYCODE_MOVE_END -> esc("[F")
            KeyEvent.KEYCODE_PAGE_UP -> esc("[5~")
            KeyEvent.KEYCODE_PAGE_DOWN -> esc("[6~")
            KeyEvent.KEYCODE_F1 -> esc("OP")
            KeyEvent.KEYCODE_F2 -> esc("OQ")
            KeyEvent.KEYCODE_F3 -> esc("OR")
            KeyEvent.KEYCODE_F4 -> esc("OS")
            KeyEvent.KEYCODE_F5 -> esc("[15~")
            KeyEvent.KEYCODE_F6 -> esc("[17~")
            KeyEvent.KEYCODE_F7 -> esc("[18~")
            KeyEvent.KEYCODE_F8 -> esc("[19~")
            KeyEvent.KEYCODE_F9 -> esc("[20~")
            KeyEvent.KEYCODE_F10 -> esc("[21~")
            KeyEvent.KEYCODE_F11 -> esc("[23~")
            KeyEvent.KEYCODE_F12 -> esc("[24~")
            else -> null
        }
    }

    private fun controlKeyBytes(code: Int): ByteArray? = when (code) {
        KeyEvent.KEYCODE_TAB -> byteArrayOf(0x09)
        KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> byteArrayOf(0x0d)
        KeyEvent.KEYCODE_ESCAPE -> byteArrayOf(0x1b)
        KeyEvent.KEYCODE_DEL -> byteArrayOf(0x7f)
        KeyEvent.KEYCODE_FORWARD_DEL -> esc("[3~")
        else -> null
    }

    /**
     * Bare printable keys (letters, digits, punctuation) typed on a hardware
     * keyboard with no IME composition involved. Duplication against a
     * commitText-based IME path is guarded by the caller
     * (TerminalInputConnection), not here.
     */
    private fun printableFallback(event: KeyEvent): ByteArray? {
        val unicode = event.unicodeChar
        if (unicode == 0) return null
        return String(Character.toChars(unicode)).toByteArray(Charsets.UTF_8)
    }

    private fun esc(rest: String): ByteArray = byteArrayOf(ESC, *rest.toByteArray(Charsets.US_ASCII))
}
