package dev.servercontrolpanel.feature.terminal.transport

import dev.servercontrolpanel.data.terminal.TerminalTicketSource
import dev.servercontrolpanel.data.terminal.TerminalWebSocket
import dev.servercontrolpanel.data.terminal.TerminalWebSocketFactory
import dev.servercontrolpanel.data.terminal.TerminalWebSocketListener
import dev.servercontrolpanel.data.terminal.WsTicketResult
import dev.servercontrolpanel.feature.terminal.input.ByteSink
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean

private const val WS_SHELL_PATH = "/ws/shell"

/** Close code from `internal/pty/pty.go` meaning "this session is confirmed gone, do not retry". */
internal const val WS_CLOSE_SESSION_ENDED = 4404

// Reconnect backoff: base 500 ms, doubling per attempt, capped at 15 s
// (500, 1000, 2000, 4000, 8000, 15000, ...). Fast after a blip, gentle on a
// server that is really down.
internal const val BACKOFF_BASE_MS = 500L
internal const val BACKOFF_FACTOR = 2L
internal const val BACKOFF_CAP_MS = 15_000L

/**
 * Ceiling for the outbound queue while the socket is down, in bytes. 8 KiB is far
 * above any burst of typing and small enough never to become a surprise dump into
 * the shell.
 */
internal const val MAX_PENDING_SEND_BYTES = 8 * 1024

/**
 * Deadline for the outbound queue. A healthy reconnect takes 0.5 to 1 s (measured);
 * 10 s leaves ample slack while ensuring stale typing never lands in a shell the
 * user has mentally left. Past it the queue is dropped and the user is told.
 */
internal const val PENDING_TTL_MS = 10_000L

internal fun backoffDelayMs(attempt: Int): Long {
    if (attempt <= 1) return BACKOFF_BASE_MS
    var delayMs = BACKOFF_BASE_MS
    repeat(attempt - 1) {
        delayMs = (delayMs * BACKOFF_FACTOR).coerceAtMost(BACKOFF_CAP_MS)
    }
    return delayMs
}

/**
 * `ByteSink` over a single auto-reconnecting `/ws/shell` connection, plus the
 * `resize` control frame of the wire contract (`ctrlMsg` in `internal/pty/pty.go`).
 *
 * There is no paste control frame: the server is a pipe and cannot know whether
 * the program enabled bracketed paste (DECSET 2004). Pastes go through [send],
 * already encoded by `TerminalEngine.encodePaste`, as one binary frame, which the
 * server writes to the PTY atomically.
 *
 * [connect] starts the connect-and-retry loop and returns at once; [state] is how
 * callers observe progress (never infer "stuck" from silence). Only the first
 * attempt omits `attach=1`; with it, the server skips the replay and reports an
 * ended session as close code [WS_CLOSE_SESSION_ENDED], which stops the loop.
 * Every attempt fetches a fresh one-shot ticket from [ticketSource].
 */
