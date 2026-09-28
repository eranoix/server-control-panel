package dev.servercontrolpanel.data.terminal

interface TerminalWebSocket {
    fun sendBytes(bytes: ByteArray): Boolean

    fun sendText(text: String): Boolean

    fun close(code: Int, reason: String): Boolean
}

interface TerminalWebSocketListener {
    fun onOpen()
    fun onBinaryMessage(bytes: ByteArray)

    fun onTextMessage(text: String) {}
    fun onClosed(code: Int, reason: String)
    fun onFailure(reason: String)
}

fun interface TerminalWebSocketFactory {
    fun open(url: String, listener: TerminalWebSocketListener): TerminalWebSocket
}
