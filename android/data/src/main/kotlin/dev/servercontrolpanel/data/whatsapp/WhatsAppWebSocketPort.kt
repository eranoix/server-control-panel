package dev.servercontrolpanel.data.whatsapp

interface WhatsAppWebSocket {
    fun close(code: Int, reason: String): Boolean
}

interface WhatsAppWebSocketListener {
    fun onOpen()
    fun onTextMessage(text: String)
    fun onClosed(code: Int, reason: String)
    fun onFailure(reason: String)
}

fun interface WhatsAppWebSocketFactory {
    fun open(url: String, listener: WhatsAppWebSocketListener): WhatsAppWebSocket
}
