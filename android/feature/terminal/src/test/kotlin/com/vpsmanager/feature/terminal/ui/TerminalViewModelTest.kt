package com.vpsmanager.feature.terminal.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModelStore
import com.vpsmanager.data.terminal.RawLogResult
import com.vpsmanager.data.terminal.TerminalRawLogSource
import com.vpsmanager.data.terminal.TerminalTicketSource
import com.vpsmanager.data.terminal.TerminalWebSocket
import com.vpsmanager.data.terminal.TerminalWebSocketFactory
import com.vpsmanager.data.terminal.TerminalWebSocketListener
import com.vpsmanager.data.terminal.WsTicketResult
import com.vpsmanager.feature.terminal.prefs.TerminalScrollback
import com.vpsmanager.feature.terminal.transport.ConnectionState
import com.vpsmanager.terminalengine.CellSnapshot
import com.vpsmanager.terminalengine.MouseAction
import com.vpsmanager.terminalengine.MouseButton
import com.vpsmanager.terminalengine.MouseGeometry
import com.vpsmanager.terminalengine.TerminalModes
import com.vpsmanager.terminalengine.TerminalScrollState
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.TestResult
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** Mirrors `TerminalSocketClientTest`'s fake ticket source. */
private class FakeTicketSource(private val tickets: MutableList<String>) : TerminalTicketSource {
    override suspend fun wsTicket(name: String): WsTicketResult {
        if (tickets.isEmpty()) return WsTicketResult.Error("sem mais tickets fake")
        return WsTicketResult.Success(ticket = tickets.removeAt(0), expiresIn = 60)
    }
}

/** Mirrors `TerminalSocketClientTest`'s recording socket + factory. */
private class RecordingWebSocket : TerminalWebSocket {
    val binaryFrames = mutableListOf<ByteArray>()
    val textFrames = mutableListOf<String>()
    override fun sendBytes(bytes: ByteArray): Boolean {
        binaryFrames += bytes
        return true
    }
    override fun sendText(text: String): Boolean {
        textFrames += text
        return true
    }
    override fun close(code: Int, reason: String): Boolean = true
}

/**
 * Records every byte ceiling requested and returns the results in order. Empty
 * = "session with no log", which is the normal case for a freshly created
 * session, not an error.
 */
private class FakeRawLogSource(private val resultados: MutableList<RawLogResult>) : TerminalRawLogSource {
    val bytesPedidos = mutableListOf<Int>()

    /**
     * Results for the RENDERED history. Empty = "this session has no history
     * file yet", which is the case for every older session — and that is why
     * the primer falls back to the raw log instead of giving up.
     */
    val doHistorico = mutableListOf<RawLogResult>()
    val pedidosDeHistorico = mutableListOf<Int>()

    override suspend fun historico(name: String, bytes: Int): RawLogResult {
        pedidosDeHistorico += bytes
        return if (doHistorico.isEmpty()) RawLogResult.Success(ByteArray(0), 0) else doHistorico.removeAt(0)
    }

    override suspend fun logBruto(name: String, bytes: Int): RawLogResult {
        bytesPedidos += bytes
        return if (resultados.isEmpty()) RawLogResult.Success(ByteArray(0), 0) else resultados.removeAt(0)
    }
}

private class FakeWebSocketFactory : TerminalWebSocketFactory {
    val openedUrls = mutableListOf<String>()
    val sockets = mutableListOf<RecordingWebSocket>()
    val listeners = mutableListOf<TerminalWebSocketListener>()
    override fun open(url: String, listener: TerminalWebSocketListener): TerminalWebSocket {
        openedUrls += url
        listeners += listener
        val socket = RecordingWebSocket()
        sockets += socket
        return socket
    }
}

/** Records every call [TerminalViewModel] makes on its [GridEngine] seam. */
private class FakeGridEngine(private val snapshotToReturn: CellSnapshot) : GridEngine {
    val writes = mutableListOf<ByteArray>()
    val resizeCalls = mutableListOf<Pair<Int, Int>>()
    var closed = false

