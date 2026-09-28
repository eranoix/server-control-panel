package dev.servercontrolpanel.feature.terminal.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.terminal.OkHttpTerminalWebSocketFactory
import dev.servercontrolpanel.data.terminal.RawLogResult
import dev.servercontrolpanel.data.terminal.TerminalRepository
import dev.servercontrolpanel.data.terminal.TerminalRawLogSource
import dev.servercontrolpanel.data.terminal.TerminalTicketSource
import dev.servercontrolpanel.data.terminal.TerminalWebSocketFactory
import dev.servercontrolpanel.core.shell.TerminalBridge
import dev.servercontrolpanel.data.terminal.defaultTerminalWsBaseUrl
import dev.servercontrolpanel.feature.terminal.input.ByteSink
import dev.servercontrolpanel.feature.terminal.prefs.TerminalScrollback
import dev.servercontrolpanel.feature.terminal.transport.ConnectionState
import dev.servercontrolpanel.feature.terminal.transport.TerminalDiag
import dev.servercontrolpanel.feature.terminal.transport.TerminalSocketClient
import dev.servercontrolpanel.terminalengine.MouseAction
import dev.servercontrolpanel.terminalengine.MouseButton
import dev.servercontrolpanel.terminalengine.MouseGeometry
import dev.servercontrolpanel.terminalengine.TerminalEngine
import dev.servercontrolpanel.terminalengine.TerminalModes
import dev.servercontrolpanel.terminalengine.TerminalScrollState
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.yield

const val TERMINAL_SESSION_NAME_ARG = "name"

