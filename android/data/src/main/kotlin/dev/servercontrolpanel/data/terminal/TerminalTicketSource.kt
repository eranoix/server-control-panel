package dev.servercontrolpanel.data.terminal

sealed interface WsTicketResult {
    data class Success(val ticket: String, val expiresIn: Int) : WsTicketResult
    data class Error(val reason: String) : WsTicketResult
}

interface TerminalTicketSource {
    suspend fun wsTicket(name: String): WsTicketResult
}
