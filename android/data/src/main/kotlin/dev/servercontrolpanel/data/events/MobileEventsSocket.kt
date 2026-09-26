package dev.servercontrolpanel.data.events

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import kotlin.random.Random

private const val WS_MOBILE_EVENTS_PATH = "/ws/mobile-events"

// Backoff schedule: base 1s, doubling each attempt, capped at 60s, +/-20% jitter — the
// non-negotiable reconnect state machine this class is specified against.
internal const val EVENTS_BACKOFF_BASE_MS = 1_000L
internal const val EVENTS_BACKOFF_FACTOR = 2L
internal const val EVENTS_BACKOFF_CAP_MS = 60_000L
internal const val EVENTS_BACKOFF_JITTER_FRACTION = 0.2

/**
 * Exponential backoff with jitter for [attempt] (1-based: the first retry after a failure is
 * `attempt = 1`). [random] is an injected seam so tests can assert the exact jittered value
 * (see `MobileEventsSocketTest`'s pure-function tests) instead of only observing "some delay
 * happened".
 */
internal fun eventsBackoffDelayMs(attempt: Int, random: Random): Long {
    var base = EVENTS_BACKOFF_BASE_MS
    repeat((attempt - 1).coerceAtLeast(0)) {
        base = (base * EVENTS_BACKOFF_FACTOR).coerceAtMost(EVENTS_BACKOFF_CAP_MS)
    }
    val jitterFactor = 1.0 + random.nextDouble(-EVENTS_BACKOFF_JITTER_FRACTION, EVENTS_BACKOFF_JITTER_FRACTION)
    return (base * jitterFactor).toLong().coerceIn(0, EVENTS_BACKOFF_CAP_MS)
}

/**
 * Observable state of the single `/ws/mobile-events` connection — the exact shape the
 * reconnect state machine requires, driven only by
 * [MobileEventsSocket.start]/[MobileEventsSocket.stop].
 */
enum class ConnectionState { DISCONNECTED, MINTING_TICKET, CONNECTING, CONNECTED, BACKOFF }

/**
 * A single live `/ws/mobile-events` socket, abstracted away from the concrete OkHttp WebSocket
 * type underneath — mirrors `dev.servercontrolpanel.data.terminal.TerminalWebSocket`'s seam so
 * [MobileEventsSocket] can be unit-tested on the JVM with a fake, never a real network call.
 */
internal interface MobileEventsWebSocket {
    /** Sends exactly one text frame (a JSON-encoded control message). Returns false if the socket already closed/queue is full. */
    fun sendText(text: String): Boolean

    /** Requests a graceful close with the given WS close code/reason. */
    fun close(code: Int, reason: String): Boolean
}

/** Callbacks a [MobileEventsWebSocket] delivers to whoever opened it. */
internal interface MobileEventsWebSocketListener {
    fun onOpen()
    fun onTextMessage(text: String)
    fun onClosed(code: Int, reason: String)
    fun onFailure(reason: String)
}

/**
 * Opens a [MobileEventsWebSocket] against [url], delivering events to [listener].
 * [OkHttpMobileEventsWebSocketFactory] is the only production implementation and the only place
 * in this file that constructs a real OkHttp WebSocket; tests substitute a fake factory that
 * never touches the network.
 */
internal fun interface MobileEventsWebSocketFactory {
    fun open(url: String, listener: MobileEventsWebSocketListener): MobileEventsWebSocket
}