internal class TerminalViewModel(
    savedStateHandle: SavedStateHandle,
    ticketSource: TerminalTicketSource = TerminalRepository(),
    webSocketFactory: TerminalWebSocketFactory = OkHttpTerminalWebSocketFactory(),
    wsBaseUrl: String = defaultTerminalWsBaseUrl(),
    private val rawLogSource: TerminalRawLogSource = TerminalRepository(),
    private val engineFactory: (cols: Int, rows: Int, scrollback: Int) -> GridEngine =
        RealGridEngine.Companion::create,
    initialScrollback: Int = TerminalEngine.DEFAULT_SCROLLBACK,
    private val stallCheckIntervalMs: Long = 1_000L,
    private val stallThresholdMs: Long = STALL_THRESHOLD_MS_DEFAULT,
    private val clock: () -> Long = System::currentTimeMillis,
    private val bannerGraceMs: Long = BANNER_GRACE_MS_DEFAULT,
) : ViewModel() {

    val sessionName: String = checkNotNull(savedStateHandle.get<String>(TERMINAL_SESSION_NAME_ARG)) {
        "TerminalViewModel requires a '$TERMINAL_SESSION_NAME_ARG' nav argument"
    }

    var scrollbackLines: Int = initialScrollback

    private var engine: GridEngine? = null


    private var resizeJob: Job? = null
    private var gridCols = 0
    private var gridRows = 0

    private var lastBytesReceivedAtMs: Long? = null
    private var lastSendAtMs: Long? = null

    private val _isStalled = MutableStateFlow(false)
    val isStalled: StateFlow<Boolean> = _isStalled.asStateFlow()

    private var primerPending = false

    private val _bridgeCommandOrigin = MutableStateFlow<String?>(null)

    val bridgeCommandOrigin: StateFlow<String?> = _bridgeCommandOrigin.asStateFlow()

    private val pending = ArrayDeque<ByteArray>()
    private var pendingBytes = 0

    private val socketClient = TerminalSocketClient(
        name = sessionName,
        ticketSource = ticketSource,
        webSocketFactory = webSocketFactory,
        wsBaseUrl = wsBaseUrl,
        scope = viewModelScope,
        onBytes = ::onBytesReceived,
        onSessionSize = ::applySessionSize,
        requestServerReplay = false,
    )

    val connectionState: StateFlow<ConnectionState> = socketClient.state

    @OptIn(ExperimentalCoroutinesApi::class)
    val bannerState: StateFlow<ConnectionState> = socketClient.state
        .flatMapLatest { state ->
            if (state is ConnectionState.Connecting || state is ConnectionState.Reconnecting) {
                flow {
                    delay(bannerGraceMs)
                    emit(state)
                }
            } else {
                flowOf(state)
            }
        }
        .stateIn(viewModelScope, SharingStarted.Eagerly, ConnectionState.Live)

    val typingDiscarded: StateFlow<Boolean> = socketClient.typingDiscarded

    val pendingTyping: StateFlow<String> = socketClient.pendingTyping

    val byteSink: ByteSink = ByteSink { bytes ->
        lastSendAtMs = clock()
        socketClient.send(bytes)
    }

    init {
        viewModelScope.launch {
            while (isActive) {
                delay(stallCheckIntervalMs)
                evaluateStall()
            }
        }
    }

    fun onGridSizeChanged(cols: Int, rows: Int) {
        if (cols <= 0 || rows <= 0 || (cols == gridCols && rows == gridRows)) return

        resizeJob?.cancel()
        resizeJob = viewModelScope.launch {
            delay(SIZE_SETTLE_MS)
            applySize(cols, rows)
        }
    }

    private fun applySize(cols: Int, rows: Int) {
        if (cols == gridCols && rows == gridRows) return
        gridCols = cols
        gridRows = rows
        val currentEngine = engine
        if (currentEngine == null) {
            TerminalDiag.log("engine CREATED ${cols}x$rows session=$sessionName -> primer")
            engine = engineFactory(cols, rows, scrollbackLines)
            primerPending = true
            socketClient.sendResize(cols, rows)
            startPrimer()
        } else {
            TerminalDiag.log("window ${cols}x$rows session=$sessionName")
            socketClient.sendResize(cols, rows)
        }
    }

    private fun applySessionSize(cols: Int, rows: Int) {
        viewModelScope.launch {
            val motor = engine ?: return@launch
            if (cols == gridCols && rows == gridRows) return@launch
            TerminalDiag.log("session is now ${cols}x$rows")
            gridCols = cols
            gridRows = rows
            motor.resize(cols, rows)
        }
    }

    fun onReturnedToForeground() {
        if (engine == null) return
        socketClient.reconnectNow()
    }

    fun sendPaste(text: String) {
        val bytes = engine?.encodePaste(text) ?: return
        if (bytes.isEmpty()) return
        socketClient.send(bytes)
    }

    fun currentModes(): TerminalModes = engine?.modes() ?: TerminalModes.NONE

    fun encodeMouse(
        action: MouseAction,
        button: MouseButton,
        positionXPx: Float,
        positionYPx: Float,
        geometry: MouseGeometry,
        anyButtonPressed: Boolean,
    ): ByteArray? = engine?.encodeMouse(
        action = action,
        button = button,
        positionXPx = positionXPx,
        positionYPx = positionYPx,
        geometry = geometry,
        anyButtonPressed = anyButtonPressed,
    )

    fun currentSnapshot() = engine?.snapshot()

    fun scrollViewport(lines: Int) {
        engine?.scrollViewport(lines)
    }

    fun scrollToBottom() {
        engine?.scrollToBottom()
    }

    fun currentScrollState(): TerminalScrollState =
        engine?.scrollState() ?: TerminalScrollState.AT_END

    private fun onBytesReceived(bytes: ByteArray) {
        viewModelScope.launch {
            if (primerPending) {
                queueUntilPrimerDone(bytes)
            } else {
                engine?.write(bytes)
            }
            lastBytesReceivedAtMs = clock()
            _isStalled.value = false
        }
    }

    private fun queueUntilPrimerDone(bytes: ByteArray) {
        pending.addLast(bytes)
        pendingBytes += bytes.size
        if (pendingBytes > MAX_PENDING_BYTES) {
            TerminalDiag.log("primer ABANDONED: $pendingBytes B of live data arrived before the history")
            completePrimer()
        }
    }

    private fun startPrimer() {
        val target = TerminalScrollback.byRows(scrollbackLines)
        viewModelScope.launch {
            val history = fetchHistory(target.logBytesToFetch)

            socketClient.connect()

            if (history != null) writeInChunks(history)
            completePrimer()
        }
    }

    private suspend fun fetchHistory(targetBytes: Int): ByteArray? {
        val deadline = withTimeoutOrNull(PRIMER_TIMEOUT_MS) {
            for (attempt in 0 until PRIMER_ATTEMPTS) {
                if (!primerPending) return@withTimeoutOrNull null
                val rendered = rawLogSource.history(sessionName, targetBytes)
                if (rendered is RawLogResult.Success && rendered.bytes.isNotEmpty()) {
                    TerminalDiag.log(
                        "primer session=$sessionName ${rendered.bytes.size} B of " +
                            "${rendered.total} B of rendered history",
                    )
                    return@withTimeoutOrNull rendered.bytes
                }
                when (val r = rawLogSource.rawLog(sessionName, targetBytes)) {
                    is RawLogResult.Success -> {
                        TerminalDiag.log(
                            "primer session=$sessionName ${r.bytes.size} B of ${r.total} B of raw log (fallback)",
                        )
                        return@withTimeoutOrNull r.bytes
                    }
                    is RawLogResult.Error -> {
                        TerminalDiag.log("primer FAILED (${attempt + 1}): ${r.reason}")
                        if (attempt + 1 < PRIMER_ATTEMPTS) delay(RETRY_DELAY_MS)
                    }
                }
            }
            null
        }
        if (deadline == null) {
            TerminalDiag.log("primer without history, connecting with the live stream only")
        }
        return deadline
    }

    private suspend fun writeInChunks(bytes: ByteArray) {
        val motor = engine ?: return
        var start = 0
        while (start < bytes.size) {
            val end = minOf(start + REPLAY_CHUNK_SIZE, bytes.size)
            motor.write(bytes.copyOfRange(start, end))
            start = end
            yield()
        }
    }

    private fun completePrimer() {
        if (!primerPending) return
        primerPending = false
        val motor = engine
        while (pending.isNotEmpty()) {
            val tile = pending.removeFirst()
            motor?.write(tile)
        }
        pendingBytes = 0
        deliverBridgeCommand()
    }

    private fun deliverBridgeCommand() {
        val request = TerminalBridge.consume() ?: return
        sendPaste(request.command)
        _bridgeCommandOrigin.value = request.origin
        viewModelScope.launch {
            delay(BRIDGE_NOTICE_DURATION_MS)
            if (_bridgeCommandOrigin.value == request.origin) {
                _bridgeCommandOrigin.value = null
            }
        }
    }

    private fun evaluateStall() {
        if (connectionState.value != ConnectionState.Live) {
            _isStalled.value = false
            return
        }
        val sentAt = lastSendAtMs
        val receivedAt = lastBytesReceivedAtMs
        val now = clock()
        _isStalled.value = sentAt != null &&
            (receivedAt == null || sentAt > receivedAt) &&
            (now - sentAt) > stallThresholdMs
    }

    override fun onCleared() {
        socketClient.disconnect()
        engine?.close()
    }

    companion object {
        const val ATTACH_CLEAR_SILENCE_MS = 1_200L

        const val SIZE_SETTLE_MS = 220L

        const val STALL_THRESHOLD_MS_DEFAULT = 8_000L

        const val BANNER_GRACE_MS_DEFAULT = 1_500L

        const val MAX_PENDING_BYTES = 2 * 1024 * 1024

        const val REPLAY_CHUNK_SIZE = 256 * 1024

        const val PRIMER_ATTEMPTS = 3

        const val RETRY_DELAY_MS = 400L

        const val PRIMER_TIMEOUT_MS = 12_000L

        const val BRIDGE_NOTICE_DURATION_MS = 6_000L
    }

}