    /** How many times the history was cleared — the scaffolding for the attach repaint. */
    var limpezasDeHistorico = 0
        private set

    override fun limparHistorico() {
        limpezasDeHistorico++
    }

    /** What this test's "remote program" switched on. Swapped live, like an `htop` that opens and closes. */
    var modes = TerminalModes.NENHUM

    /** Bytes the native encoder would return; `null` = "this event produces no report". */
    var mouseBytes: ByteArray? = null
    val mouseCalls = mutableListOf<MouseAction>()
    val pastesCodificados = mutableListOf<String>()

    /** Every scroll request, in lines — negative scrolls up (into the past). */
    val rolagens = mutableListOf<Int>()
    var voltasAoFim = 0

    /** A fake viewport, just enough for the test to observe position. */
    var estadoDeRolagem = TerminalScrollState(total = 100, offset = 90, visiveis = 10, noFim = true)

    override fun write(bytes: ByteArray) { writes += bytes }
    override fun snapshot(): CellSnapshot = snapshotToReturn
    override fun resize(cols: Int, rows: Int) { resizeCalls += cols to rows }
    override fun close() { closed = true }
    override fun modes(): TerminalModes = modes
    override fun encodeMouse(
        action: MouseAction,
        button: MouseButton,
        positionXPx: Float,
        positionYPx: Float,
        geometry: MouseGeometry,
        anyButtonPressed: Boolean,
    ): ByteArray? {
        mouseCalls += action
        return mouseBytes
    }
    override fun scrollViewport(linhas: Int) {
        rolagens += linhas
        val novoOffset = (estadoDeRolagem.offset + linhas).coerceIn(0, estadoDeRolagem.historico)
        estadoDeRolagem = estadoDeRolagem.copy(
            offset = novoOffset,
            noFim = novoOffset >= estadoDeRolagem.historico,
        )
    }

    override fun scrollToBottom() {
        voltasAoFim++
        estadoDeRolagem = estadoDeRolagem.copy(offset = estadoDeRolagem.historico, noFim = true)
    }

    override fun scrollState(): TerminalScrollState = estadoDeRolagem

    override fun encodePaste(text: String): ByteArray {
        pastesCodificados += text
        // Mirrors the real contract: with 2004 on the text arrives wrapped,
        // without it the line breaks become carriage returns.
        return if (modes.bracketedPaste) {
            "\u001b[200~$text\u001b[201~".toByteArray(Charsets.UTF_8)
        } else {
            text.replace('\n', '\r').toByteArray(Charsets.UTF_8)
        }
    }
}

/**
 * A structurally valid 1x1 grid — content is irrelevant to these tests, only
 * identity is checked. [CellSnapshot]'s only public construction path
 * ([CellSnapshot.fromBuffer]) is `internal` to `:terminal-engine`, invisible
 * across the module boundary even to `:feature-terminal`'s own test source
 * set (friend-paths only bridge a module's own main/test split, not two
 * separate Gradle modules) — so this reaches its private constructor via
 * reflection rather than depending on a native-backed engine just to obtain
 * one immutable value object.
 */
private fun trivialSnapshot(): CellSnapshot {
    val cell = CellSnapshot.Cell(
        codepoint = 'x'.code,
        fg = null,
        bg = null,
        bold = false,
        italic = false,
        faint = false,
        blink = false,
        inverse = false,
        invisible = false,
        strikethrough = false,
        overline = false,
        underline = 0,
        wide = CellSnapshot.Wide.NARROW,
    )
    val constructor = CellSnapshot::class.java.getDeclaredConstructor(
        Int::class.javaPrimitiveType,
        Int::class.javaPrimitiveType,
        Int::class.javaPrimitiveType,
        Int::class.javaPrimitiveType,
        Boolean::class.javaPrimitiveType,
        Boolean::class.javaPrimitiveType,
        Boolean::class.javaPrimitiveType,
        ByteArray::class.java,
        Array<CellSnapshot.Cell>::class.java,
    )
    constructor.isAccessible = true
    return constructor.newInstance(1, 1, 0, 0, false, false, false, ByteArray(1), arrayOf(cell))
}

class TerminalViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    // Every ViewModel built via [buildViewModel] is tracked here so its
    // `viewModelScope` (which backs the init-block's stall-check loop) is
    // genuinely cancelled before each test's `runTest` body returns. Without
    // this, that loop keeps re-scheduling itself forever on the SAME
    // `TestCoroutineScheduler` `Dispatchers.Main` is bound to below, which
    // `runTest`'s own idle-detection also drives -- an uncancelled recurring
    // `delay` loop on that shared scheduler means the scheduler never reports
    // idle, and `runTest` hangs indefinitely (a known kotlinx-coroutines-test
    // gotcha, not specific to this ViewModel). `ViewModelStore.clear()` is the
    // only way to reach `ViewModel.clear()` from outside the lifecycle
    // module -- it is Kotlin `internal` to `androidx.lifecycle`.
    private val viewModelStore = ViewModelStore()
    private var viewModelKeyCounter = 0

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        // DRAIN before releasing Main. Without this, a coroutine still
        // pending on a ViewModel from this test wakes up AFTER resetMain(),
        // touches a Dispatchers.Main that no longer exists, and the exception
        // surfaces as "uncaught exceptions before the test started" in the
        // NEXT test — which may even belong to another class. That was the
        // flake: it passed alone, failed in the suite, victim always changing.
        dispatcher.scheduler.advanceUntilIdle()
        Dispatchers.resetMain()
    }

    /** Runs [body], then unconditionally cancels every ViewModel built during it (see [viewModelStore]'s doc comment). */
    private fun runViewModelTest(body: suspend TestScope.() -> Unit): TestResult = runTest(dispatcher) {
        try {
            body()
        } finally {
            viewModelStore.clear()
        }
    }

    private fun buildViewModel(
        factory: FakeWebSocketFactory,
        ticketSource: FakeTicketSource = FakeTicketSource(mutableListOf("t1", "t2", "t3")),
        rawLogSource: FakeRawLogSource = FakeRawLogSource(mutableListOf()),
        engine: FakeGridEngine = FakeGridEngine(trivialSnapshot()),
        stallCheckIntervalMs: Long = 10L,
        stallThresholdMs: Long = 50L,
        bannerGraceMs: Long = TerminalViewModel.BANNER_GRACE_MS_DEFAULT,
    ): Pair<TerminalViewModel, FakeGridEngine> {
        val viewModel = TerminalViewModel(
            savedStateHandle = SavedStateHandle(mapOf(TERMINAL_SESSION_NAME_ARG to "main")),
            ticketSource = ticketSource,
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            rawLogSource = rawLogSource,
            engineFactory = { _, _, _ -> engine },
            stallCheckIntervalMs = stallCheckIntervalMs,
            stallThresholdMs = stallThresholdMs,
            clock = { dispatcher.scheduler.currentTime },
            bannerGraceMs = bannerGraceMs,
        )
        viewModelStore.put("terminal-${viewModelKeyCounter++}", viewModel)
        return viewModel to engine
    }

    // ---- Grace window: switching apps must not cost any noise ----

    // THE PRIMER PREFERS THE RENDERED HISTORY; THE RAW LOG IS THE FALLBACK.
    //
    // Replaying the raw log duplicates: the `ESC[nA` of a repainting program
    // saturates at the top of the SCREEN and never reaches the scrollback, so
    // the earlier copy stays put. The server's history has no such problem
    // because it is not a replay — those lines already left the screen of an
    // emulator alive on the session's grid. Swapping the order of these two
    // sources brings the duplication straight back.
    @Test
    fun `o primer usa o historico renderizado quando ele existe`() = runViewModelTest {
        val fonte = FakeRawLogSource(mutableListOf())
        fonte.doHistorico += RawLogResult.Success("HISTORICO\r\n".toByteArray(), 11)
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory, rawLogSource = fonte)
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals("pediu o historico", 1, fonte.pedidosDeHistorico.size)
        assertEquals("e nao precisou do log cru", 0, fonte.bytesPedidos.size)
        assertTrue(
            "o historico foi replayado na engine",
            engine.writes.any { String(it).contains("HISTORICO") },
        )
    }

    // An older session has no history file yet: empty is a legitimate answer,
    // and the primer has to fall back to the raw log instead of opening blank.
    @Test
    fun `sem historico renderizado o primer cai no log cru`() = runViewModelTest {
        val fonte = FakeRawLogSource(mutableListOf(RawLogResult.Success("CRU\r\n".toByteArray(), 5)))
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory, rawLogSource = fonte)
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals("tentou o historico primeiro", 1, fonte.pedidosDeHistorico.size)
        assertEquals("e caiu no log cru", 1, fonte.bytesPedidos.size)
        assertTrue(
            "a reserva foi replayada",
            engine.writes.any { String(it).contains("CRU") },
        )
    }

    @Test
    fun `reconexao rapida nao acende faixa nenhuma`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles — see onGridSizeChanged.
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()
        assertEquals(ConnectionState.Live, viewModel.bannerState.value)

        // Android cut the network when it sent the app to the background.
        factory.listeners[0].onFailure("rede cortada em segundo plano")
        runCurrent()
        assertEquals(
            "o estado REAL já é de reconexão",
            ConnectionState.Reconnecting(1),
            viewModel.connectionState.value,
        )
        assertEquals("a faixa, não", ConnectionState.Live, viewModel.bannerState.value)

        // Back in ~600 ms, inside the measured window of 0.5 s to 1 s.
        advanceTimeBy(600)
        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()
        advanceTimeBy(5_000)
        runCurrent()

        assertEquals(
            "reconectou antes da carência: a faixa nunca chegou a acender",
            ConnectionState.Live,
            viewModel.bannerState.value,
        )
    }

    @Test
    fun `reconexao que passa da carencia acende a faixa`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles — see onGridSizeChanged.
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        factory.listeners[0].onFailure("servidor fora do ar")
        runCurrent()
        // Nobody opens the next socket: the reconnect really does take a while.
        advanceTimeBy(TerminalViewModel.BANNER_GRACE_MS_DEFAULT + 200)
        runCurrent()

        assertEquals(
            "demorou de verdade: aí a informação é útil",
            ConnectionState.Reconnecting(1),
            viewModel.bannerState.value,
        )
    }

    // ---- Paste: the VT emulator decides the wrapping, not the server ----

    @Test
    fun `colar com colagem entre colchetes ligada manda os marcadores em UM quadro binario`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot()).apply {
            modes = TerminalModes(mouseTracking = false, bracketedPaste = true)
        }
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles — see onGridSizeChanged.
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        // Actually OPEN it: `send` only writes to an OPEN socket. It used to be
        // enough for one to EXIST, and that slack was what made typing vanish
        // during a reconnect — this test went along with it without meaning to,
        // sending bytes down a socket that was never opened.
        factory.listeners[0].onOpen()
        runCurrent()

        viewModel.sendPaste("linha 1\nlinha 2\nlinha 3")

        val socket = factory.sockets[0]
        assertEquals("uma colagem e UM quadro, nunca picada em pedacos", 1, socket.binaryFrames.size)
        assertEquals(
            "\u001b[200~linha 1\nlinha 2\nlinha 3\u001b[201~",
            String(socket.binaryFrames[0], Charsets.UTF_8),
        )
        assertTrue(
            "colagem nao pode mais sair como quadro de controle: o servidor embrulhava as cegas",
            socket.textFrames.none { it.contains("\"paste\"") },
        )
    }

    @Test
    fun `colar com colagem entre colchetes desligada nao manda marcador nenhum`() = runViewModelTest {
        // The other side of the same defect. The server ALWAYS wrapped, and
        // with DECSET 2004 off the markers themselves turned into literal text
        // on the command line — the same kind of garbage as the mouse defect.
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot()).apply { modes = TerminalModes.NENHUM }
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles — see onGridSizeChanged.
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        // Actually OPEN it: `send` only writes to an OPEN socket. It used to be
        // enough for one to EXIST, and that slack was what made typing vanish
        // during a reconnect — this test went along with it without meaning to,
        // sending bytes down a socket that was never opened.
        factory.listeners[0].onOpen()
        runCurrent()

        viewModel.sendPaste("linha 1\nlinha 2")

        val enviado = String(factory.sockets[0].binaryFrames[0], Charsets.UTF_8)
        assertFalse(enviado.contains("\u001b[200~"))
        assertFalse(enviado.contains("\u001b[201~"))
        assertEquals("linha 1\rlinha 2", enviado)
    }

    @Test
    fun `colar consulta o motor, que e quem conhece o modo do programa remoto`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot())
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles — see onGridSizeChanged.
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        viewModel.sendPaste("texto")

        assertEquals(listOf("texto"), engine.pastesCodificados)
    }

    // THE "CLEAR HISTORY" TESTS WENT AWAY TOGETHER WITH THE BUTTON.
    //
    // It existed as a remedy for damage of our own making — a log written
    // twice over, with `dtach`'s screen clear inside it. Once the causes
    // were fixed, the owner was explicit: "I will never want to erase my
    // history". Offering "erase everything" as the way out of a defect
    // charges the person the highest possible price for the author's mistake.

    @Test
    fun `colar sem motor ainda criado nao envia nada`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)

        viewModel.sendPaste("texto")
        runCurrent()

        assertTrue("sem terminal nao ha onde colar, e nao ha modo a consultar", factory.sockets.isEmpty())
    }

    // ---- Modes: the truth comes from the engine, re-read on every ask ----

    @Test
    fun `sem motor os modos sao os seguros - nada de mouse, nada de colchetes`() = runViewModelTest {
        val (viewModel, _) = buildViewModel(FakeWebSocketFactory())

        assertEquals(TerminalModes.NENHUM, viewModel.currentModes())
    }

    @Test
    fun `os modos acompanham o programa remoto abrindo e fechando`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot())
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles — see onGridSizeChanged.
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertFalse("prompt de shell: ninguem pediu mouse", viewModel.currentModes().mouseTracking)

        engine.modes = TerminalModes(mouseTracking = true, bracketedPaste = true)
        assertTrue("abriu o htop", viewModel.currentModes().mouseTracking)

        engine.modes = TerminalModes.NENHUM
        assertFalse("fechou o htop", viewModel.currentModes().mouseTracking)
    }

    @Test
    fun `encodeMouse sem motor devolve nada em vez de inventar bytes`() = runViewModelTest {
        val (viewModel, _) = buildViewModel(FakeWebSocketFactory())

        val bytes = viewModel.encodeMouse(
            action = MouseAction.PRESS,
            button = MouseButton.ESQUERDO,
            positionXPx = 10f,
            positionYPx = 10f,
            geometry = MouseGeometry(cellWidthPx = 10, cellHeightPx = 20, screenWidthPx = 800, screenHeightPx = 480),
            anyButtonPressed = true,
        )

        assertNull(bytes)
    }

    @Test
    fun `sessionName comes from the SavedStateHandle nav argument`() = runViewModelTest {
        val (viewModel, _) = buildViewModel(FakeWebSocketFactory())
        assertEquals("main", viewModel.sessionName)
    }

    @Test
    fun `first onGridSizeChanged creates the engine and connects exactly once, never a resize`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()

        assertEquals(1, factory.openedUrls.size)
        assertTrue(engine.resizeCalls.isEmpty())
    }

    @Test
    fun `repeating the same grid size is a no-op`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles — see onGridSizeChanged.
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals(1, factory.openedUrls.size)
        assertTrue(engine.resizeCalls.isEmpty())
    }

    @Test
    fun `mudar a janela AVISA o servidor e NAO mexe na engine`() = runViewModelTest {
        // The server decides the GRID size: a session can have several clients
        // and the PTY settles on the smallest of them. The engine only changes
        // when the announcement arrives.
        //
        // Resizing the engine from the window measurement was the defect: with
        // a smaller client attached, the program broke its lines at 66 columns
        // while the app drew on a grid of 72.
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        viewModel.onGridSizeChanged(100, 30)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals("nao pode reconectar por mudanca de janela", 1, factory.openedUrls.size)
        assertTrue(
            "a engine nao pode seguir a janela — quem manda e o anuncio do servidor",
            engine.resizeCalls.isEmpty(),
        )
        assertTrue(
            "mas o servidor TEM de saber o tamanho desta janela, senao nao ha minimo a calcular",
            factory.sockets[0].textFrames.any { it.contains("\"cols\":100") && it.contains("\"rows\":30") },
        )
    }

    @Test
    fun `o PRIMEIRO tamanho tambem e enviado ao servidor`() = runViewModelTest {
        // The engine's CREATION branch never sent the size; only the CHANGE
        // branch did. And `reafirmarTamanho` gives up if nothing was ever sent.
        // On a fresh attach the app never said what size it was: the PTY stayed
        // at the previous client's size and the screen came out wrong, nearly
        // always black.
        //
        // It explains the gestures that "fixed" it: changing the font size,
        // attaching an image, opening the sheet. All of them change the grid
        // height and land in the CHANGE branch — and only then did the size go.
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        assertTrue(
            "o servidor precisa saber o tamanho ja no primeiro attach",
            factory.sockets[0].textFrames.any { it.contains("\"cols\":80") && it.contains("\"rows\":24") },
        )
    }

    @Test
    fun `o anuncio do servidor e que redimensiona a engine`() = runViewModelTest {
        // The other half of the minimum-size rule: every client draws a grid the
        // size of the SESSION, not the size of its own window.
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(100, 30)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()

        // The server says the session is smaller, because another client is attached.
        factory.listeners[0].onTextMessage("""{"type":"size","cols":66,"rows":24}""")
        runCurrent()

        assertEquals(listOf(66 to 24), engine.resizeCalls)
    }

    @Test
    fun `quadro de texto desconhecido nao derruba nem redimensiona nada`() = runViewModelTest {
        // A frame that is not the announcement must never turn into garbage on
        // screen nor into an exception: it simply is not this announcement.
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()

        factory.listeners[0].onTextMessage("isto nao e json")
        factory.listeners[0].onTextMessage("""{"type":"outra coisa"}""")
        runCurrent()

        assertTrue(engine.resizeCalls.isEmpty())
    }

    @Test
    fun `inbound bytes are written to the engine and become visible via currentSnapshot`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        factory.listeners[0].onBinaryMessage(byteArrayOf(1, 2, 3))
        runCurrent()

        assertEquals(1, engine.writes.size)
        assertTrue(engine.writes[0].contentEquals(byteArrayOf(1, 2, 3)))
        assertEquals(engine.snapshot(), viewModel.currentSnapshot())
    }

    @Test
    fun `byteSink writes are forwarded to the socket as a single binary frame`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        // Actually OPEN it: `send` only writes to an OPEN socket. It used to be
        // enough for one to EXIST, and that slack was what made typing vanish
        // during a reconnect — this test went along with it without meaning to,
        // sending bytes down a socket that was never opened.
        factory.listeners[0].onOpen()
        runCurrent()
        viewModel.byteSink.send(byteArrayOf(9))

        assertEquals(1, factory.sockets[0].binaryFrames.size)
        assertTrue(factory.sockets[0].binaryFrames[0].contentEquals(byteArrayOf(9)))
    }

    @Test
    fun `a keystroke with no reply for longer than the threshold marks the connection stalled`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory, stallCheckIntervalMs = 10L, stallThresholdMs = 50L)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        assertEquals(ConnectionState.Live, viewModel.connectionState.value)

        viewModel.byteSink.send(byteArrayOf(1))
        dispatcher.scheduler.advanceTimeBy(60L)
        dispatcher.scheduler.runCurrent()

        assertTrue("keystroke unanswered past the threshold must surface as stalled", viewModel.isStalled.value)
    }

    @Test
    fun `bytes arriving after a stall clear it immediately`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory, stallCheckIntervalMs = 10L, stallThresholdMs = 50L)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        viewModel.byteSink.send(byteArrayOf(1))
        dispatcher.scheduler.advanceTimeBy(60L)
        dispatcher.scheduler.runCurrent()
        assertTrue(viewModel.isStalled.value)

        factory.listeners[0].onBinaryMessage(byteArrayOf(2))
        runCurrent()

        assertFalse("a reply must clear the stalled flag right away", viewModel.isStalled.value)
    }

    @Test
    fun `an idle Live connection nobody typed into is never reported as stalled`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory, stallCheckIntervalMs = 10L, stallThresholdMs = 50L)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        dispatcher.scheduler.advanceTimeBy(200L)
        dispatcher.scheduler.runCurrent()

        assertFalse("idle-but-healthy must not be conflated with stalled", viewModel.isStalled.value)
    }

    @Test
    fun `not yet Live is never reported as stalled even after a send`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory, stallCheckIntervalMs = 10L, stallThresholdMs = 50L)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles — see onGridSizeChanged.

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        viewModel.byteSink.send(byteArrayOf(1))
        dispatcher.scheduler.advanceTimeBy(200L)
        dispatcher.scheduler.runCurrent()

        assertFalse(viewModel.isStalled.value)
    }

    // ---- Session history: fetched, replayed, BEFORE the live stream ----

    @Test
    fun `o historico buscado entra na engine antes do fluxo vivo, nunca depois`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val historico = "conversa antiga\r\n".toByteArray()
        val (viewModel, engine) = buildViewModel(
            factory,
            rawLogSource = FakeRawLogSource(mutableListOf(RawLogResult.Success(historico, historico.size))),
        )

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.ESTABILIZACAO_DE_TAMANHO_MS + 1)
        runCurrent()
        // The live byte arrives BEFORE the history has been written — this is
        // the real race: the socket opens in the same turn the fetch goes out.
        factory.listeners.last().onOpen()
        factory.listeners.last().onBinaryMessage("ao vivo".toByteArray())
        runCurrent()

        val escritos = engine.writes.map { String(it) }
        assertEquals(listOf("conversa antiga\r\n", "ao vivo"), escritos)
    }

    @Test
    fun `o attach fresco dispensa o historico do servidor mas continua sendo fresco`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.ESTABILIZACAO_DE_TAMANHO_MS + 1)
        runCurrent()

        val url = factory.openedUrls.first()
        assertTrue(
            "sem replay=0 o historico apareceria duas vezes: o do servidor e o " +
                "que o app replaya (URL=$url)",
            url.contains("replay=0"),
        )
        assertFalse(
            "attach=1 na PRIMEIRA conexao desligaria o repaint-wobble, e a tela " +
                "ficaria parada no ultimo quadro gravado (URL=$url)",
            url.contains("attach=1"),
        )
    }

    @Test
    fun `o teto de bytes buscados sai da preferencia de linhas, nao de um chute`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val fonte = FakeRawLogSource(mutableListOf())
        val (viewModel, _) = buildViewModel(factory, rawLogSource = fonte)
        viewModel.scrollbackLinhas = TerminalScrollback.DEZ_MIL.linhas

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.ESTABILIZACAO_DE_TAMANHO_MS + 1)
        runCurrent()

        assertEquals(listOf(TerminalScrollback.DEZ_MIL.bytesDeLogParaBuscar), fonte.bytesPedidos)
    }

    @Test
    fun `historico que nao vem nao pode reter o fluxo vivo - ele e liberado do mesmo jeito`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(
            factory,
            rawLogSource = FakeRawLogSource(
                MutableList(TerminalViewModel.TENTATIVAS_DO_PRIMER) { RawLogResult.Error("falha de rede") },
            ),
        )

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.ESTABILIZACAO_DE_TAMANHO_MS + 1)
        runCurrent()

        // THE SOCKET DOES NOT EXIST YET, and that is the point: connecting
        // only happens after the fetch. See the KDoc of `iniciarPrimer` —
        // attaching in parallel made the server write into the log the very
        // bytes already arriving over the socket, and the engine got them twice.
        assertTrue(
            "conectar durante a busca reintroduz a sobreposicao que embaralhava a tela",
            factory.listeners.isEmpty(),
        )

        // The attempts, with the breather between them. After that, it
        // connects. The `+ 1` on the number of breathers is deliberate: there
        // are N attempts and N-1 waits, and a test that counts them exactly
        // breaks when someone tunes the retry count — which is legitimate
        // tuning, not a regression.
        advanceTimeBy(
            TerminalViewModel.ESPERA_ENTRE_TENTATIVAS_MS * (TerminalViewModel.TENTATIVAS_DO_PRIMER + 1),
        )
        runCurrent()

        assertEquals(1, factory.listeners.size)
        factory.listeners.last().onOpen()
        factory.listeners.last().onBinaryMessage("ao vivo".toByteArray())
        runCurrent()

        assertEquals(listOf("ao vivo"), engine.writes.map { String(it) })
    }

    @Test
    fun `o historico e buscado ANTES de conectar - e o encaixe que evita byte repetido`() = runViewModelTest {
        // The server only writes to the session log WHILE someone is attached.
        // Fetching with the log frozen makes the history end exactly where the
        // live stream begins. Reproduced off-device, in the app's own engine:
        // 2000 repeated bytes at the end of the log are enough to paint a rule
        // over text, because Claude Code's frame uses relative cursor movement
        // and column jumps that do not erase what they skip.
        val factory = FakeWebSocketFactory()
        val log = FakeRawLogSource(mutableListOf(RawLogResult.Success("historico".toByteArray(), 9)))
        val (viewModel, engine) = buildViewModel(factory, rawLogSource = log)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.ESTABILIZACAO_DE_TAMANHO_MS + 1)
        runCurrent()

        assertEquals("a busca do log tem de acontecer antes de qualquer attach", 1, log.bytesPedidos.size)
        assertEquals(1, factory.listeners.size)
        factory.listeners.last().onOpen()
        factory.listeners.last().onBinaryMessage("vivo".toByteArray())
        runCurrent()

        // History first, live stream after, every byte exactly once.
        assertEquals(listOf("historico", "vivo"), engine.writes.map { String(it) })
    }

    @Test
    fun `fluxo vivo demais durante a busca abandona o historico em vez de encher a memoria`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        // A fetch that never answers in time: meanwhile, the session dumps.
        val (viewModel, engine) = buildViewModel(
            factory,
            rawLogSource = FakeRawLogSource(mutableListOf(RawLogResult.Error("demorou"))),
        )

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.ESTABILIZACAO_DE_TAMANHO_MS + 1)
        runCurrent()
        // The fetch fails and the ceiling wins; only then does the socket open.
        // The guard stays reachable: the replay writes in chunks, yielding as
        // it goes, and it is in those yields that a live dump can arrive.
        advanceTimeBy(TerminalViewModel.TETO_DO_PRIMER_MS + 1)
        runCurrent()
        factory.listeners.last().onOpen()
        val despejo = ByteArray(TerminalViewModel.MAX_BYTES_PENDENTES + 1) { 'x'.code.toByte() }
        factory.listeners.last().onBinaryMessage(despejo)
        runCurrent()

        assertEquals(
            "o despejo tem de chegar a engine na hora, sem esperar a busca",
            1,
            engine.writes.size,
        )
        assertEquals(despejo.size, engine.writes.first().size)
    }

}