/** Production [MobileEventsWebSocketFactory]: opens a real OkHttp WebSocket against `/ws/mobile-events`. */
internal class OkHttpMobileEventsWebSocketFactory(
    private val client: OkHttpClient = OkHttpClient(),
) : MobileEventsWebSocketFactory {

    override fun open(url: String, listener: MobileEventsWebSocketListener): MobileEventsWebSocket {
        val request = Request.Builder().url(url).build()
        val socket = client.newWebSocket(
            request,
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
        return object : MobileEventsWebSocket {
            override fun sendText(text: String): Boolean = socket.send(text)
            override fun close(code: Int, reason: String): Boolean = socket.close(code, reason)
        }
    }
}

/**
 * The single reconnecting, lifecycle-bound socket for everything routed through
 * `/ws/mobile-events`: the deploy-live screen, health dashboard, and in-app
 * notification stream all consume channels through [MobileEventsClient] on top of
 * this class, never opening a second connection of their own.
 *
 * [start] and [stop] are the only two lifecycle entry points, wired exclusively to
 * `ProcessLifecycleOwner` — no other code path can trigger a reconnect once [stop]
 * has run, until [start] runs again: [stop] unconditionally cancels the reconnect loop and
 * closes the socket with WS close code 1000 / reason `"background"`, and never itself
 * schedules a future attempt.
 *
 * Every attempt (first connect and every reconnect alike) mints a *fresh* ticket via
 * [ticketSource] — the previous one-shot ticket is already consumed server-side
 * (`internal/auth/tokens.go`) and reusing it would just fail. The server's Hub
 * (`internal/mobilebff/events_hub.go`) does not buffer or replay events for a disconnected
 * client — `TestMobileEventsWS_ReconnectDoesNotDuplicate` proves an event published while
 * nobody is connected is simply never delivered — so "never duplicate an event"
 * holds by construction on the client side too: there is no cursor/sequence number to resume
 * from, only a fresh subscribe after reconnecting (see `MobileEventsClient`, which re-sends
 * every currently-active channel's `subscribe` frame once this socket reaches CONNECTED).
 */
class MobileEventsSocket internal constructor(
    private val ticketSource: MobileEventsTicketSource,
    private val scope: CoroutineScope,
    private val wsBaseUrl: String,
    private val webSocketFactory: MobileEventsWebSocketFactory,
    private val random: Random = Random.Default,
    private val delayer: suspend (Long) -> Unit = { delay(it) },
) {
    /** Public constructor: always wires the real OkHttp-backed transport (internal type, hidden from callers). */
    constructor(
        ticketSource: MobileEventsTicketSource,
        scope: CoroutineScope,
        wsBaseUrl: String,
    ) : this(ticketSource, scope, wsBaseUrl, OkHttpMobileEventsWebSocketFactory())

    private val json = Json { ignoreUnknownKeys = true }

    private val _state = MutableStateFlow(ConnectionState.DISCONNECTED)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<MobileEvent>(extraBufferCapacity = 64)
    val events: SharedFlow<MobileEvent> = _events.asSharedFlow()

    private val _acks = MutableSharedFlow<SubscribedAck>(extraBufferCapacity = 64)
    val acks: SharedFlow<SubscribedAck> = _acks.asSharedFlow()

    @Volatile
    private var socket: MobileEventsWebSocket? = null

    @Volatile
    private var shouldRun = false

    private var loopJob: Job? = null

    /** Starts the connect-and-auto-reconnect loop. A no-op if already running. */
    fun start() {
        if (shouldRun) return
        shouldRun = true
        _state.value = ConnectionState.DISCONNECTED
        loopJob = scope.launch { runLoop() }
    }

    /**
     * Unconditionally stops this socket: cancels any in-flight connect/backoff coroutine,
     * closes the socket (if open) with code 1000 / reason `"background"`, and settles on
     * [ConnectionState.DISCONNECTED]. Never schedules a future reconnect on its own — only a
     * subsequent [start] does. This is the single place the "no background reconnect
     * loop" guarantee lives.
     */
    fun stop() {
        shouldRun = false
        loopJob?.cancel()
        socket?.close(1000, "background")
        socket = null
        _state.value = ConnectionState.DISCONNECTED
    }

    /**
     * Sends [op] (a subscribe/unsubscribe control frame) over the currently open socket.
     * Dropped, never queued, when not [ConnectionState.CONNECTED] — a frame sent while
     * disconnected would be meaningless anyway, since [MobileEventsClient] re-sends every
     * active subscription's `subscribe` frame the moment this socket reaches CONNECTED again.
     */
    fun send(op: ClientOp): Boolean {
        if (_state.value != ConnectionState.CONNECTED) return false
        val current = socket ?: return false
        return current.sendText(json.encodeToString(ClientOp.serializer(), op))
    }

    private sealed interface AttemptOutcome {
        data class Failed(val reason: String) : AttemptOutcome
        data object Stopped : AttemptOutcome
    }

    private suspend fun runLoop() {
        var attempt = 0
        while (shouldRun) {
            when (connectAttempt()) {
                is AttemptOutcome.Failed -> {
                    if (!shouldRun) return
                    attempt += 1
                    _state.value = ConnectionState.BACKOFF
                    delayer(eventsBackoffDelayMs(attempt, random))
                }
                AttemptOutcome.Stopped -> return
            }
        }
    }

    private suspend fun connectAttempt(): AttemptOutcome {
        if (!shouldRun) return AttemptOutcome.Stopped
        _state.value = ConnectionState.MINTING_TICKET
        val ticket = when (val result = ticketSource.wsTicket()) {
            is WsTicketResult.Error -> return AttemptOutcome.Failed(result.reason)
            is WsTicketResult.Success -> result.ticket
        }
        if (!shouldRun) return AttemptOutcome.Stopped
        _state.value = ConnectionState.CONNECTING
        val url = "$wsBaseUrl$WS_MOBILE_EVENTS_PATH?ticket=$ticket"
        val outcome = CompletableDeferred<AttemptOutcome>()
        val listener = object : MobileEventsWebSocketListener {
            override fun onOpen() {
                _state.value = ConnectionState.CONNECTED
            }

            override fun onTextMessage(text: String) = dispatchFrame(text)

            override fun onClosed(code: Int, reason: String) {
                socket = null
                outcome.complete(AttemptOutcome.Failed(reason))
            }

            override fun onFailure(reason: String) {
                socket = null
                outcome.complete(AttemptOutcome.Failed(reason))
            }
        }
        socket = webSocketFactory.open(url, listener)
        return outcome.await()
    }

    /**
     * Decides ack-vs-event by attempting to decode as [SubscribedAck] first (its `op` field is
     * required and absent from every [MobileEvent] frame, so the attempt fails cleanly and
     * falls through) rather than hand-parsing a [kotlinx.serialization.json.JsonObject] — one
     * `Json` instance, two schemas, no manual key inspection.
     */
    private fun dispatchFrame(text: String) {
        try {
            _acks.tryEmit(json.decodeFromString(SubscribedAck.serializer(), text))
            return
        } catch (_: SerializationException) {
            // Not an ack frame — fall through and try MobileEvent below.
        }
        try {
            _events.tryEmit(json.decodeFromString(MobileEvent.serializer(), text))
        } catch (_: SerializationException) {
            // Malformed/unrecognized frame: dropped, not fatal.
        }
    }
}
