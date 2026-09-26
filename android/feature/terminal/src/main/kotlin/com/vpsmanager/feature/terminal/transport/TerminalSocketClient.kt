package com.vpsmanager.feature.terminal.transport

import com.vpsmanager.data.terminal.TerminalTicketSource
import com.vpsmanager.data.terminal.TerminalWebSocket
import com.vpsmanager.data.terminal.TerminalWebSocketFactory
import com.vpsmanager.data.terminal.TerminalWebSocketListener
import com.vpsmanager.data.terminal.WsTicketResult
import com.vpsmanager.feature.terminal.input.ByteSink
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

/** `internal/pty/pty.go`'s close code for "this session is confirmed gone, do not retry." */
internal const val WS_CLOSE_SESSION_ENDED = 4404

// Backoff schedule for the reconnect loop below: base 500ms, doubling each
// attempt, capped at 15s. Chosen so the first retry after a blip is fast
// enough to feel instant, while a genuinely down server (or BFF) never gets
// hammered — 500, 1000, 2000, 4000, 8000, 15000, 15000, ... Documented here
// (and in the plan's SUMMARY) since the plan left the exact constants to
// this implementation's discretion.
internal const val BACKOFF_BASE_MS = 500L
internal const val BACKOFF_FACTOR = 2L
internal const val BACKOFF_CAP_MS = 15_000L

/**
 * Ceiling for the outbound queue used while the socket is down, in bytes.
 * 8 KiB is orders of magnitude above any burst of human typing (even a long
 * command line does not exceed a few hundred bytes) and still small enough
 * never to become a surprise dump into the shell.
 */
internal const val MAX_PENDENTE_BYTES = 8 * 1024

/**
 * Deadline for the outbound queue. Measured on the emulator, a healthy
 * reconnect on this path takes 0.5 s to 1 s (SYN + TLS + ticket + upgrade);
 * 10 s leaves an order of magnitude of slack for a bad network and still
 * guarantees that nobody comes back from five minutes away and watches what
 * they typed walk into a shell that is no longer the same mental context.
 * Past that, it is dropped and the user is TOLD — what must never happen is
 * for it to vanish in silence.
 */
internal const val PENDENTE_TTL_MS = 10_000L

internal fun backoffDelayMs(attempt: Int): Long {
    if (attempt <= 1) return BACKOFF_BASE_MS
    var delayMs = BACKOFF_BASE_MS
    repeat(attempt - 1) {
        delayMs = (delayMs * BACKOFF_FACTOR).coerceAtMost(BACKOFF_CAP_MS)
    }
    return delayMs
}

/**
 * `ByteSink` implementation over a single, auto-reconnecting `/ws/shell`
 * connection, plus the `resize` control frame the wire contract requires
 * (`internal/pty/pty.go`'s `ctrlMsg`).
 *
 * **There is no `sendPaste` here any more.** There used to be: it sent a
 * `paste` control frame, and the SERVER wrapped the text between `ESC[200~`
 * and `ESC[201~` unconditionally. Except that the server does not emulate a
 * terminal — it is a pipe to the PTY — and so it has no way of knowing
 * whether the program on the other side turned bracketed paste on (DECSET
 * 2004). The one that knows is this app's VT emulator. Today a paste goes out
 * through [send], already encoded by `TerminalEngine.encodePaste`, in a
 * single binary frame: the server writes a binary frame into the PTY with one
 * `Write`, so the atomicity a paste requires still stands — what changed was
 * WHO decides the wrapping. A `sendPaste` that ignored the mode would be a
 * trap for the next caller, so it went away rather than sit there unused.
 *
 * [connect] starts (or restarts, if already stopped) the connect-and-retry
 * loop and returns immediately; [state] is how a caller observes progress —
 * inferring "stuck" from silence is forbidden. The very first attempt this
 * instance ever makes omits `attach=1`; every attempt after that includes it
 * (`internal/pty/pty.go` ~lines 110-320): `attach=1` skips scrollback replay
 * and turns a genuinely-ended session into WS close code
 * [WS_CLOSE_SESSION_ENDED] instead of an ordinary retryable failure — which
 * this loop treats as terminal (no further attempts) rather than retried
 * forever against a session the server has already confirmed is gone.
 *
 * Every attempt (first connect and every reconnect alike) fetches a *fresh*
 * ticket from [ticketSource] — the previous one-shot ticket is already
 * consumed and reusing it would just fail.
 */
