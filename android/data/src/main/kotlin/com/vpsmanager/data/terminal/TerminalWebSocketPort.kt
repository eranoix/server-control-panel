package com.vpsmanager.data.terminal

/**
 * A single live `/ws/shell` connection, abstracted away from the concrete
 * OkHttp WebSocket type underneath. `:feature-terminal` is not allowed to
 * import `okhttp3.*` directly (the `BffOnlyNetworkPlugin` gate) — this
 * is the pure-Kotlin seam it holds instead, satisfied in production by
 * [OkHttpTerminalWebSocketFactory] and by a fake in tests.
 */
interface TerminalWebSocket {
    /** Sends exactly one binary frame. Returns false if the socket already closed/queue is full. */
    fun sendBytes(bytes: ByteArray): Boolean

    /** Sends exactly one text frame (a JSON-encoded control message). */
    fun sendText(text: String): Boolean

    /** Requests a graceful close with the given WS close code/reason. */
    fun close(code: Int, reason: String): Boolean
}

/** Callbacks a [TerminalWebSocket] delivers to whoever opened it. */
interface TerminalWebSocketListener {
    fun onOpen()
    fun onBinaryMessage(bytes: ByteArray)

    /**
     * A TEXT frame: conversation between server and client, never output from
     * the remote program.
     *
     * It exists for the session's effective size announcement
     * (`{"type":"size","cols":…,"rows":…}`, see `internal/pty/pty.go`). It
     * comes in separately on purpose: if it arrived through the same path, the
     * emulator would draw it on screen as though it were program text.
     *
     * Empty by default so that a caller who does not care need not know this
     * exists.
     */
    fun onTextMessage(text: String) {}
    fun onClosed(code: Int, reason: String)
    fun onFailure(reason: String)
}

/**
 * Opens a [TerminalWebSocket] against [url], delivering events to [listener].
 * [OkHttpTerminalWebSocketFactory] is the only production implementation and
 * the only place in the app that constructs a real OkHttp WebSocket; tests
 * substitute a fake factory that never touches the network.
 */
fun interface TerminalWebSocketFactory {
    fun open(url: String, listener: TerminalWebSocketListener): TerminalWebSocket
}
