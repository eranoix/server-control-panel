package com.vpsmanager.feature.terminal.keys

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue

/**
 * One sticky modifier's tap cycle: `OFF -> ARMED -> LOCKED -> OFF`. `ARMED`
 * applies to exactly the next keystroke and then falls back to `OFF`
 * ([PendingModifiers.consumeAfterKeystroke]); `LOCKED` keeps applying to
 * every keystroke until the chip is tapped again.
 */
enum class ModifierArmState {
    OFF,
    ARMED,
    LOCKED,
    ;

    fun next(): ModifierArmState = when (this) {
        OFF -> ARMED
        ARMED -> LOCKED
        LOCKED -> OFF
    }
}

/**
 * Sticky Ctrl/Alt state for [ExtraKeysBar]. Ctrl and Alt cycle
 * INDEPENDENTLY — arming one never touches the other, so they combine freely
 * (e.g. arm both to send Ctrl+Alt+something).
 *
 * A pending modifier here is merged into a [android.view.KeyEvent]'s
 * effective ctrl/alt flags BEFORE [com.vpsmanager.terminalengine.KeyByteEncoder.encode]
 * runs (see [HardwareKeyHandler] and `ExtraKeysBar`'s own direct-tap
 * encoding) rather than by transforming already-encoded output bytes: a
 * byte-level decorator cannot tell "this payload is ESC-prefixed for its own
 * reason" (an arrow-key escape sequence, or a hardware event that was
 * already Alt-pressed) apart from "needs an Alt ESC-prefix added," which
 * risks a double-ESC bug. Merging at the [android.view.KeyEvent] level lets
 * the encoder's own `alt`/`ctrl` branches make that call exactly once.
 *
 * Backed by [mutableStateOf] so a Composable reading [ctrl]/[alt] recomposes
 * on every tap; nothing here requires an active composition to read or
 * mutate correctly (used directly, with no Compose runtime installed, by
 * [HardwareKeyHandler] and its unit tests).
 *
 * Deliberately out of scope: characters committed by the system IME's own
 * `commitText` path (plain typing through the soft keyboard) are NOT
 * retroactively modified by a pending sticky modifier — doing so would
 * require touching `TerminalInputConnection`'s dedup-guard-critical
 * `commitText` path, which this feature does not touch. Sticky Ctrl/Alt
 * apply to hardware-keyboard keystrokes and this row's own direct key taps.
 */
class PendingModifiers {
    var ctrl: ModifierArmState by mutableStateOf(ModifierArmState.OFF)
        private set

    var alt: ModifierArmState by mutableStateOf(ModifierArmState.OFF)
        private set

    fun tapCtrl() {
        ctrl = ctrl.next()
    }

    fun tapAlt() {
        alt = alt.next()
    }

    fun isCtrlPending(): Boolean = ctrl != ModifierArmState.OFF

    fun isAltPending(): Boolean = alt != ModifierArmState.OFF

    /**
     * Called exactly once per keystroke that a pending modifier was merged
     * into (whether or not that keystroke actually produced bytes is the
     * caller's concern — callers only invoke this after a real, mapped
     * keystroke went out). `ARMED` is one-shot and drops back to `OFF`;
     * `LOCKED` persists across any number of keystrokes until manually
     * cycled off.
     *
     * Note this fires for ANY keystroke while a modifier is pending, not
     * only ones where the sticky modifier was the sole source of Ctrl/Alt —
     * a hardware Ctrl+C typed while sticky Ctrl also happens to be ARMED
     * still consumes the one-shot arm. What a bare hardware modifier
     * press alone (Ctrl with no second key) never does is call this at all,
     * since [HardwareKeyHandler] only calls it after a key that
     * [com.vpsmanager.terminalengine.KeyByteEncoder] actually mapped —
     * so a bare modifier press never "wastes" a pending sticky arm.
     */
    fun consumeAfterKeystroke() {
        if (ctrl == ModifierArmState.ARMED) ctrl = ModifierArmState.OFF
        if (alt == ModifierArmState.ARMED) alt = ModifierArmState.OFF
    }
}
