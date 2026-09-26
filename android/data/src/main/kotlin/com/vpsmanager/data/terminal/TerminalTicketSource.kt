package com.vpsmanager.data.terminal

/**
 * Outcome of `POST /api/mobile/v1/terminal/ws-ticket`. Never `Empty` — a
 * successful call always returns a usable one-shot ticket.
 */
sealed interface WsTicketResult {
    data class Success(val ticket: String, val expiresIn: Int) : WsTicketResult
    data class Error(val reason: String) : WsTicketResult
}

/**
 * The narrow slice of [TerminalRepository] that `:feature-terminal`'s
 * `TerminalSocketClient` depends on: a fresh, never-reused ticket for every
 * connect/reconnect attempt. Kept as its own interface (rather than a direct
 * dependency on the concrete [TerminalRepository]) purely so tests can supply
 * a fake without touching the generated client or the network — production
 * code always passes a real [TerminalRepository].
 */
interface TerminalTicketSource {
    suspend fun wsTicket(name: String): WsTicketResult
}
