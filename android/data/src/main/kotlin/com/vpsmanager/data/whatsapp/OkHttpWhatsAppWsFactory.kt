package com.vpsmanager.data.whatsapp

import com.vpsmanager.mobileapiclient.infrastructure.ApiClient
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener

/**
 * Production [WhatsAppWebSocketFactory]: opens a real OkHttp WebSocket
 * against `/ws/whatsapp`. Reuses [ApiClient.defaultClient] -- the exact same
 * shared `OkHttpClient` (connection pool, configured interceptors) every
 * generated BFF call already goes through -- rather than building a second
 * HTTP client, and attaches [ApiClient.accessToken] (the companion-level
 * value every generated `ApiClient` instance's own `accessTokenProvider`
 * hook defaults to reading) via a plain `Authorization: Bearer` header
 * (`internal/whatsapp/ws.go` accepts this for a native client, unlike a
 * browser WS). That value is written by
 * [com.vpsmanager.data.auth.SessionManager] on every session change (login,
 * renewal, logout) -- before a login flow existed it was always null and the
 * socket opened with no credential. It is read on every `open()`, so a
 * reconnection after a renewal already carries the new token. The 25s ping /
 * 45s pong keepalive is server-driven (`internal/whatsapp/ws.go`) -- OkHttp
 * answers protocol-level pings on its own, nothing to configure here.
 */
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
