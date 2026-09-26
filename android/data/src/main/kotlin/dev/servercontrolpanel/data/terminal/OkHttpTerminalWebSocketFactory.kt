package dev.servercontrolpanel.data.terminal

import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString

/**
 * Production [TerminalWebSocketFactory]: opens a real OkHttp WebSocket
 * against `/ws/shell`. The 25s ping / 45s pong keepalive is server-driven
 * (`internal/pty/pty.go`) — OkHttp answers protocol-level pings on its own,
 * nothing to configure here.
 */
class OkHttpTerminalWebSocketFactory(
    private val client: OkHttpClient = OkHttpClient(),
) : TerminalWebSocketFactory {

    override fun open(url: String, listener: TerminalWebSocketListener): TerminalWebSocket {
        val request = Request.Builder().url(url).build()
        val socket = client.newWebSocket(
            request,
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    listener.onOpen()
                }

                override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                    listener.onBinaryMessage(bytes.toByteArray())
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
        return object : TerminalWebSocket {
            override fun sendBytes(bytes: ByteArray): Boolean = socket.send(bytes.toByteString())
            override fun sendText(text: String): Boolean = socket.send(text)
            override fun close(code: Int, reason: String): Boolean = socket.close(code, reason)
        }
    }
}
