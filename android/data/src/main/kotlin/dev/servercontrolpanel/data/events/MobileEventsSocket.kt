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

internal const val EVENTS_BACKOFF_BASE_MS = 1_000L
internal const val EVENTS_BACKOFF_FACTOR = 2L
internal const val EVENTS_BACKOFF_CAP_MS = 60_000L
internal const val EVENTS_BACKOFF_JITTER_FRACTION = 0.2

internal fun eventsBackoffDelayMs(attempt: Int, random: Random): Long {
    var base = EVENTS_BACKOFF_BASE_MS
    repeat((attempt - 1).coerceAtLeast(0)) {
        base = (base * EVENTS_BACKOFF_FACTOR).coerceAtMost(EVENTS_BACKOFF_CAP_MS)
    }
    val jitterFactor = 1.0 + random.nextDouble(-EVENTS_BACKOFF_JITTER_FRACTION, EVENTS_BACKOFF_JITTER_FRACTION)
    return (base * jitterFactor).toLong().coerceIn(0, EVENTS_BACKOFF_CAP_MS)
}

enum class ConnectionState { DISCONNECTED, MINTING_TICKET, CONNECTING, CONNECTED, BACKOFF }

internal interface MobileEventsWebSocket {
    fun sendText(text: String): Boolean

    fun close(code: Int, reason: String): Boolean
}

internal interface MobileEventsWebSocketListener {
    fun onOpen()
    fun onTextMessage(text: String)
    fun onClosed(code: Int, reason: String)
    fun onFailure(reason: String)
}

internal fun interface MobileEventsWebSocketFactory {
    fun open(url: String, listener: MobileEventsWebSocketListener): MobileEventsWebSocket
}

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

class MobileEventsSocket internal constructor(
    private val ticketSource: MobileEventsTicketSource,
    private val scope: CoroutineScope,
    private val wsBaseUrl: String,
    private val webSocketFactory: MobileEventsWebSocketFactory,
    private val random: Random = Random.Default,
    private val delayer: suspend (Long) -> Unit = { delay(it) },
) {
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

    fun start() {
        if (shouldRun) return
        shouldRun = true
        _state.value = ConnectionState.DISCONNECTED
        loopJob = scope.launch { runLoop() }
    }

    fun stop() {
        shouldRun = false
        loopJob?.cancel()
        socket?.close(1000, "background")
        socket = null
        _state.value = ConnectionState.DISCONNECTED
    }

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

    private fun dispatchFrame(text: String) {
        try {
            _acks.tryEmit(json.decodeFromString(SubscribedAck.serializer(), text))
            return
        } catch (_: SerializationException) {
        }
        try {
            _events.tryEmit(json.decodeFromString(MobileEvent.serializer(), text))
        } catch (_: SerializationException) {
        }
    }
}
