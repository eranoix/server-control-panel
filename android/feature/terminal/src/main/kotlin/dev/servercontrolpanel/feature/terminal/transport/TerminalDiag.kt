package dev.servercontrolpanel.feature.terminal.transport

object TerminalDiag {
    const val TAG: String = "PanelTermAttach"

    @Volatile
    var enabled: Boolean = true

    fun log(message: String) {
        if (!enabled) return
        runCatching { android.util.Log.i(TAG, message) }
    }
}
