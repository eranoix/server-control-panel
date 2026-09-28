package dev.servercontrolpanel.feature.whatsapp

import dev.servercontrolpanel.data.whatsapp.WhatsAppWebSocket
import dev.servercontrolpanel.data.whatsapp.WhatsAppWebSocketFactory
import dev.servercontrolpanel.data.whatsapp.WhatsAppWebSocketListener
import dev.servercontrolpanel.data.whatsapp.WhatsAppWsEvent
import dev.servercontrolpanel.data.whatsapp.parseWhatsAppWsEvent
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

sealed interface WhatsAppConnectionState {
    data object Connecting : WhatsAppConnectionState
    data object Live : WhatsAppConnectionState
    data class Reconnecting(val attempt: Int) : WhatsAppConnectionState
    data object Disconnected : WhatsAppConnectionState
}

interface WhatsAppEventSource {
    val state: StateFlow<WhatsAppConnectionState>
    val events: SharedFlow<WhatsAppWsEvent>
    fun connect()
    fun disconnect()
}

internal const val BACKOFF_BASE_MS = 500L
internal const val BACKOFF_FACTOR = 2L
internal const val BACKOFF_CAP_MS = 15_000L

internal fun backoffDelayMs(attempt: Int): Long {
    if (attempt <= 1) return BACKOFF_BASE_MS
    var delayMs = BACKOFF_BASE_MS
    repeat(attempt - 1) {
        delayMs = (delayMs * BACKOFF_FACTOR).coerceAtMost(BACKOFF_CAP_MS)
    }
    return delayMs
}

class WhatsAppWsClient(
    private val webSocketFactory: WhatsAppWebSocketFactory,
    private val wsBaseUrl: String,
    private val scope: CoroutineScope,
    private val delayer: suspend (Long) -> Unit = { delay(it) },
) : WhatsAppEventSource {

    private val _state = MutableStateFlow<WhatsAppConnectionState>(WhatsAppConnectionState.Connecting)
    override val state: StateFlow<WhatsAppConnectionState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<WhatsAppWsEvent>(extraBufferCapacity = 64)
    override val events: SharedFlow<WhatsAppWsEvent> = _events.asSharedFlow()

    @Volatile
    private var socket: WhatsAppWebSocket? = null

    @Volatile
    private var shouldRun = false

    private var loopJob: Job? = null

    override fun connect() {
        if (shouldRun) return
        shouldRun = true
        _state.value = WhatsAppConnectionState.Connecting
        loopJob = scope.launch { runLoop() }
    }

    override fun disconnect() {
        shouldRun = false
        loopJob?.cancel()
        socket?.close(1000, "client disconnect")
        socket = null
        _state.value = WhatsAppConnectionState.Disconnected
    }

    private suspend fun runLoop() {
        var attempt = 0
        while (shouldRun) {
            connectAttempt()
            if (!shouldRun) return
            attempt += 1
            _state.value = WhatsAppConnectionState.Reconnecting(attempt)
            delayer(backoffDelayMs(attempt))
        }
    }

    private suspend fun connectAttempt() {
        val outcome = CompletableDeferred<Unit>()
        val listener = object : WhatsAppWebSocketListener {
            override fun onOpen() {
                _state.value = WhatsAppConnectionState.Live
            }

            override fun onTextMessage(text: String) {
                parseWhatsAppWsEvent(text)?.let { _events.tryEmit(it) }
            }

            override fun onClosed(code: Int, reason: String) {
                socket = null
                outcome.complete(Unit)
            }

            override fun onFailure(reason: String) {
                socket = null
                outcome.complete(Unit)
            }
        }
        socket = webSocketFactory.open(wsBaseUrl + WS_WHATSAPP_PATH, listener)
        outcome.await()
    }

    companion object {
        internal const val WS_WHATSAPP_PATH = "/ws/whatsapp"
    }
}
