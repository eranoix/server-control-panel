package com.vpsmanager.feature.terminal.transport

/**
 * Observable state of a [TerminalSocketClient]'s `/ws/shell` connection — a
 * screen must never infer "stuck" from silence, so every transition this
 * client can make is a distinct, renderable value here.
 */
sealed interface ConnectionState {
    /** Never connected yet, or reconnecting is about to start a brand-new attempt. */
    data object Connecting : ConnectionState

    /** Socket open, bytes flowing both ways. */
    data object Live : ConnectionState

    /** An unexpected drop is being retried; [attempt] is 1 on the first retry. */
    data class Reconnecting(val attempt: Int) : ConnectionState

    /** [TerminalSocketClient.disconnect] was called explicitly — never auto-reconnects from here. */
    data object Disconnected : ConnectionState

    /** Reserved for a terminal failure outside the retry loop (e.g. malformed ticket response). */
    data class Failed(val reason: String) : ConnectionState

    /** The server closed with WS code 4404 — the session itself is gone; retrying would just repeat this. */
    data object SessionEnded : ConnectionState
}
