package com.vpsmanager.feature.terminal.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.terminal.OkHttpTerminalWebSocketFactory
import com.vpsmanager.data.terminal.RawLogResult
import com.vpsmanager.data.terminal.TerminalRepository
import com.vpsmanager.data.terminal.TerminalRawLogSource
import com.vpsmanager.data.terminal.TerminalTicketSource
import com.vpsmanager.data.terminal.TerminalWebSocketFactory
import com.vpsmanager.core.shell.PonteComOTerminal
import com.vpsmanager.data.terminal.defaultTerminalWsBaseUrl
import com.vpsmanager.feature.terminal.input.ByteSink
import com.vpsmanager.feature.terminal.prefs.TerminalScrollback
import com.vpsmanager.feature.terminal.transport.ConnectionState
import com.vpsmanager.feature.terminal.transport.TerminalDiag
import com.vpsmanager.feature.terminal.transport.TerminalSocketClient
import com.vpsmanager.terminalengine.MouseAction
import com.vpsmanager.terminalengine.MouseButton
import com.vpsmanager.terminalengine.MouseGeometry
import com.vpsmanager.terminalengine.TerminalEngine
import com.vpsmanager.terminalengine.TerminalModes
import com.vpsmanager.terminalengine.TerminalScrollState
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

/** Nav argument key both `AppNavHost`'s route and this ViewModel's `SavedStateHandle` read/write. */
const val TERMINAL_SESSION_NAME_ARG = "name"

