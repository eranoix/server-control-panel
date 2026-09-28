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

internal const val WS_CLOSE_SESSION_ENDED = 4404

internal const val BACKOFF_BASE_MS = 500L
internal const val BACKOFF_FACTOR = 2L
internal const val BACKOFF_CAP_MS = 15_000L

internal const val MAX_PENDING_SEND_BYTES = 8 * 1024

internal const val PENDING_TTL_MS = 10_000L

internal fun backoffDelayMs(attempt: Int): Long {
    if (attempt <= 1) return BACKOFF_BASE_MS
    var delayMs = BACKOFF_BASE_MS
    repeat(attempt - 1) {
        delayMs = (delayMs * BACKOFF_FACTOR).coerceAtMost(BACKOFF_CAP_MS)
    }
    return delayMs
}

class TerminalSocketClient(
    private val name: String,
    private val ticketSource: TerminalTicketSource,
    private val webSocketFactory: TerminalWebSocketFactory,
    private val wsBaseUrl: String,
    private val scope: CoroutineScope,
    private val onBytes: (ByteArray) -> Unit,
    private val onSessionSize: (cols: Int, rows: Int) -> Unit = { _, _ -> },
    private val requestServerReplay: Boolean = true,
    private val delayer: suspend (Long) -> Unit = { delay(it) },
    private val now: () -> Long = System::currentTimeMillis,
) : ByteSink {

    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.Connecting)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    @Volatile
    private var socket: TerminalWebSocket? = null

    @Volatile
    private var openSocket: TerminalWebSocket? = null

    @Volatile
    private var everConnected = false

    @Volatile
    private var shouldRun = false

    private var loopJob: Job? = null

    private val pending = ArrayDeque<ByteArray>()
    private var pendingBytes = 0
    private var pendingSince = 0L

    private val _typingDiscarded = MutableStateFlow(false)

    val typingDiscarded: StateFlow<Boolean> = _typingDiscarded.asStateFlow()

    private val _pendingTyping = MutableStateFlow("")

    val pendingTyping: StateFlow<String> = _pendingTyping.asStateFlow()

    override fun send(bytes: ByteArray) {
        if (bytes.isEmpty()) return
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

    @Synchronized
    private fun drainPending(destination: TerminalWebSocket) {
        openSocket = destination
        while (pending.isNotEmpty()) {
            destination.sendBytes(pending.removeFirst())
        }
        pendingBytes = 0
        _pendingTyping.value = ""
        _typingDiscarded.value = false
    }

    @Volatile
    private var gridSize: Pair<Int, Int>? = null

    fun sendResize(cols: Int, rows: Int) {
        gridSize = cols to rows
        socket?.sendText(TerminalControlMessage.encode(TerminalControlMessage.resize(cols, rows)))
    }

    private fun reassertSize() {
        val (cols, rows) = gridSize ?: return
        socket?.sendText(TerminalControlMessage.encode(TerminalControlMessage.resize(cols, rows)))
        TerminalDiag.log("size reasserted ${cols}x$rows session=$name")
    }

    fun connect() {
        if (shouldRun) return
        shouldRun = true
        everConnected = false
        _state.value = ConnectionState.Connecting
        loopJob = scope.launch { runLoop() }
    }

    fun disconnect() {
        shouldRun = false
        loopJob?.cancel()
        closeSocket(1000, "client disconnect")
        _state.value = ConnectionState.Disconnected
    }

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
        openSocket = null
    }

    private sealed interface AttemptOutcome {
        data object Ended : AttemptOutcome

        data class Failed(val reason: String, val reachedLive: Boolean = false) : AttemptOutcome
    }

    private suspend fun runLoop() {
        var attempt = 0
        while (shouldRun) {
            val attach = everConnected
            when (val outcome = connectAttempt(attach)) {
                is AttemptOutcome.Ended -> {
                    shouldRun = false
                    _state.value = ConnectionState.SessionEnded
                    return
                }
                is AttemptOutcome.Failed -> {
                    if (!shouldRun) return
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
        var messages = 0
        val opened = AtomicBoolean(false)
        val listener = object : TerminalWebSocketListener {
            override fun onOpen() {
                TerminalDiag.log("onOpen session=$name attach=$attach")
                everConnected = true
                opened.set(true)
                _state.value = ConnectionState.Live
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
        if (opened.get()) drainPending(isOpen)
        return outcome.await()
    }

    companion object {
        internal fun buildUrl(
            wsBaseUrl: String,
            name: String,
            ticket: String,
            attach: Boolean,
            serverReplay: Boolean = true,
        ): String {
            val base = "$wsBaseUrl$WS_SHELL_PATH?name=$name&ticket=$ticket"
            val withAttach = if (attach) "$base&attach=1&size=1&frame=1" else "$base&size=1&frame=1"
            return if (serverReplay) withAttach else "$withAttach&replay=0"
        }
    }
}
