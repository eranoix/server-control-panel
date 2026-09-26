package com.vpsmanager.data.whatsapp

/**
 * A single live `/ws/whatsapp` connection, abstracted away from the concrete
 * OkHttp WebSocket type underneath. `:feature-whatsapp` is not allowed to
 * import `okhttp3.*` directly (the `BffOnlyNetworkPlugin` gate) --
 * this is the pure-Kotlin seam it holds instead, satisfied in production by
 * [OkHttpWhatsAppWsFactory] and by a fake in tests.
 */
interface WhatsAppWebSocket {
    /** Requests a graceful close with the given WS close code/reason. */
    fun close(code: Int, reason: String): Boolean
}

/** Callbacks a [WhatsAppWebSocket] delivers to whoever opened it. */
interface WhatsAppWebSocketListener {
    fun onOpen()
    fun onTextMessage(text: String)
    fun onClosed(code: Int, reason: String)
    fun onFailure(reason: String)
}

/**
 * Opens a [WhatsAppWebSocket] against [url], delivering events to
 * [listener]. [OkHttpWhatsAppWsFactory] is the only production
 * implementation and the only place in the app that constructs a real OkHttp
 * WebSocket for `/ws/whatsapp`; tests substitute a fake factory that never
 * touches the network.
 */
fun interface WhatsAppWebSocketFactory {
    fun open(url: String, listener: WhatsAppWebSocketListener): WhatsAppWebSocket
}