/**
 * Owns one [TerminalSocketClient] and one [GridEngine] for this screen's
 * lifetime. The session `name` comes from [savedStateHandle] rather than a
 * plain constructor parameter — that is the mechanism that survives PROCESS
 * DEATH: Navigation-Compose restores its back stack (including this route's
 * `name` argument) from the activity's saved-instance bundle before this
 * ViewModel is ever recreated, so a system-killed-and-relaunched app
 * reconstructs the same `name` and reattaches to the same dtach session (the
 * app-killed case the acceptance criterion names). An ordinary configuration
 * change (rotation) never recreates this ViewModel at all — the in-memory
 * grid and live socket are untouched by rotation, only [onGridSizeChanged]
 * fires. A process-death relaunch DOES construct a brand-new
 * [TerminalSocketClient], so its very first `connect()` is a genuine fresh
 * attach (never `attach=1`) — correct, since the in-memory grid was wiped and
 * the server's fresh-attach scrollback replay is exactly what reshows the
 * session's prior output.
 *
 * Threading: every mutation of [GridEngine] — [GridEngine.write] (inbound
 * bytes), [GridEngine.resize] (grid-size changes) and [GridEngine.snapshot]
 * (polled by the renderer) — is funneled onto [viewModelScope]'s dispatcher
 * (`Dispatchers.Main.immediate`) even though inbound bytes arrive on the
 * WebSocket's own callback thread. `TerminalEngine`'s own docs only
 * guarantee `write`/`snapshot` are safe to call *concurrently* from two
 * threads, not that `resize` (which mutates plain, non-atomic Kotlin fields)
 * is safe to race against either — funneling everything onto one dispatcher
 * removes that race entirely rather than relying on the narrower native
 * guarantee.
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
     * How many LINES of scrollback the emulator keeps. Read at the moment the
     * engine is CREATED, because `ghostty_terminal_new` fixes the scrollback
     * size at creation — changing the preference only applies to the next
     * attached session, and the options sheet says so on screen.
     */
    scrollbackInicial: Int = TerminalEngine.SCROLLBACK_PADRAO,
    private val stallCheckIntervalMs: Long = 1_000L,
    private val stallThresholdMs: Long = STALL_THRESHOLD_MS_DEFAULT,
    private val clock: () -> Long = System::currentTimeMillis,
    private val bannerGraceMs: Long = BANNER_GRACE_MS_DEFAULT,
) : ViewModel() {

    val sessionName: String = checkNotNull(savedStateHandle.get<String>(TERMINAL_SESSION_NAME_ARG)) {
        "TerminalViewModel requires a '$TERMINAL_SESSION_NAME_ARG' nav argument"
    }

    /**
     * How many scrollback lines the NEXT engine will keep. It is a `var`
     * because the preference arrives from the DataStore after the ViewModel is
     * constructed, and the engine is only born once the grid is measured — the
     * route updates this value before then. Changing it later does not touch
     * the live engine: the scrollback size is fixed in `ghostty_terminal_new`.
     */
    var scrollbackLinhas: Int = scrollbackInicial

    private var engine: GridEngine? = null


    /** Waits for the grid to stop changing before telling the server. */
    private var resizeJob: Job? = null
    private var gridCols = 0
    private var gridRows = 0

    // `null` means "never happened yet" -- kept distinct from a real
    // timestamp of 0 so a keystroke sent at clock-time zero (only reachable
    // with a virtual/test clock, since a real device clock is never exactly
    // 0) can't be mistaken for "no send has happened," which would silently
    // suppress stall detection.
    private var lastBytesReceivedAtMs: Long? = null
    private var lastSendAtMs: Long? = null

    private val _isStalled = MutableStateFlow(false)
    val isStalled: StateFlow<Boolean> = _isStalled.asStateFlow()

    /**
     * True from the instant the engine is born until the fetched history has
     * been written into it. While it holds, bytes arriving from the socket sit
     * in [pendentes] instead of reaching the engine — see [onBytesReceived].
     */
    private var primerPendente = false

    private val _origemDoComandoDaPonte = MutableStateFlow<String?>(null)

    /**
     * Where the command the bridge has just inserted came from, or `null`.
     *
     * It exists because the command turns up in a session that may have ten
     * lines of something else above it. Without saying "came from: nginx
     * (container)", whoever looks at the screen two minutes later finds a
     * `docker logs` they do not remember typing. The line clears itself — it
     * is a note, not a state.
     */
    val origemDoComandoDaPonte: StateFlow<String?> = _origemDoComandoDaPonte.asStateFlow()

    /** Live bytes that arrived during the primer, in the order they arrived. */
    private val pendentes = ArrayDeque<ByteArray>()
    private var bytesPendentes = 0

    private val socketClient = TerminalSocketClient(
        name = sessionName,
        ticketSource = ticketSource,
        webSocketFactory = webSocketFactory,
        wsBaseUrl = wsBaseUrl,
        scope = viewModelScope,
        onBytes = ::onBytesReceived,
        onTamanhoDaSessao = ::aplicarTamanhoDaSessao,
        // What primes the screen here is `iniciarPrimer`, with the raw log
        // replayed into the engine. Asking the server to replay as well would
        // show the history twice.
        pedirReplayDoServidor = false,
    )

    /** The REAL socket state — the logic decides by this one (never by [bannerState]). */
    val connectionState: StateFlow<ConnectionState> = socketClient.state

    /**
     * What the banner should say: [connectionState] with a grace period.
     *
     * The banner appeared THE INSTANT anything dropped, and since Android 15+
     * cuts the app's network a few seconds after it leaves the foreground,
     * EVERY app switch paid for a "Reconnecting…" flashing on the way back —
     * even when the reconnection sorted itself out before the person had
     * finished looking at the screen. It was never a lost session (the session
     * lives on the server, under `dtach`); it was noise.
     *
     * [bannerGraceMs] is measured, not guessed: on this app's real path a
     * healthy reconnection takes 0.5 s to 1 s (SYN + TLS + ticket + upgrade,
     * timed with `tcpdump` on the emulator). 1.5 s sits comfortably above the
     * top of that range, so a normal reconnection NEVER gets to light the
     * banner, and one that runs past it is already a delay worth reporting.
     *
     * **This is PRESENTATION state.** The initial value and the "nothing to
     * say" value are both [ConnectionState.Live], because that is how
     * [ConnectionBanner] draws nothing; nobody should read from here to decide
     * whether bytes may be sent — [connectionState] exists for that.
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    val bannerState: StateFlow<ConnectionState> = socketClient.state
        .flatMapLatest { estado ->
            if (estado is ConnectionState.Connecting || estado is ConnectionState.Reconnecting) {
                // flatMapLatest cancels this wait if the state changes before
                // it elapses — which is exactly the "reconnected fast" case.
                flow {
                    delay(bannerGraceMs)
                    emit(estado)
                }
            } else {
                flowOf(estado)
            }
        }
        .stateIn(viewModelScope, SharingStarted.Eagerly, ConnectionState.Live)

    /** True when what was typed while the connection was down had to be dropped. */
    val digitacaoDescartada: StateFlow<Boolean> = socketClient.digitacaoDescartada

    /** What was typed with no connection and is still waiting to go up. */
    val digitacaoPendente: StateFlow<String> = socketClient.digitacaoPendente

    /**
     * Wired as `TerminalInputView.byteSink`. Records write activity for the
     * stall detector *before* delegating, so a keystroke that never gets a
     * response is exactly the signal [evaluateStall] needs — distinct from a
     * connection that is simply idle because nobody has typed anything.
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
     * Called by [TerminalRoute] whenever its measured size (in cells) changes
     * — first measurement, rotation, window resize, or IME show/hide
     * changing available rows. The first call creates the engine and starts
     * the socket; every call after that keeps the local engine and the
     * remote PTY in agreement by resizing the engine BEFORE telling the
     * server, so they never disagree on geometry mid-resize.
     */
    fun onGridSizeChanged(cols: Int, rows: Int) {
        if (cols <= 0 || rows <= 0 || (cols == gridCols && rows == gridRows)) return

        // ── THE DUPLICATION DEFECT, MEASURED ────────────────────────────────
        // Raising the on-screen keyboard does not change the grid size ONCE:
        // it changes it on EVERY FRAME of the slide animation. Logged on the
        // server, a single tap to open the keyboard:
        //
        //   resize do cliente: 67x46 -> 67x42     (10 changes
        //   resize do cliente: 67x42 -> 67x39      within the
        //   ... 39, 38, 36, 35, 34, 33, 32         same second)
        //   resize do cliente: 67x31 -> 67x30
        //
        // and another ten on the way out. Each one becomes a SIGWINCH, and a
        // differential renderer (Ink, the one Claude Code uses) repaints the
        // WHOLE FRAME on every SIGWINCH. A frame taller than the screen cannot
        // erase itself — the repaint's `ESC[nA` saturates at the first line of
        // the SCREEN and never reaches the scrollback — so each repaint leaves
        // the previous copy above it. Twenty repaints, twenty copies. THIS was
        // the duplication the operator reported three times, not the attach: my
        // two earlier fixes addressed the attach and so never touched it.
        //
        // The intermediate size of an animation is not information: it is
        // interpolation noise. Nobody needs to know the grid passed through 39
        // rows on its way to 30. Only the value it SETTLES on matters, and it
        // is the only one that leaves here.
        //
        // 220 ms covers Android's IME animation (~200 ms on most handsets; the
        // material design sheet specifies 250 ms for large transitions) and
        // stays imperceptible for a screen rotation, which is the other path
        // that comes through here.
        resizeJob?.cancel()
        resizeJob = viewModelScope.launch {
            delay(ESTABILIZACAO_DE_TAMANHO_MS)
            aplicarTamanho(cols, rows)
        }
    }

    /**
     * Applies the size the grid ACTUALLY settled on, once it stops changing.
     * Kept apart from [onGridSizeChanged] so the engine-creation path — which
     * also triggers the connection — remains a single one.
     */
    private fun aplicarTamanho(cols: Int, rows: Int) {
        if (cols == gridCols && rows == gridRows) return
        gridCols = cols
        gridRows = rows
        val currentEngine = engine
        if (currentEngine == null) {
            TerminalDiag.log("engine CRIADA ${cols}x$rows sessao=$sessionName -> primer")
            engine = engineFactory(cols, rows, scrollbackLinhas)
            // ORDER MATTERS, and it now lives ENTIRELY inside the primer: it
            // fetches the log and ONLY THEN connects. See [iniciarPrimer] on why
            // connecting in parallel delivered the same bytes twice.
            primerPendente = true
            // ANNOUNCE THE SIZE BEFORE ANYTHING ELSE — this was missing.
            //
            // This branch (engine creation) never called `sendResize`; only the
            // CHANGE branch did. And `reafirmarTamanho`, which resends once the
            // socket opens, gives up if nothing was ever sent:
            //
            //     val (cols, rows) = tamanhoDaGrade ?: return
            //
            // Result: on a FRESH attach the app never told the server what size
            // it was. The PTY stayed at the previous client's size, the program
            // painted for that size, and the app drew on its own — the wrong
            // screen, almost always a blank one.
            //
            // And it explains every gesture that "fixed" it: changing the font
            // size, attaching an image, opening the options sheet. They all
            // change the grid height, land in the CHANGE branch, and only then
            // did the size finally go out. Closing and reopening brought the
            // defect back because it came back to this branch.
            //
            // The socket does not exist yet here, and that is no harm:
            // `sendResize` records the size and `reafirmarTamanho` delivers it
            // the moment the connection opens. What could not go on was the
            // size never existing at all.
            socketClient.sendResize(cols, rows)
            iniciarPrimer()
        } else {
            // TELLS the server the size of THIS WINDOW — and leaves the engine alone.
            //
            // The server decides the GRID size, because a session may have
            // several clients and the PTY sits at the smallest of them. The
            // engine only changes when the announcement arrives
            // (`aplicarTamanhoDaSessao`).
            //
            // Resizing the engine from the measurement taken here was the
            // defect: with a smaller client attached, the program wrapped its
            // lines at 66 columns while the app drew on a grid of 72.
            TerminalDiag.log("janela ${cols}x$rows sessao=$sessionName")
            socketClient.sendResize(cols, rows)
        }
    }

    /**
     * The server has announced the session's EFFECTIVE size — the smallest
     * among the attached clients. The engine takes on that size.
     *
     * ## Why the server decides, instead of the client
     *
     * It is the smallest-size rule, and it has two halves. The first is the
     * minimum. The second, which is what makes the first work, is that EVERY
     * client draws a grid the size of the SESSION, not of its own window —
     * whoever has the larger window sees the session with space around it.
     *
     * Without the second half, the remote program wraps its lines at N columns
     * while the client draws on a grid of M, and every piece of text lands in
     * the wrong place. That was the defect, measured in the server's log:
     *
     * ```
     * resize do cliente: 83x63 -> 72x60 (sessao "Aplicativo")
     * tamanho efetivo (menor entre os clientes): 66x60
     * ```
     *
     * The space left over on screen is not orphaned: [AncoraDoQuadro] already
     * knows how to position a grid smaller than the visible area, pinning the
     * content to the bottom.
     */
    private fun aplicarTamanhoDaSessao(cols: Int, rows: Int) {
        viewModelScope.launch {
            val motor = engine ?: return@launch
            if (cols == gridCols && rows == gridRows) return@launch
            TerminalDiag.log("sessao passa a ser ${cols}x$rows")
            gridCols = cols
            gridRows = rows
            motor.resize(cols, rows)
        }
    }

    /**
     * The app has returned to the foreground (the screen lifecycle's
     * `ON_START`). Reconnects NOW, rather than waiting for a heartbeat to fail.
     *
     * This is not an optimisation: it was MEASURED that Android 15+ cuts the
     * app's network ~5.7 s after it leaves the foreground (the
     * `FIREWALL_CHAIN_BACKGROUND` firewall chain) and destroys the sockets,
     * and that, once blocked, the reconnection loop does not even get as far
     * as emitting a SYN. That is: on the way back what exists is always a dead
     * connection and a loop sleeping off its backoff. Waiting discovers
     * nothing that is not already known.
     *
     * A no-op before the grid's first measurement: there is no engine and no
     * socket yet, and [onGridSizeChanged] is what connects.
     */
    fun onVoltouAoPrimeiroPlano() {
        if (engine == null) return
        socketClient.reconectarAgora()
    }

    /**
     * Pastes [text] into the remote terminal, **deciding from the program's
     * real mode** whether the text goes wrapped in the bracketed-paste markers
     * (`ESC[200~` … `ESC[201~`, DECSET 2004).
     *
     * **What was wrong.** The app sent a `paste` control frame and the SERVER
     * wrapped unconditionally (`internal/pty/pty.go`). The server, though,
     * emulates no terminal at all: it is a pipe down to the PTY and has no way
     * of knowing whether the program on the other side turned 2004 on. The one
     * that does know is this app — libghostty-vt here processes all of the
     * PTY's output and tracks the mode. Wrapping with the mode off dumps the
     * markers as literal text onto the command line (the same defect as with
     * the mouse); not wrapping with it on makes a multi-line text be EXECUTED
     * on the spot. Neither decision was the server's to make.
     *
     * So the paste now goes out as already-encoded bytes, in a single binary
     * frame — which the server writes into the PTY with one `Write`, exactly
     * as it did with the `paste` frame. The atomicity bracketed paste demands
     * is preserved; what changed is WHO decides.
     */
    fun sendPaste(text: String) {
        val bytes = engine?.encodePaste(text) ?: return
        if (bytes.isEmpty()) return
        socketClient.send(bytes)
    }

    /**
     * The modes the remote program has turned on in the terminal. It is the
     * question the gesture layer asks before deciding what a touch means —
     * without it, the app was guessing.
     */
    fun currentModes(): TerminalModes = engine?.modes() ?: TerminalModes.NENHUM

    /**
     * Encodes a mouse event for the remote program, or returns `null` when
     * there is nothing to send (nobody asked for mouse reporting, or the
     * finger never left the cell).
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

    /** Polled by the renderer on its own frame cadence — decoupled from write cadence by design. */
    fun currentSnapshot() = engine?.snapshot()

    /**
     * Moves the viewport over the history the emulator already held — negative
     * goes up (into the past).
     *
     * The emulator's history and the "Carregar histórico anterior" panel are
     * TWO different histories, and they stay that way: this one is
     * libghostty-vt's live scrollback, with colour, attribute and position;
     * that one is the session log fetched from the server, plain text, and it
     * reaches further back in time. The gesture scrolls this one; the panel
     * remains the way to whatever is older than the emulator still holds.
     */
    fun scrollViewport(linhas: Int) {
        engine?.scrollViewport(linhas)
    }

    /** Pins the viewport back at the end — the UI's "back to the end". */
    fun scrollToBottom() {
        engine?.scrollToBottom()
    }

    /**
     * Where the viewport sits in the history. Read once per frame, alongside
     * the snapshot: the library does not notify scroll changes, so whoever
     * draws the position has to ask.
     */
    fun currentScrollState(): TerminalScrollState =
        engine?.scrollState() ?: TerminalScrollState.NO_FIM

    private fun onBytesReceived(bytes: ByteArray) {
        viewModelScope.launch {
            if (primerPendente) {
                enfileirarAteOPrimerTerminar(bytes)
            } else {
                engine?.write(bytes)
            }
            lastBytesReceivedAtMs = clock()
            _isStalled.value = false
        }
    }

    /**
     * Holds back one live block until the fetched history has been written.
     *
     * The queue is bounded because the alternative is worse: a session that
     * dumps megabytes the instant it attaches (a busy `tail -f`, a build in
     * flight) would fill memory while the primer is still crossing the
     * network. Once the limit is blown the primer is ABANDONED and the queue
     * released at once — losing the history is an annoyance, freezing the live
     * terminal is a defect.
     */
    private fun enfileirarAteOPrimerTerminar(bytes: ByteArray) {
        pendentes.addLast(bytes)
        bytesPendentes += bytes.size
        if (bytesPendentes > MAX_BYTES_PENDENTES) {
            TerminalDiag.log("primer ABANDONADO: $bytesPendentes B vivos chegaram antes do historico")
            concluirPrimer()
        }
    }

    /**
     * Fetches the session's raw log, CONNECTS, and writes the log into the
     * engine before releasing the live stream.
     *
     * ## Why the connection happens IN HERE, and not in parallel
     *
     * The server only writes to the session log WHILE somebody is attached.
     * That makes the ordering a matter of correctness, not of taste:
     *
     * - fetch the log BEFORE attaching → the log is frozen, and the history
     *   ends EXACTLY where the live stream begins. A perfect join.
     * - attach in parallel → everything the remote program writes between the
     *   attach and the read of the log goes to BOTH sides: to the log (which
     *   the primer replays) and to the socket (which the queue delivers right
     *   after). Those bytes enter the engine TWICE.
     *
     * And replaying a piece of a frame twice is not merely redundant: Claude
     * Code's frame is drawn with RELATIVE cursor movement (`ESC[nA`, `\r`,
     * `ESC[1B`) and with column jumps (`ESC[nG`) that do NOT erase what they
     * skip. Applied from a different position, it paints rules over text and
     * leaves the old characters in the gaps.
     *
     * ## The measurement
     *
     * The defect was reproduced off the device, in the app's OWN engine
     * (libghostty-vt, the vendored `.a`), by feeding the session log with 2000
     * repeated bytes at the end:
     *
     * ```
     * 47|───────────────────────────✔─Update─installed─·─Restart─to─update──
     * 48|  ⧉2%setenta-telasr· modelos-cenarios3·9por-paginaens  Opus 5  ·…
     * ```
     *
     * Which is, cell by cell, the screen the owner photographed. Without the
     * repetition the same engine and the same log yield a clean screen — and
     * so does an independent reference emulator. The bytes were right; the
     * duplicate was ours.
     *
     * Note that it is the SMALL overlap that ruins things: 20 KiB or 100 KiB
     * repeated repaint a whole frame over the top and the screen recomposes
     * itself. It is the short piece — half a frame — that sticks. That is why
     * the defect was intermittent, and why it could not be found by looking at
     * the screen alone.
     *
     * ## The rest of the contract, which still holds
     *
     * ## Why the history is replayed, and not "pasted in"
     *
     * The earlier attempt fetched TEXT (ANSI stripped) and showed it in a
     * panel. It did not work, and the measurement on the real "Aplicativo"
     * session log shows why: the last 5,000 lines of plain text held 511
     * non-empty lines and 150 distinct ones — almost all of it spinner frames.
     * A program that redraws rewrites the SAME cell hundreds of times; without
     * the escapes, every rewrite becomes a line, and the text comes out
     * shredded ("*lg", "Ml", "u2726i…"). The information that would say where
     * each piece goes is precisely the one that was stripped out.
     *
     * What knows how to assemble that is a terminal emulator, and this app has
     * one. So the server hands over the raw bytes and libghostty-vt replays
     * them: the same log yields 5,058 legible lines instead of 511 shredded
     * ones, and the scrollback ends up holding exactly what a terminal that
     * had watched the whole session would have.
     *
     * ## No sanitising the end of the replay
     *
     * The temptation is to emit a "leave the alt-screen"/"reset attributes"
     * after the replay. It would be wrong: if the session is RIGHT NOW inside
     * a `vim`, the log ends inside the alt-screen and that is exactly the
     * engine's correct state. A faithful replay leaves the engine in the same
     * state a terminal that had been present would be in — including the modes
     * (mouse, bracketed paste) the gesture layer consults. Correcting it "for
     * the best" would break that.
     */
    private fun iniciarPrimer() {
        val alvo = TerminalScrollback.porLinhas(scrollbackLinhas)
        viewModelScope.launch {
            val historico = buscarHistorico(alvo.bytesDeLogParaBuscar)

            // ── CONNECT ONLY NOW, AND NEVER BEFORE ──────────────────────────
            // See [iniciarPrimer]'s KDoc: while nobody is attached the server
            // does not write to the log, so opening the socket AFTER the bytes
            // are already in hand makes the history end exactly where the live
            // stream begins. Connecting in parallel is what produced the overlap.
            socketClient.connect()

            if (historico != null) escreverEmPedacos(historico)
            concluirPrimer()
        }
    }

    /**
     * Fetches the raw log, with a time ceiling. `null` when it did not arrive.
     *
     * The ceiling is not decoration: the connection now waits on this fetch,
     * and a slow server must not turn into a terminal that never connects.
     * History is a comfort; a live session is why the screen exists at all.
     */
    private suspend fun buscarHistorico(bytesAlvo: Int): ByteArray? {
        val prazo = withTimeoutOrNull(TETO_DO_PRIMER_MS) {
            for (tentativa in 0 until TENTATIVAS_DO_PRIMER) {
                if (!primerPendente) return@withTimeoutOrNull null
                // TWO SOURCES, IN THIS ORDER.
                //
                // The RENDERED history first: these are the lines that have
                // already left the screen, as append-only text. It is not a
                // replay, so there is no way for it to duplicate — and this
                // KDoc, until now, said the duplication "has no fix". It has
                // none on the side that READS; the server started fixing it on
                // the side that WRITES, by keeping a live emulator on the
                // session's grid.
                //
                // The RAW log as a fallback: an old session (with no history
                // file yet) still loads whatever can be loaded.
                val renderizado = rawLogSource.historico(sessionName, bytesAlvo)
                if (renderizado is RawLogResult.Success && renderizado.bytes.isNotEmpty()) {
                    TerminalDiag.log(
                        "primer sessao=$sessionName ${renderizado.bytes.size} B de " +
                            "${renderizado.total} B do historico renderizado",
                    )
                    return@withTimeoutOrNull renderizado.bytes
                }
                when (val r = rawLogSource.logBruto(sessionName, bytesAlvo)) {
                    is RawLogResult.Success -> {
                        TerminalDiag.log(
                            "primer sessao=$sessionName ${r.bytes.size} B de ${r.total} B do log cru (reserva)",
                        )
                        return@withTimeoutOrNull r.bytes
                    }
                    is RawLogResult.Error -> {
                        TerminalDiag.log("primer FALHOU (${tentativa + 1}): ${r.reason}")
                        if (tentativa + 1 < TENTATIVAS_DO_PRIMER) delay(ESPERA_ENTRE_TENTATIVAS_MS)
                    }
                }
            }
            null
        }
        if (prazo == null) {
            TerminalDiag.log("primer sem historico — conectando so com o fluxo vivo")
        }
        return prazo
    }

    /**
     * Writes the replay in chunks, yielding the turn between them.
     *
     * Up to 16 MiB cross the JNI boundary here, and all of it runs on the same
     * dispatcher that draws the screen (see the class's threading note). In a
     * single write that would be hundreds of milliseconds of lost frames right
     * at the opening — precisely where the person is looking. [yield] hands
     * the turn back to the renderer between chunks: the history appears
     * scrolling in, instead of the screen freezing and blinking up finished.
     */
    private suspend fun escreverEmPedacos(bytes: ByteArray) {
        val motor = engine ?: return
        var inicio = 0
        while (inicio < bytes.size) {
            val fim = minOf(inicio + TAMANHO_DO_PEDACO_DE_REPLAY, bytes.size)
            motor.write(bytes.copyOfRange(inicio, fim))
            inicio = fim
            yield()
        }
    }

    /** Releases the live stream that was awaiting the history, in arrival order. */
    private fun concluirPrimer() {
        if (!primerPendente) return
        primerPendente = false
        val motor = engine
        while (pendentes.isNotEmpty()) {
            val bloco = pendentes.removeFirst()
            motor?.write(bloco)
        }
        bytesPendentes = 0
        entregarOComandoDaPonte()
    }

    /**
     * Inserts on the prompt line the command another screen sent through the
     * bridge, if there is one.
     *
     * **Here and not before.** While the primer runs, the engine is taking in
     * the session's entire history; pasting into the middle of that would put
     * the command at a position the following replay would overwrite, and the
     * person would watch the terminal "eat" what they asked for. After the
     * primer the grid is already the real one.
     *
     * The command arrives PASTED, never executed — see [PonteComOTerminal].
     * The person is the one who presses Enter, after reading it.
     */
    private fun entregarOComandoDaPonte() {
        val pedido = PonteComOTerminal.consumir() ?: return
        sendPaste(pedido.comando)
        _origemDoComandoDaPonte.value = pedido.origem
        viewModelScope.launch {
            delay(DURACAO_DO_RECADO_DA_PONTE_MS)
            // Compare before clearing: a second command arriving inside the
            // window swaps the line, and clearing blindly here would erase the NEW one.
            if (_origemDoComandoDaPonte.value == pedido.origem) {
                _origemDoComandoDaPonte.value = null
            }
        }
    }

    // THE ATTACH-TIME HISTORY CLEAR HAS BEEN REMOVED.
    //
    // It went in at 0.1.12 to erase the copies the attach repaint left behind.
    // It was treating the wrong symptom — the duplication came from the
    // keyboard's SIGWINCH storm, not from the attach — and it created a WORSE
    // defect, which the operator reported like this: "the drag only works once
    // the keyboard has come up for the first time".
    //
    // The cause, measured on the emulator with the gesture instrumented:
    //
    //   scroll: pointerInput INICIADO geracao=1
    //   scroll: DOWN geracao=1 consumido=false
    //   scroll: passou o slop geracao=1 vertical=true dy=21
    //
    // The gesture was recognised perfectly BEFORE the keyboard. There was no
    // focus problem, no consumption by the AndroidView, no restart of the
    // pointerInput — all three suspicions fell at once. **There was nothing to
    // scroll**: this clear had just wiped the whole scrollback. And what
    // repopulated it was the repaint the keyboard itself triggered — hence the
    // symptom looking as though the keyboard caused it.
    //
    // Erasing the session's history to hide a copy was trading an annoyance
    // for data loss. The right way with the copy is not to produce it: the
    // resize debounce (which killed 20 SIGWINCH per tap) and, if any is still
    // left over, the grid not shrinking with the keyboard.

    private fun evaluateStall() {
        if (connectionState.value != ConnectionState.Live) {
            _isStalled.value = false
            return
        }
        val sentAt = lastSendAtMs
        val receivedAt = lastBytesReceivedAtMs
        val now = clock()
        // Stalled means: the user tried to type (sentAt is non-null and,
        // when a reply has ever arrived, more recent than it) and got no
        // bytes back for stallThresholdMs, while the socket itself still
        // reports Live. A Live connection with nobody typing is just a
        // healthy idle prompt, not a stall.
        _isStalled.value = sentAt != null &&
            (receivedAt == null || sentAt > receivedAt) &&
            (now - sentAt) > stallThresholdMs
    }

    override fun onCleared() {
        socketClient.disconnect()
        engine?.close()
    }

    companion object {
        /**
         * The silence that marks the end of the attach repaint: once this long
         * has passed with no new byte, the final frame is already on screen
         * and whatever is left above it is wobble scaffolding. See
         * `reagendarLimpezaDoAttach`.
         */
        const val SILENCIO_PARA_LIMPAR_ATTACH_MS = 1_200L

        /**
         * How long to wait for the grid to settle before sending the size.
         * Covers Android's IME animation (~200 ms) without perceptibly
         * delaying a screen rotation.
         */
        const val ESTABILIZACAO_DE_TAMANHO_MS = 220L

        /**
         * 8s: long enough that an ordinary round-trip — even over a slow or
         * briefly congested network — never false-positives, short enough
         * that a genuinely hung remote shell surfaces well within the time a
         * person would otherwise spend re-typing and wondering if anything
         * is happening. Documented here since the plan left the exact
         * threshold to this implementation's discretion.
         */
        const val STALL_THRESHOLD_MS_DEFAULT = 8_000L

        /**
         * 1.5 s: above the measured ceiling of a healthy reconnection on this
         * path (0.5 s to 1 s), so that everyday app switching lights no banner
         * at all, and below the point at which going unanswered starts to look
         * like a freeze.
         */
        const val BANNER_GRACE_MS_DEFAULT = 1_500L

        /**
         * Ceiling on the live stream held back while the history is fetched.
         * 2 MiB is more than any normal attach produces (a full-screen
         * program's frame fits in tens of KiB) and small enough not to weigh
         * on memory should something be dumping output non-stop. See
         * [enfileirarAteOPrimerTerminar].
         */
        const val MAX_BYTES_PENDENTES = 2 * 1024 * 1024

        /**
         * The slice of the replay written between one yield and the next.
         * 256 KiB is large enough for the per-JNI-call cost to disappear and
         * small enough to fit comfortably inside a 16 ms frame.
         */
        const val TAMANHO_DO_PEDACO_DE_REPLAY = 256 * 1024

        /**
         * Two attempts at fetching the history. The first competes with the
         * attach itself for the network — the most congested moment of the
         * whole screen — and a failure there is usually transient. Insisting
         * beyond that would only delay releasing the live stream.
         */
        const val TENTATIVAS_DO_PRIMER = 3

        /** Breathing room between the primer's attempts. */
        const val ESPERA_ENTRE_TENTATIVAS_MS = 400L

        /**
         * Ceiling on waiting for the history BEFORE connecting.
         *
         * The connection waits on the log fetch (see [iniciarPrimer]), so this
         * ceiling is what stops a slow server from becoming a terminal that
         * never connects.
         *
         * ## Why it is NOT three seconds
         *
         * It was, and that was too little. Three seconds came from the path
         * measured over wi-fi, where the fetch takes hundreds of
         * milliseconds. But the body is the whole session log — a few MB,
         * ~15x smaller under the transport's gzip, still hundreds of KB — and
         * the attach happens exactly when the device has just come back to
         * the foreground, with the network freshly restored and contended for
         * by all the rest of the app. Once the ceiling is blown the primer
         * gives up, the grid stays EMPTY, and the remote program only redraws
         * when it has something new to say: a black screen until somebody
         * types. That was the owner's report.
         *
         * Twelve seconds because the cost of the two mistakes is asymmetric.
         * Waiting too long delays a screen that, on a network that bad, was
         * not going to work anyway; giving up too early hands over a black
         * screen with a live session on the other end — and that one is worse,
         * because it looks broken.
         */
        const val TETO_DO_PRIMER_MS = 12_000L

        /**
         * How long the "came from X" line stays on screen.
         *
         * Six seconds because it is a NOTE and not a state: long enough to be
         * read by someone who was looking at the keyboard when the terminal
         * opened, and short enough not to become one more permanent banner
         * competing for the grid's few rows.
         */
        const val DURACAO_DO_RECADO_DA_PONTE_MS = 6_000L
    }

}