class TerminalSocketClient(
    private val name: String,
    private val ticketSource: TerminalTicketSource,
    private val webSocketFactory: TerminalWebSocketFactory,
    private val wsBaseUrl: String,
    private val scope: CoroutineScope,
    private val onBytes: (ByteArray) -> Unit,
    /**
     * The effective session size announced by the server. With several clients
     * the PTY takes the smallest size, and every client must draw a grid of the
     * SESSION's size, or the program's line wrapping and the grid disagree.
     */
    private val onSessionSize: (cols: Int, rows: Int) -> Unit = { _, _ -> },
    /**
     * Whether a fresh attach asks for the server's history replay (`attachReplay`
     * in `internal/pty/pty.go`). `false` when the app primes the screen itself
     * (`TerminalViewModel.startPrimer`); both at once would show the history twice.
     * The app's primer is better: the server replay is capped at 128 KiB, is
     * discarded for repainting programs, and ignores the rotated log. Defaults to
     * `true` so callers that do not prime still get history.
     */
    private val requestServerReplay: Boolean = true,
    private val delayer: suspend (Long) -> Unit = { delay(it) },
    private val now: () -> Long = System::currentTimeMillis,
) : ByteSink {

    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.Connecting)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    @Volatile
    private var socket: TerminalWebSocket? = null

    /**
     * The socket that actually opened, as opposed to [socket], which is assigned as
     * soon as the attempt begins. Sending on a not-yet-open socket silently drops
     * the bytes, so [send] only uses this one and queues otherwise; reconnects are
     * common right when the user returns to the app and starts typing.
     */
    @Volatile
    private var openSocket: TerminalWebSocket? = null

    @Volatile
    private var everConnected = false

    @Volatile
    private var shouldRun = false

    private var loopJob: Job? = null

    /**
     * What the user typed while there was no open socket. Without it the bytes
     * vanished and, with no local echo, the command seemed eaten. Reconnects
     * (0.5 to 1 s) happen right when the user returns and types.
     */
    private val pending = ArrayDeque<ByteArray>()
    private var pendingBytes = 0
    private var pendingSince = 0L

    private val _typingDiscarded = MutableStateFlow(false)

    /**
     * True when the queue overran its size or deadline and was discarded: the bytes
     * either go out or the user is told. Resets once a connection drains the queue.
     */
    val typingDiscarded: StateFlow<Boolean> = _typingDiscarded.asStateFlow()

    private val _pendingTyping = MutableStateFlow("")

    /**
     * Typed text not yet sent, as readable text. Without a connection there is no
     * server echo, and a mute screen looks frozen. See [typingSummary] for why this
     * is not written onto the grid.
     */
    val pendingTyping: StateFlow<String> = _pendingTyping.asStateFlow()

    override fun send(bytes: ByteArray) {
        if (bytes.isEmpty()) return
        // Open, not merely assigned; see [openSocket].
        val live = openSocket
        if (live != null) {
            live.sendBytes(bytes)
            return
        }
        enqueue(bytes)
    }

    @Synchronized
    private fun enqueue(bytes: ByteArray) {
        val nowMs = now()
        if (pending.isEmpty()) pendingSince = nowMs
        val sizeExceeded = pendingBytes + bytes.size > MAX_PENDING_SEND_BYTES
        val deadlineExceeded = nowMs - pendingSince > PENDING_TTL_MS
        if (sizeExceeded || deadlineExceeded) {
            pending.clear()
            pendingBytes = 0
            _pendingTyping.value = ""
            _typingDiscarded.value = true
            return
        }
        pending.addLast(bytes)
        pendingBytes += bytes.size
        _pendingTyping.value = typingSummary(_pendingTyping.value, bytes)
    }

    /**
     * Sends held-back bytes to [destination] in typed order. Idempotent: called
     * from `onOpen` and right after the socket is assigned, on different threads
     * in no guaranteed order.
     */
    @Synchronized
    private fun drainPending(destination: TerminalWebSocket) {
        // Reaching here confirms the socket is open (both callers require it), so
        // mark it in this single place.
        openSocket = destination
        while (pending.isNotEmpty()) {
            destination.sendBytes(pending.removeFirst())
        }
        pendingBytes = 0
        // Clear the summary once the bytes are really sent; from now on the
        // server's echo shows the text on the grid.
        _pendingTyping.value = ""
        _typingDiscarded.value = false
    }

    /** The current grid size, kept so it can be reasserted on every connection. See [reassertSize]. */
    @Volatile
    private var gridSize: Pair<Int, Int>? = null

    /**
     * Tells the server the grid size and remembers it. With the connection down the
     * message is lost, and `TerminalViewModel.applySize` skips unchanged sizes, so
     * [reassertSize] resends the remembered size whenever a socket opens.
     */
    fun sendResize(cols: Int, rows: Int) {
        gridSize = cols to rows
        socket?.sendText(TerminalControlMessage.encode(TerminalControlMessage.resize(cols, rows)))
    }

    /**
     * Reasserts the grid size as soon as the socket opens. Other clients (e.g. the
     * web panel attached to the same session) can move the PTY size, and the client
     * only speaks when its own size changes, so after a reconnect the program might
     * paint for N rows on a grid of M. One text frame per connection fixes it.
     */
    private fun reassertSize() {
        val (cols, rows) = gridSize ?: return
        socket?.sendText(TerminalControlMessage.encode(TerminalControlMessage.resize(cols, rows)))
        TerminalDiag.log("size reasserted ${cols}x$rows session=$name")
    }

    /** Starts the connect-and-auto-reconnect loop. A no-op if already running. */
    fun connect() {
        if (shouldRun) return
        shouldRun = true
        everConnected = false
        _state.value = ConnectionState.Connecting
        loopJob = scope.launch { runLoop() }
    }

    /**
     * Explicit disconnect: closes the socket and stops auto-reconnect for good.
     * Neither [ConnectionState.Disconnected] nor [ConnectionState.SessionEnded]
     * reconnects on its own; only a new [connect] call does.
     */
    fun disconnect() {
        shouldRun = false
        loopJob?.cancel()
        closeSocket(1000, "client disconnect")
        _state.value = ConnectionState.Disconnected
    }

    /**
     * The user returned to the app: retry now instead of waiting out the backoff
     * (which could leave "Reconnecting…" up for 15 s with the server fine).
     *
     * Restarts the loop, resetting `attempt`. It leaves [everConnected] untouched,
     * so the attempt still sends `attach=1` and the server does not resend
     * scrollback, which would duplicate the grid held in memory by
     * [dev.servercontrolpanel.feature.terminal.ui.GridEngine]. It does not revive an
     * explicit [disconnect] nor disturb a live connection.
     */
    fun reconnectNow() {
        if (!shouldRun) return
        if (_state.value == ConnectionState.Live) return
        loopJob?.cancel()
        _state.value = ConnectionState.Connecting
        loopJob = scope.launch { runLoop() }
    }

    private fun closeSocket(code: Int, reason: String) {
        socket?.close(code, reason)
        socket = null
        // A socket we closed is no longer open; keeping it would send keystrokes
        // into a dead pipe instead of the queue.
        openSocket = null
    }

    private sealed interface AttemptOutcome {
        data object Ended : AttemptOutcome

        /**
         * [reachedLive]: whether this attempt opened before failing. Only attempts
         * that never connected should keep climbing the backoff ladder.
         */
        data class Failed(val reason: String, val reachedLive: Boolean = false) : AttemptOutcome
    }

    private suspend fun runLoop() {
        var attempt = 0
        while (shouldRun) {
            // `attach=1` means "I already have the screen": true on reconnects,
            // where a replay would duplicate the in-memory grid; false on the first
            // connection, which `startPrimer` fills. With `replay=0`, the server
            // also leaves the PTY geometry alone (`internal/pty/pty.go`).
            val attach = everConnected
            when (val outcome = connectAttempt(attach)) {
                is AttemptOutcome.Ended -> {
                    shouldRun = false
                    _state.value = ConnectionState.SessionEnded
                    return
                }
                is AttemptOutcome.Failed -> {
                    if (!shouldRun) return
                    // An attempt that reached Live resets the ladder, so repeated
                    // trips to the background (Android cuts the network within
                    // seconds) do not pile up delays. Backoff protects a down
                    // server; it should not punish switching apps.
                    if (outcome.reachedLive) attempt = 0
                    attempt += 1
                    _state.value = ConnectionState.Reconnecting(attempt)
                    delayer(backoffDelayMs(attempt))
                }
            }
        }
    }

    private suspend fun connectAttempt(attach: Boolean): AttemptOutcome {
        val ticket = when (val result = ticketSource.wsTicket(name)) {
            is WsTicketResult.Error -> return AttemptOutcome.Failed(result.reason)
            is WsTicketResult.Success -> result.ticket
        }
        val url = buildUrl(wsBaseUrl, name, ticket, attach, requestServerReplay)
        TerminalDiag.log("attempt session=$name attach=$attach everConnected=$everConnected")
        val outcome = CompletableDeferred<AttemptOutcome>()
        // Binary messages received so far. Without `attach=1`, the first one is the
        // server's scrollback replay.
        var messages = 0
        // Atomic: set in onOpen on the WebSocket thread, read in
        // onClosed/onFailure, possibly on another.
        val opened = AtomicBoolean(false)
        val listener = object : TerminalWebSocketListener {
            override fun onOpen() {
                TerminalDiag.log("onOpen session=$name attach=$attach")
                everConnected = true
                opened.set(true)
                _state.value = ConnectionState.Live
                // Deferred to the dispatcher because `onOpen` may arrive before
                // `socket` is assigned below. The size goes before the queued
                // typing, so it reaches a PTY with the right geometry.
                scope.launch {
                    reassertSize()
                    socket?.let(::drainPending)
                }
            }

            override fun onTextMessage(text: String) {
                val size = TerminalControlMessage.sessionSize(text) ?: return
                TerminalDiag.log("session size=${size.first}x${size.second}")
                onSessionSize(size.first, size.second)
            }

            override fun onBinaryMessage(bytes: ByteArray) {
                messages += 1
                if (messages <= 3) {
                    TerminalDiag.log("msg#$messages attach=$attach bytes=${bytes.size}")
                }
                // Without `attach=1`, the first message is exactly the scrollback
                // replay: the server writes it in one `conn.WriteMessage` before
                // wiring up the PTY proxy. With `attach=1` there is no replay.
                if (requestServerReplay && !attach && messages == 1 &&
                    AttachReplay.isDiffRepaint(bytes)
                ) {
                    TerminalDiag.log("replay DISCARDED (differential repaint) bytes=${bytes.size}")
                    return
                }
                onBytes(bytes)
            }

            override fun onClosed(code: Int, reason: String) {
                socket = null
                openSocket = null
                if (code == WS_CLOSE_SESSION_ENDED) {
                    outcome.complete(AttemptOutcome.Ended)
                } else {
                    outcome.complete(AttemptOutcome.Failed(reason, opened.get()))
                }
            }

            override fun onFailure(reason: String) {
                socket = null
                openSocket = null
                outcome.complete(AttemptOutcome.Failed(reason, opened.get()))
            }
        }
        val isOpen = webSocketFactory.open(url, listener)
        socket = isOpen
        // The other half of the race: if `onOpen` already ran, its drain saw a null
        // `socket`. Draining is synchronized and empties the queue, so twice is safe.
        if (opened.get()) drainPending(isOpen)
        return outcome.await()
    }

    companion object {
        /**
         * Builds the `/ws/shell` URL. The two flags answer different questions:
         *
         * - `attach=1`: "I already have the screen" (a reconnect); the server neither
         *   resends history nor forces a repaint.
         * - `replay=0`: "I handle history"; the server skips the history block only.
         */
        internal fun buildUrl(
            wsBaseUrl: String,
            name: String,
            ticket: String,
            attach: Boolean,
            serverReplay: Boolean = true,
        ): String {
            val base = "$wsBaseUrl$WS_SHELL_PATH?name=$name&ticket=$ticket"
            // `size=1`: this client understands the effective-size announcement
            // (older clients would print the JSON). `frame=1`: it accepts a
            // rendered crop of the session screen when its window is smaller, so a
            // phone does not shrink the session for larger clients.
            val withAttach = if (attach) "$base&attach=1&size=1&frame=1" else "$base&size=1&frame=1"
            return if (serverReplay) withAttach else "$withAttach&replay=0"
        }
    }
}
