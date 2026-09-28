package dev.servercontrolpanel.data.whatsapp

import dev.servercontrolpanel.mobileapiclient.infrastructure.ApiClient
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

class OkHttpWhatsAppWsFactory(
    private val client: OkHttpClient = ApiClient.defaultClient,
    private val accessTokenProvider: () -> String? = { ApiClient.accessToken },
) : WhatsAppWebSocketFactory {

    override fun open(url: String, listener: WhatsAppWebSocketListener): WhatsAppWebSocket {
        val requestBuilder = Request.Builder().url(url)
        accessTokenProvider()?.let { token -> requestBuilder.header("Authorization", "Bearer $token") }
        val socket = client.newWebSocket(
            requestBuilder.build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    listener.onOpen()
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    listener.onTextMessage(text)
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    listener.onClosed(code, reason)
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    listener.onFailure(t.message ?: t::class.simpleName ?: "unknown failure")
                }
            },
        )
        return object : WhatsAppWebSocket {
            override fun close(code: Int, reason: String): Boolean = socket.close(code, reason)
        }
    }
}
