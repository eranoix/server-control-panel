package com.vpsmanager.feature.terminal.mouse

/**
 * Who a gesture on the grid belongs to: the remote program or the app.
 *
 * **This stopped being a preference.** There used to be a two-position
 * `MouseTouchPreference` here ("the touch belongs to the program" / "always
 * select"), the last residue of the manual mouse switch. It was removed at the
 * app owner's request, and the reason stands up to scrutiny: it was a
 * preference about a rare case (the program ASKED for the mouse and the
 * operator wants to copy text anyway) that forced the person to understand the
 * terminal's mouse model — DECSET 1000/1002/1003 — just to decide. Anyone who
 * needs to select inside `htop` still has the **long press**, which anchors the
 * selection and does not go through the routing below.
 *
 * What is left is not a choice, it is the right answer: the emulator's REAL
 * state decides. The program asked for mouse tracking ⇒ the gesture is its;
 * it did not ask ⇒ the gesture belongs to the app (selection, scrolling,
 * keyboard). It is the same decision Termux takes in
 * `TerminalView.onSingleTapUp` from `isMouseTrackingActive()`.
 *
 * The value comes from `TerminalEngine.modes().mouseTracking`, read on every
 * gesture — never from a stored copy, because the program switches the mode on
 * and off mid-session (entering and leaving `htop`) and a copy would go stale
 * in silence.
 */
fun interface RoteamentoDeToque {

    /** Is the remote program asking for mouse events RIGHT NOW? */
    fun programaPedeMouse(): Boolean

    /** The gesture belongs to the app: text selection, scrolling, keyboard. The normal case. */
    fun toqueEDoApp(): Boolean = !programaPedeMouse()
}
