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

/** Nav argument key read by both `AppNavHost`'s route and this ViewModel's `SavedStateHandle`. */
const val TERMINAL_SESSION_NAME_ARG = "name"

/**
 * Owns one [TerminalSocketClient] and one [GridEngine] for this screen's lifetime.
 * The session `name` comes from [savedStateHandle] so it survives process death:
 * Navigation-Compose restores the route argument before the ViewModel is
 * recreated, and the new client's first `connect()` is a fresh attach, which is
 * right since the in-memory grid is gone. Rotation never recreates the ViewModel;
 * only [onGridSizeChanged] fires.
 *
 * Threading: every [GridEngine] call (write, resize, snapshot) runs on
 * [viewModelScope]'s dispatcher, even though bytes arrive on the WebSocket
 * thread. `TerminalEngine` only guarantees `write`/`snapshot` are safe
 * concurrently, not `resize`, so a single dispatcher removes the race.
 */
internal class TerminalViewModel(
    savedStateHandle: SavedStateHandle,
    ticketSource: TerminalTicketSource = TerminalRepository(),
    webSocketFactory: TerminalWebSocketFactory = OkHttpTerminalWebSocketFactory(),
    wsBaseUrl: String = defaultTerminalWsBaseUrl(),
    private val rawLogSource: TerminalRawLogSource = TerminalRepository(),
    private val engineFactory: (cols: Int, rows: Int, scrollback: Int) -> GridEngine =
        RealGridEngine.Companion::create,
    /**
     * Scrollback lines the emulator keeps. Read when the engine is created, since
     * `ghostty_terminal_new` fixes it then; a change applies to the next session.
     */
    initialScrollback: Int = TerminalEngine.DEFAULT_SCROLLBACK,
    private val stallCheckIntervalMs: Long = 1_000L,
    private val stallThresholdMs: Long = STALL_THRESHOLD_MS_DEFAULT,
    private val clock: () -> Long = System::currentTimeMillis,
    private val bannerGraceMs: Long = BANNER_GRACE_MS_DEFAULT,
) : ViewModel() {

    val sessionName: String = checkNotNull(savedStateHandle.get<String>(TERMINAL_SESSION_NAME_ARG)) {
        "TerminalViewModel requires a '$TERMINAL_SESSION_NAME_ARG' nav argument"
    }

    /**
     * Scrollback lines for the NEXT engine. A `var` because the preference arrives
     * from DataStore after construction, and the route sets it before the grid is
     * measured. The live engine is unaffected.
     */
    var scrollbackLines: Int = initialScrollback

    private var engine: GridEngine? = null


    /** Waits for the grid to stop changing before telling the server. */
    private var resizeJob: Job? = null
    private var gridCols = 0
    private var gridRows = 0

    // `null` means "never happened", distinct from a timestamp of 0 (reachable
    // with a test clock), which must not suppress stall detection.
    private var lastBytesReceivedAtMs: Long? = null
    private var lastSendAtMs: Long? = null

    private val _isStalled = MutableStateFlow(false)
    val isStalled: StateFlow<Boolean> = _isStalled.asStateFlow()

    /**
     * True from engine creation until the fetched history is written. Meanwhile,
     * socket bytes wait in [pending]; see [onBytesReceived].
     */
    private var primerPending = false

    private val _bridgeCommandOrigin = MutableStateFlow<String?>(null)

    /**
     * Where the command the bridge just inserted came from, or `null`. Without it,
     * a `docker logs` appearing in a busy session would be a mystery later. Clears
     * itself after a while.
     */
    val bridgeCommandOrigin: StateFlow<String?> = _bridgeCommandOrigin.asStateFlow()

    /** Live bytes that arrived during the primer, in the order they arrived. */
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
        // `startPrimer` primes the screen from the raw log; a server replay too
        // would show the history twice.
        requestServerReplay = false,
    )

    /** The real socket state; logic decides by this one, never by [bannerState]. */
    val connectionState: StateFlow<ConnectionState> = socketClient.state

    /**
     * What the banner should say: [connectionState] with a grace period, so the
     * routine reconnect after every app switch (Android 15+ cuts background network)
     * does not flash "Reconnecting…". [bannerGraceMs] (1.5 s) is above a measured
     * healthy reconnect (0.5 to 1 s).
     *
     * Presentation only: idle is [ConnectionState.Live] because that draws nothing.
     * Never use this to decide whether bytes may be sent.
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    val bannerState: StateFlow<ConnectionState> = socketClient.state
        .flatMapLatest { state ->
            if (state is ConnectionState.Connecting || state is ConnectionState.Reconnecting) {
                // flatMapLatest cancels this wait if the state changes first,
                // which is the "reconnected fast" case.
                flow {
                    delay(bannerGraceMs)
                    emit(state)
                }
            } else {
                flowOf(state)
            }
        }
        .stateIn(viewModelScope, SharingStarted.Eagerly, ConnectionState.Live)

    /** True when what was typed while the connection was down had to be dropped. */
    val typingDiscarded: StateFlow<Boolean> = socketClient.typingDiscarded

    /** What was typed with no connection and is still waiting to go up. */
    val pendingTyping: StateFlow<String> = socketClient.pendingTyping

    /**
     * Wired as `TerminalInputView.byteSink`. Records the send time for the stall
     * detector before delegating, so an unanswered keystroke is distinguishable
     * from an idle connection.
     */
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

    /**
     * Called by [TerminalRoute] whenever the measured size in cells changes. The
     * first call creates the engine and starts the connection; later calls
     * announce the window size to the server.
     */
    fun onGridSizeChanged(cols: Int, rows: Int) {
        if (cols <= 0 || rows <= 0 || (cols == gridCols && rows == gridRows)) return

        // Debounce: a keyboard slide changes the size on every animation frame
        // (measured: ten resizes per keyboard open), and each SIGWINCH makes a
        // differential renderer repaint frames that leave duplicate copies in the
        // scrollback. Only the settled size matters. 220 ms covers the IME
        // animation (~200 ms) and is imperceptible on rotation.
        resizeJob?.cancel()
        resizeJob = viewModelScope.launch {
            delay(SIZE_SETTLE_MS)
            applySize(cols, rows)
        }
    }

    /**
     * Applies the size the grid settled on. Separate from [onGridSizeChanged] so
     * engine creation, which also starts the connection, has a single path.
     */
    private fun applySize(cols: Int, rows: Int) {
        if (cols == gridCols && rows == gridRows) return
        gridCols = cols
        gridRows = rows
        val currentEngine = engine
        if (currentEngine == null) {
            TerminalDiag.log("engine CREATED ${cols}x$rows session=$sessionName -> primer")
            engine = engineFactory(cols, rows, scrollbackLines)
            // Order matters and lives in the primer: fetch the log, then connect
            // (see [startPrimer]).
            primerPending = true
            // Announce the size first, even with no socket yet: `sendResize`
            // records it and `reassertSize` delivers it when the socket opens.
            // Without this a fresh attach never told the server its size.
            socketClient.sendResize(cols, rows)
            startPrimer()
        } else {
            // Tell the server this window's size and leave the engine alone: the
            // server decides the grid size (the smallest client wins), and the
            // engine follows the announcement in `applySessionSize`.
            TerminalDiag.log("window ${cols}x$rows session=$sessionName")
            socketClient.sendResize(cols, rows)
        }
    }

    /**
     * The server announced the session's effective size (the smallest attached
     * client), and the engine takes it. Every client must draw the session's grid,
     * not its own window's, or the program's line wrapping lands in the wrong
     * place. [ScreenAnchor] positions a grid smaller than the visible area.
     */
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

    /**
     * The app returned to the foreground (`ON_START`): reconnect now. Android 15+
     * cuts background network after about 6 s (measured), so on return the
     * connection is always dead and the loop asleep in backoff. A no-op before the
     * first measurement, since [onGridSizeChanged] is what connects.
     */
    fun onReturnedToForeground() {
        if (engine == null) return
        socketClient.reconnectNow()
    }

    /**
     * Pastes [text], deciding from the program's real mode whether to wrap it in
     * bracketed-paste markers (`ESC[200~` ... `ESC[201~`, DECSET 2004). Only the
     * local emulator knows the mode (the server is a pipe): wrapping with it off
     * dumps markers on the command line, not wrapping with it on executes
     * multi-line text. Sent as one binary frame, written to the PTY atomically.
     */
    fun sendPaste(text: String) {
        val bytes = engine?.encodePaste(text) ?: return
        if (bytes.isEmpty()) return
        socketClient.send(bytes)
    }

    /** The modes the remote program enabled; the gesture layer asks before deciding what a touch means. */
    fun currentModes(): TerminalModes = engine?.modes() ?: TerminalModes.NONE

    /**
     * Encodes a mouse event for the remote program, or `null` when there is nothing
     * to send (no mouse reporting requested, or the finger stayed in the cell).
     */
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

    /** Polled by the renderer on its own frame cadence, decoupled from writes by design. */
    fun currentSnapshot() = engine?.snapshot()

    /**
     * Moves the viewport over the emulator's history; negative goes up. This is the
     * live libghostty-vt scrollback; the "load older" panel fetches the older
     * server log as plain text.
     */
    fun scrollViewport(lines: Int) {
        engine?.scrollViewport(lines)
    }

    /** Pins the viewport back at the end ("back to the end" in the UI). */
    fun scrollToBottom() {
        engine?.scrollToBottom()
    }

    /**
     * Where the viewport sits in the history, read once per frame: the library
     * does not notify scroll changes.
     */
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

    /**
     * Holds back one live block until the fetched history is written. Bounded: a
     * session dumping megabytes at attach would fill memory, so past the limit
     * the primer is abandoned and the queue released. Losing history is an
     * annoyance; freezing the live terminal is a defect.
     */
    private fun queueUntilPrimerDone(bytes: ByteArray) {
        pending.addLast(bytes)
        pendingBytes += bytes.size
        if (pendingBytes > MAX_PENDING_BYTES) {
            TerminalDiag.log("primer ABANDONED: $pendingBytes B of live data arrived before the history")
            completePrimer()
        }
    }

    /**
     * Fetches the session history, connects, and writes the history into the engine
     * before releasing the live stream.
     *
     * The connection happens here, after the fetch, because the server only writes
     * the session log while someone is attached. Fetching first means the history
     * ends exactly where the live stream begins; attaching in parallel sends the
     * overlap to both, and replaying part of a frame twice corrupts the screen
     * (TUI frames use relative cursor moves and column jumps that do not erase).
     * Small overlaps are the damaging ones, which made this intermittent.
     *
     * The history is replayed as raw bytes into libghostty-vt rather than shown as
     * stripped text: stripped repaint output is shredded (measured: 511 non-empty
     * lines, mostly spinner frames, versus 5,058 legible lines replayed).
     *
     * The end of the replay is not "sanitised": if the session is inside `vim`,
     * ending in the alt screen with its modes is the correct state.
     */
    private fun startPrimer() {
        val target = TerminalScrollback.byRows(scrollbackLines)
        viewModelScope.launch {
            val history = fetchHistory(target.logBytesToFetch)

            // Connect only now: with nobody attached the server does not write the
            // log, so the history ends exactly where the live stream begins.
            socketClient.connect()

            if (history != null) writeInChunks(history)
            completePrimer()
        }
    }

    /**
     * Fetches the history with a time ceiling, since the connection waits on it;
     * `null` when it did not arrive. History is a comfort; the live session is
     * the point.
     */
    private suspend fun fetchHistory(targetBytes: Int): ByteArray? {
        val deadline = withTimeoutOrNull(PRIMER_TIMEOUT_MS) {
            for (attempt in 0 until PRIMER_ATTEMPTS) {
                if (!primerPending) return@withTimeoutOrNull null
                // Two sources, in order: the rendered history (append-only lines
                // that left the screen, built server-side, so it cannot duplicate),
                // then the raw log as a fallback for sessions without it.
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

    /**
     * Writes the replay in chunks, yielding between them. Up to 16 MiB cross JNI on
     * the dispatcher that draws the screen; [yield] lets frames render so the
     * history scrolls in instead of the screen freezing.
     */
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

    /** Releases the live stream that was awaiting the history, in arrival order. */
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

    /**
     * Pastes the command another screen sent through the bridge, if any. Only after
     * the primer, or the replay would overwrite it. It is pasted, never executed
     * (see [TerminalBridge]); the user presses Enter.
     */
    private fun deliverBridgeCommand() {
        val request = TerminalBridge.consume() ?: return
        sendPaste(request.command)
        _bridgeCommandOrigin.value = request.origin
        viewModelScope.launch {
            delay(BRIDGE_NOTICE_DURATION_MS)
            // Only clear if unchanged: a second command inside the window
            // replaces the line.
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
        // Stalled: the user typed (sentAt is set and newer than the last reply)
        // and nothing came back for stallThresholdMs while the socket reports
        // Live. Live with nobody typing is a healthy idle prompt.
        _isStalled.value = sentAt != null &&
            (receivedAt == null || sentAt > receivedAt) &&
            (now - sentAt) > stallThresholdMs
    }

    override fun onCleared() {
        socketClient.disconnect()
        engine?.close()
    }

    companion object {
        /** Silence marking the end of an attach repaint. */
        const val ATTACH_CLEAR_SILENCE_MS = 1_200L

        /**
         * How long to wait for the grid to settle before sending the size. Covers
         * the IME animation (~200 ms) without noticeably delaying rotation.
         */
        const val SIZE_SETTLE_MS = 220L

        /**
         * 8 s: long enough that a slow round trip never false-positives, short
         * enough that a hung remote shell surfaces before the user starts retyping.
         */
        const val STALL_THRESHOLD_MS_DEFAULT = 8_000L

        /**
         * 1.5 s: above a measured healthy reconnect (0.5 to 1 s), so app switching
         * lights no banner, and below the point where silence looks like a freeze.
         */
        const val BANNER_GRACE_MS_DEFAULT = 1_500L

        /**
         * Ceiling on the live stream held while the history is fetched. 2 MiB is
         * more than a normal attach produces. See [queueUntilPrimerDone].
         */
        const val MAX_PENDING_BYTES = 2 * 1024 * 1024

        /**
         * Replay slice written between yields: 256 KiB amortises the JNI call cost
         * and fits comfortably in a 16 ms frame.
         */
        const val REPLAY_CHUNK_SIZE = 256 * 1024

        /**
         * Attempts at fetching the history. The first competes with the attach
         * for the network, so failures are usually transient; more would only
         * delay the live stream.
         */
        const val PRIMER_ATTEMPTS = 3

        /** Pause between primer attempts. */
        const val RETRY_DELAY_MS = 400L

        /**
         * Ceiling on waiting for the history before connecting, so a slow server
         * cannot keep the terminal from connecting. 12 s rather than 3 s: the body
         * is hundreds of KB even gzipped, fetched right as the device returns to
         * the foreground on a contended network, and giving up leaves a black
         * screen until the program redraws. Waiting too long is the cheaper mistake.
         */
        const val PRIMER_TIMEOUT_MS = 12_000L

        /**
         * How long the "came from X" line stays: 6 s, readable after looking up
         * from the keyboard, short enough not to become a permanent banner.
         */
        const val BRIDGE_NOTICE_DURATION_MS = 6_000L
    }

}
