package dev.servercontrolpanel.feature.terminal.transport

sealed interface ConnectionState {
    data object Connecting : ConnectionState

    data object Live : ConnectionState

    data class Reconnecting(val attempt: Int) : ConnectionState

    data object Disconnected : ConnectionState

    data class Failed(val reason: String) : ConnectionState

    data object SessionEnded : ConnectionState
}