class TerminalSocketClient(
    private val name: String,
    private val ticketSource: TerminalTicketSource,
    private val webSocketFactory: TerminalWebSocketFactory,
    private val wsBaseUrl: String,
    private val scope: CoroutineScope,
    private val onBytes: (ByteArray) -> Unit,
    /**
     * The EFFECTIVE size of the session, as announced by the server.
     *
     * A session can have several clients, and the PTY sits at the SMALLEST of
     * them — the minimum-size rule. The half that makes it work is this:
     * EVERY client draws a grid the size of the SESSION, not of its own
     * window. Whoever has the larger window sees the session with space
     * around it.
     *
     * Without obeying this, the remote program wraps its lines at N columns
     * while the client draws on a grid of M: everything lands in the wrong
     * place. That was the defect, measured in the server log.
     */
    private val onTamanhoDaSessao: (cols: Int, rows: Int) -> Unit = { _, _ -> },
    /**
     * Whether a FRESH attach should ask the server for the block of history it
     * re-emits (`attachReplay`, `internal/pty/pty.go`).
     *
     * `false` when the app itself is the one priming the screen, fetching the
     * raw log and replaying it into libghostty-vt (see
     * `TerminalViewModel.iniciarPrimer`). Both paths deliver the SAME thing,
     * and leaving both switched on would show the history twice — the classic
     * defect of this terminal.
     *
     * The app's path wins on three measurements, not on taste: the server's is
     * capped at 128 KiB, is discarded wholesale when the stream comes from a
     * program that repaints (which was the case for the operator's main
     * session: zero bytes of history), and does not reach the `.1` of the
     * rotation. The app's fetches up to the entire log and replays it in a
     * real emulator.
     *
     * Still `true` by default so that any caller that does not prime on its
     * own keeps the old behaviour, instead of ending up with no history at all
     * by omission.
     */
    private val pedirReplayDoServidor: Boolean = true,
    private val delayer: suspend (Long) -> Unit = { delay(it) },
    private val agora: () -> Long = System::currentTimeMillis,
) : ByteSink {

    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.Connecting)
    val state: StateFlow<ConnectionState> = _state.asStateFlow()

    @Volatile
    private var socket: TerminalWebSocket? = null

    /**
     * The socket that REALLY opened — not the one that is trying to open.
     *
     * ## The defect this field closes
     *
     * [socket] is assigned the instant the attempt BEGINS
     * (`webSocketFactory.open` returns straight away; `onOpen` arrives later,
     * on another thread). In the meantime `send` saw a non-null field,
     * concluded there was a connection, and pushed the bytes into a socket
     * that was still closed — which drops them in silence. And, since it was
     * not null, the queue was never engaged.
     *
     * On the device the result was exactly the owner's complaint: **you cannot
     * type during the reconnect**. It is not a frozen screen — it is the text
     * going into a pipe that does not exist yet, without even becoming pending
     * typing.
     *
     * The window is not theoretical: Android 15+ cuts the app's network a few
     * seconds after it leaves the foreground, so on the way BACK there is
     * always a reconnect in flight, and that is exactly when people type.
     */
    @Volatile
    private var socketAberto: TerminalWebSocket? = null

    @Volatile
    private var everConnected = false

    @Volatile
    private var shouldRun = false

    private var loopJob: Job? = null

    /**
     * What the operator typed while there was no socket. It exists because
     * `send` used to be `socket?.sendBytes(bytes)`: with the connection down
     * the bytes evaporated without any signal at all — the operator typed a
     * whole command, looked for the echo on screen that libghostty-vt does not
     * itself produce (there is no local echo: the remote shell is what
     * echoes), and concluded that "the app ate the command".
     *
     * The window in which this happens is real and short: Android 15+ cuts the
     * app's network a few seconds after it leaves the foreground, so on the
     * way BACK there is always a reconnect in flight, and it is exactly in
     * that interval (0.5 s to 1 s, measured) that someone is already typing.
     */
    private val pendentes = ArrayDeque<ByteArray>()
    private var pendentesBytes = 0
    private var pendentesDesde = 0L

    private val _digitacaoDescartada = MutableStateFlow(false)

    /**
     * True when the queue above overran either its size or its deadline and
     * was thrown away. It is the honest alternative to silence: either the
     * bytes go out, or somebody is told that they did not. Back to false as
     * soon as a new connection manages to drain it (or when nothing is pending
     * any more).
     */
    val digitacaoDescartada: StateFlow<Boolean> = _digitacaoDescartada.asStateFlow()

    private val _digitacaoPendente = MutableStateFlow("")

    /**
     * What has been typed and has not gone up yet, as readable text.
     *
     * It exists because in a terminal what you type only appears once the
     * SERVER echoes it back — with no connection there is no echo, and the
     * screen stays mute while the keystrokes pile up here. A mute screen is
     * indistinguishable from a frozen app, which was exactly the owner's
     * complaint.
     *
     * See [resumoDaDigitacao] for why this is NOT written onto the grid.
     */
    val digitacaoPendente: StateFlow<String> = _digitacaoPendente.asStateFlow()

    override fun send(bytes: ByteArray) {
        if (bytes.isEmpty()) return
        // OPEN, not merely existing. See [socketAberto]: the difference
        // between the two is the whole window of the reconnect, which is where
        // the typing used to vanish.
        val vivo = socketAberto
        if (vivo != null) {
            vivo.sendBytes(bytes)
            return
        }
        enfileirar(bytes)
    }

    @Synchronized
    private fun enfileirar(bytes: ByteArray) {
        val agoraMs = agora()
        if (pendentes.isEmpty()) pendentesDesde = agoraMs
        val estourouTamanho = pendentesBytes + bytes.size > MAX_PENDENTE_BYTES
        val estourouPrazo = agoraMs - pendentesDesde > PENDENTE_TTL_MS
        if (estourouTamanho || estourouPrazo) {
            pendentes.clear()
            pendentesBytes = 0
            _digitacaoPendente.value = ""
            _digitacaoDescartada.value = true
            return
        }
        pendentes.addLast(bytes)
        pendentesBytes += bytes.size
        _digitacaoPendente.value = resumoDaDigitacao(_digitacaoPendente.value, bytes)
    }

    /**
     * Sends whatever was held back, in the order it was typed, to [destino].
     * Idempotent on purpose: it is called both from `onOpen` and right after
     * the socket is assigned, because those two things happen on different
     * threads and the order between them is not guaranteed.
     */
    @Synchronized
    private fun drenarPendentes(destino: TerminalWebSocket) {
        // Getting here IS the confirmation of opening: both calls (`onOpen`
        // and the one that follows the assignment) only happen with `abriu`
        // true. Marking it here keeps "open" in a single definition, instead
        // of repeating the condition in two places that could drift apart.
        socketAberto = destino
        while (pendentes.isNotEmpty()) {
            destino.sendBytes(pendentes.removeFirst())
        }
        pendentesBytes = 0
        // The summary goes away once what it was summarising has REALLY gone
        // up. From here on the text is shown by the server's echo, on the
        // grid, which is the right place — the banner only existed while that
        // echo could not get through.
        _digitacaoPendente.value = ""
        _digitacaoDescartada.value = false
    }

    /**
     * The size the grid has RIGHT NOW, kept so that it can be reasserted on
     * every connection. See [sendResize] and [reafirmarTamanho].
     */
    @Volatile
    private var tamanhoDaGrade: Pair<Int, Int>? = null

    /**
     * Tells the server the size of the grid — and REMEMBERS it.
     *
     * ## Why remember it
     *
     * This method writes to `socket?`: with the connection down, the message
     * disappears in silence. And the only caller
     * (`TerminalViewModel.aplicarTamanho`) starts with
     * `if (cols == gridCols && rows == gridRows) return`, so a change of
     * height that happened during the outage is NEVER resent — the app already
     * believes it has said its piece.
     *
     * Remembering it here turns "I said it once" into "this is the truth", and
     * [reafirmarTamanho] reimposes it every time a socket opens.
     */
    fun sendResize(cols: Int, rows: Int) {
        tamanhoDaGrade = cols to rows
        socket?.sendText(TerminalControlMessage.encode(TerminalControlMessage.resize(cols, rows)))
    }

    /**
     * Reasserts the size of the grid as soon as the socket opens.
     *
     * ## The defect this fixes, measured
     *
     * The size of the PTY is not the client's property alone: the SERVER used
     * to move it by itself, on purpose, in the attach `repaint-wobble` — it
     * halved the rows and then restored them to the last value the client had
     * reported. Recorded in the service log:
     *
     * ```
     *   20:46:35  client resize:      67x53 → 67x47
     *   20:46:45  repaint-wobble:     67x48 → 67x24 → 67x48
     *   20:46:47  client resize:      67x48 → 67x53
     * ```
     *
     * The wobble no longer reaches this client: the `replay=0` it was already
     * sending came to mean, for the server, "I rebuild the screen myself —
     * do not touch the PTY geometry" (`primingDoServidor`, in
     * `internal/pty/pty.go`). The reassertion below is still NECESSARY, and
     * for a reason that was never the wobble: the web panel may be attached to
     * the same session and moves the PTY size on its own account.
     *
     * Note the 48: the wobble restored a STALE value, from before the client's
     * last change of height. Add to that the fact that the client only speaks
     * up when ITS own size changes, and the two sides end up in disagreement
     * with nothing to reconcile them — the remote program paints for a screen
     * of N rows onto a grid of M. Absolute positioning and `ESC[J` land in the
     * wrong place: text at the top, emptiness below.
     *
     * That is why typing "fixed it": the IME composition strip comes and goes,
     * the height genuinely changes, and only then did the app go back to
     * telling the server.
     *
     * It costs one text frame per connection — and a new connection is
     * precisely the moment when the two sides are most likely to disagree.
     */
    private fun reafirmarTamanho() {
        val (cols, rows) = tamanhoDaGrade ?: return
        socket?.sendText(TerminalControlMessage.encode(TerminalControlMessage.resize(cols, rows)))
        TerminalDiag.log("tamanho reafirmado ${cols}x$rows sessao=$name")
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
     * Explicit disconnect: closes the socket and permanently stops
     * auto-reconnect. Neither [ConnectionState.Disconnected] nor
     * [ConnectionState.SessionEnded] ever triggers a reconnect on its own —
     * only [connect] does, and only the caller decides to call it again.
     */
    fun disconnect() {
        shouldRun = false
        loopJob?.cancel()
        closeSocket(1000, "client disconnect")
        _state.value = ConnectionState.Disconnected
    }

    /**
     * The operator came back to the app: try NOW, without waiting for the
     * backoff to run out.
     *
     * Without this, coming back to the app hit the worst case of the loop:
     * Android had already torn down the socket when the app was sent to the
     * background, the loop was sleeping out its backoff, and the screen sat on
     * "Reconnecting…" for up to 15 s with the server up and the `dtach`
     * session intact on the other side.
     *
     * It cancels and restarts the loop rather than signalling a `delay` — that
     * way `attempt` goes back to zero for free. **It does not touch
     * [everConnected]**, and that is what preserves the screen: the attempt
     * still carries `attach=1`, which asks the server NOT to resend the
     * scrollback. The grid the operator is looking at is the one
     * [com.vpsmanager.feature.terminal.ui.GridEngine] holds in memory (the
     * ViewModel survives a trip to the background), so reconnecting redraws
     * nothing — if it asked for a replay, the history would appear DUPLICATED
     * on top of what is already on screen.
     *
     * It does not revive an explicit [disconnect] (there `shouldRun` is false
     * and only [connect] undoes that), nor does it disturb a connection that
     * is already alive.
     */
    fun reconectarAgora() {
        if (!shouldRun) return
        if (_state.value == ConnectionState.Live) return
        loopJob?.cancel()
        _state.value = ConnectionState.Connecting
        loopJob = scope.launch { runLoop() }
    }

    private fun closeSocket(code: Int, reason: String) {
        socket?.close(code, reason)
        socket = null
        // It goes away too: a socket we closed ourselves is no longer open,
        // and leaving this field behind would send the next keystrokes into
        // the dead pipe instead of into the queue — which is the whole defect
        // of this file.
        socketAberto = null
    }

    private sealed interface AttemptOutcome {
        data object Ended : AttemptOutcome

        /**
         * [chegouALive] says whether THIS attempt got as far as opening
         * (onOpen) before it fell. It is what separates "the server is down
         * and I have been failing all along" from "I was connected and the
         * connection has just dropped" — only the first case deserves to
         * inherit the backoff ladder.
         */
        data class Failed(val reason: String, val chegouALive: Boolean = false) : AttemptOutcome
    }

    private suspend fun runLoop() {
        var attempt = 0
        while (shouldRun) {
            // `attach=1` means "I ALREADY HAVE the screen" — it holds on a
            // reconnect, where the in-memory grid is intact and a replay would
            // duplicate it. It does not hold on the first connection: there
            // the grid is born empty and what fills it is `iniciarPrimer`,
            // with the raw log replayed into the engine.
            //
            // The `replay=0` that goes along with it is NOT merely "do not
            // send me history". The server reads the two as a single question
            // — who rebuilds the screen — and because of that it also no
            // longer touches the PTY geometry on our behalf
            // (`primingDoServidor`, `internal/pty/pty.go`). That was the
            // "repaint-wobble": halving the rows and restoring them, on every
            // fresh attach. It merged two layouts inside the remote program's
            // buffer and handed back a scrambled screen — the corrupted text
            // shows up in the server's RAW bytes, before the app touches them.
            val attach = everConnected
            when (val outcome = connectAttempt(attach)) {
                is AttemptOutcome.Ended -> {
                    shouldRun = false
                    _state.value = ConnectionState.SessionEnded
                    return
                }
                is AttemptOutcome.Failed -> {
                    if (!shouldRun) return
                    // An attempt that got as far as Live RESETS the ladder.
                    // Without this, `attempt` only ever grew: each trip to the
                    // background (Android cuts the app's network in about 6s,
                    // see docs) added another rung, and after five trips the
                    // return to the app sat for 15s on "Reconnecting (6)…" —
                    // with the server up and the dtach session intact on the
                    // other side. Backoff exists so as not to hammer a server
                    // that is down, not to punish somebody for switching
                    // apps.
                    if (outcome.chegouALive) attempt = 0
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
        val url = buildUrl(wsBaseUrl, name, ticket, attach, pedirReplayDoServidor)
        TerminalDiag.log("tentativa sessao=$name attach=$attach everConnected=$everConnected")
        val outcome = CompletableDeferred<AttemptOutcome>()
        // How many binary messages this attempt has received so far. The
        // FIRST one of a connection WITHOUT `attach=1` is the server's
        // scrollback replay (`internal/pty/pty.go` writes that block before
        // wiring up the proxy), and it is precisely the one to be measured.
        var mensagens = 0
        // Atomic, not a captured `var`: onOpen arrives on the WebSocket
        // thread and is read in onClosed/onFailure, which may come from
        // another.
        val abriu = AtomicBoolean(false)
        val listener = object : TerminalWebSocketListener {
            override fun onOpen() {
                TerminalDiag.log("onOpen sessao=$name attach=$attach")
                everConnected = true
                abriu.set(true)
                _state.value = ConnectionState.Live
                // Deferred to the dispatcher because `onOpen` can arrive on
                // the WebSocket thread BEFORE `socket` has been assigned
                // below; by the time this block runs, it has been.
                //
                // The size goes BEFORE whatever was held back: what was typed
                // while the connection was away has to reach a PTY that
                // already has the right geometry, otherwise the remote program
                // answers by painting for the wrong screen.
                scope.launch {
                    reafirmarTamanho()
                    socket?.let(::drenarPendentes)
                }
            }

            override fun onTextMessage(text: String) {
                val tamanho = TerminalControlMessage.tamanhoDaSessao(text) ?: return
                TerminalDiag.log("tamanho da sessao=${tamanho.first}x${tamanho.second}")
                onTamanhoDaSessao(tamanho.first, tamanho.second)
            }

            override fun onBinaryMessage(bytes: ByteArray) {
                mensagens += 1
                if (mensagens <= 3) {
                    TerminalDiag.log("msg#$mensagens attach=$attach bytes=${bytes.size}")
                }
                // The FIRST message of a connection WITHOUT `attach=1` is
                // the scrollback replay, and only it: the server writes that
                // block with a single `conn.WriteMessage` BEFORE wiring up the
                // PTY proxy (`internal/pty/pty.go`), so nothing from the live
                // stream can arrive ahead of it. With `attach=1` the server
                // sends no replay at all, and there is nothing to filter here.
                if (pedirReplayDoServidor && !attach && mensagens == 1 &&
                    ReplayDeAttach.ehRepinturaDiferencial(bytes)
                ) {
                    TerminalDiag.log("replay DESCARTADO (repintura diferencial) bytes=${bytes.size}")
                    return
                }
                onBytes(bytes)
            }

            override fun onClosed(code: Int, reason: String) {
                socket = null
                socketAberto = null
                if (code == WS_CLOSE_SESSION_ENDED) {
                    outcome.complete(AttemptOutcome.Ended)
                } else {
                    outcome.complete(AttemptOutcome.Failed(reason, abriu.get()))
                }
            }

            override fun onFailure(reason: String) {
                socket = null
                socketAberto = null
                outcome.complete(AttemptOutcome.Failed(reason, abriu.get()))
            }
        }
        val aberto = webSocketFactory.open(url, listener)
        socket = aberto
        // The other half of the race above: if `onOpen` had already happened
        // by the time we got here, its drain saw a null `socket` and did
        // nothing. Draining is synchronized and empties the queue, so calling
        // it twice does not send anything twice.
        if (abriu.get()) drenarPendentes(aberto)
        return outcome.await()
    }

    companion object {
        /**
         * Builds the `/ws/shell` URL with BOTH flags, which answer different
         * questions:
         *
         * - `attach=1` — "I already have the screen". A reconnect. The server
         *   neither re-emits history nor forces a repaint; the in-memory grid
         *   is intact and touching it would only duplicate what is already
         *   there.
         * - `replay=0` — "I take care of the history". The server skips only
         *   the history block, and STILL forces the repaint of the current
         *   frame, which is the one thing that is in no log.
         */
        internal fun buildUrl(
            wsBaseUrl: String,
            name: String,
            ticket: String,
            attach: Boolean,
            replayDoServidor: Boolean = true,
        ): String {
            val base = "$wsBaseUrl$WS_SHELL_PATH?name=$name&ticket=$ticket"
            // `size=1` tells the server that this client UNDERSTANDS the
            // effective-size announcement and will draw the session's grid.
            // Without asking, an older client would receive the JSON and write
            // it to the screen. `quadro=1` says this client accepts a RENDERED
            // CROP of the session's screen when its own window is smaller than
            // that screen. It is what takes the device out of being a CEILING
            // on the session size: without it, a phone at 53 columns shrinks a
            // 120-column desktop — which is what was reported. With it, the
            // session stays at the largest size and the device receives a crop
            // composed for its own window.
            val comAttach = if (attach) "$base&attach=1&size=1&quadro=1" else "$base&size=1&quadro=1"
            return if (replayDoServidor) comAttach else "$comAttach&replay=0"
        }
    }
}
