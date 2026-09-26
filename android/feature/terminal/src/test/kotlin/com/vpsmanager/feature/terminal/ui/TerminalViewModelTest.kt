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
        if (tickets.isEmpty()) return WsTicketResult.Error("no more fake tickets")
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

/** Records each requested byte ceiling and returns results in order; empty means "no log yet", not an error. */
private class FakeRawLogSource(private val results: MutableList<RawLogResult>) : TerminalRawLogSource {
    val requestedBytes = mutableListOf<Int>()

    /** Results for the rendered history; empty means the session has no history file yet. */
    val fromHistory = mutableListOf<RawLogResult>()
    val historyRequests = mutableListOf<Int>()

    override suspend fun history(name: String, bytes: Int): RawLogResult {
        historyRequests += bytes
        return if (fromHistory.isEmpty()) RawLogResult.Success(ByteArray(0), 0) else fromHistory.removeAt(0)
    }

    override suspend fun rawLog(name: String, bytes: Int): RawLogResult {
        requestedBytes += bytes
        return if (results.isEmpty()) RawLogResult.Success(ByteArray(0), 0) else results.removeAt(0)
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

    /** How many times the history was cleared (setup for the attach repaint). */
    var historyClears = 0
        private set

    override fun clearHistory() {
        historyClears++
    }

    /** Modes the fake remote program has enabled; changed live, like `htop` opening and closing. */
    var modes = TerminalModes.NONE

    /** Bytes the native encoder would return; `null` means the event produces no report. */
    var mouseBytes: ByteArray? = null
    val mouseCalls = mutableListOf<MouseAction>()
    val encodedPastes = mutableListOf<String>()

    /** Every scroll request in lines; negative scrolls up into the past. */
    val scrolls = mutableListOf<Int>()
    var backToEndCount = 0

    /** A fake viewport, just enough for the test to observe position. */
    var scrollState = TerminalScrollState(total = 100, offset = 90, visible = 10, atEnd = true)

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
    override fun scrollViewport(lines: Int) {
        scrolls += lines
        val newOffset = (scrollState.offset + lines).coerceIn(0, scrollState.history)
        scrollState = scrollState.copy(
            offset = newOffset,
            atEnd = newOffset >= scrollState.history,
        )
    }

    override fun scrollToBottom() {
        backToEndCount++
        scrollState = scrollState.copy(offset = scrollState.history, atEnd = true)
    }

    override fun scrollState(): TerminalScrollState = scrollState

    override fun encodePaste(text: String): ByteArray {
        encodedPastes += text
        // Mirrors the real contract: wrapped with 2004 on, otherwise newlines become CRs.
        return if (modes.bracketedPaste) {
            "\u001b[200~$text\u001b[201~".toByteArray(Charsets.UTF_8)
        } else {
            text.replace('\n', '\r').toByteArray(Charsets.UTF_8)
        }
    }
}

/**
 * A valid 1x1 grid; only identity matters here. [CellSnapshot.fromBuffer] is `internal`
 * to `:terminal-engine`, so the private constructor is used via reflection instead of
 * depending on the native engine.
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

    // Tracks every ViewModel so its stall-check loop is cancelled before `runTest`
    // returns; otherwise the shared scheduler never goes idle and `runTest` hangs.
    // `ViewModelStore.clear()` is the only public way to reach `ViewModel.clear()`.
    private val viewModelStore = ViewModelStore()
    private var viewModelKeyCounter = 0

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        // Drain before resetting Main: a pending coroutine waking after resetMain()
        // would fail a later, unrelated test with "uncaught exceptions".
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

    // The primer prefers the server's rendered history: replaying the raw log of a
    // repainting program duplicates output, since cursor-up saturates at the screen top.
    @Test
    fun `the primer uses the rendered history when it exists`() = runViewModelTest {
        val source = FakeRawLogSource(mutableListOf())
        source.fromHistory += RawLogResult.Success("HISTORICO\r\n".toByteArray(), 11)
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory, rawLogSource = source)
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals("requested the history", 1, source.historyRequests.size)
        assertEquals("and did not need the raw log", 0, source.requestedBytes.size)
        assertTrue(
            "the history was replayed into the engine",
            engine.writes.any { String(it).contains("HISTORICO") },
        )
    }

    // Older sessions have no history file, so the primer falls back to the raw log.
    @Test
    fun `without rendered history the primer falls back to the raw log`() = runViewModelTest {
        val source = FakeRawLogSource(mutableListOf(RawLogResult.Success("CRU\r\n".toByteArray(), 5)))
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory, rawLogSource = source)
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals("tried the history first", 1, source.historyRequests.size)
        assertEquals("and fell back to the raw log", 1, source.requestedBytes.size)
        assertTrue(
            "the fallback was replayed",
            engine.writes.any { String(it).contains("CRU") },
        )
    }

    @Test
    fun `a quick reconnect shows no banner`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()
        assertEquals(ConnectionState.Live, viewModel.bannerState.value)

        // Android cuts the network when the app goes to the background.
        factory.listeners[0].onFailure("network cut in the background")
        runCurrent()
        assertEquals(
            "the real state is already reconnecting",
            ConnectionState.Reconnecting(1),
            viewModel.connectionState.value,
        )
        assertEquals("but the banner is not", ConnectionState.Live, viewModel.bannerState.value)

        // Back in about 600 ms, within the typical 0.5 to 1 s.
        advanceTimeBy(600)
        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()
        advanceTimeBy(5_000)
        runCurrent()

        assertEquals(
            "reconnected within the grace period, so the banner never showed",
            ConnectionState.Live,
            viewModel.bannerState.value,
        )
    }

    @Test
    fun `a reconnect that outlasts the grace period shows the banner`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        factory.listeners[0].onFailure("server down")
        runCurrent()
        // The next socket never opens, so the reconnect takes a while.
        advanceTimeBy(TerminalViewModel.BANNER_GRACE_MS_DEFAULT + 200)
        runCurrent()

        assertEquals(
            "it really took a while, so the banner is useful",
            ConnectionState.Reconnecting(1),
            viewModel.bannerState.value,
        )
    }

    // Paste: the VT emulator decides the bracketing, not the server.

    @Test
    fun `paste with bracketed paste on sends the markers in one binary frame`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot()).apply {
            modes = TerminalModes(mouseTracking = false, bracketedPaste = true)
        }
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        // `send` only writes to an open socket.
        factory.listeners[0].onOpen()
        runCurrent()

        viewModel.sendPaste("line 1\nline 2\nline 3")

        val socket = factory.sockets[0]
        assertEquals("a paste is one frame, never split into pieces", 1, socket.binaryFrames.size)
        assertEquals(
            "\u001b[200~line 1\nline 2\nline 3\u001b[201~",
            String(socket.binaryFrames[0], Charsets.UTF_8),
        )
        assertTrue(
            "paste must not go out as a control frame, the server would bracket it blindly",
            socket.textFrames.none { it.contains("\"paste\"") },
        )
    }

    @Test
    fun `paste with bracketed paste off sends no markers`() = runViewModelTest {
        // With DECSET 2004 off, bracket markers would appear as literal text.
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot()).apply { modes = TerminalModes.NONE }
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        // `send` only writes to an open socket.
        factory.listeners[0].onOpen()
        runCurrent()

        viewModel.sendPaste("line 1\nline 2")

        val sent = String(factory.sockets[0].binaryFrames[0], Charsets.UTF_8)
        assertFalse(sent.contains("\u001b[200~"))
        assertFalse(sent.contains("\u001b[201~"))
        assertEquals("line 1\rline 2", sent)
    }

    @Test
    fun `paste asks the engine, which knows the remote program's mode`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot())
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        viewModel.sendPaste("text")

        assertEquals(listOf("text"), engine.encodedPastes)
    }

    @Test
    fun `paste before the engine exists sends nothing`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)

        viewModel.sendPaste("text")
        runCurrent()

        assertTrue("without a terminal there is nowhere to paste and no mode to check", factory.sockets.isEmpty())
    }

    // Modes come from the engine and are re-read on every query.

    @Test
    fun `without an engine the modes are the safe defaults`() = runViewModelTest {
        val (viewModel, _) = buildViewModel(FakeWebSocketFactory())

        assertEquals(TerminalModes.NONE, viewModel.currentModes())
    }

    @Test
    fun `modes follow the remote program opening and closing`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot())
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertFalse("shell prompt, nobody asked for the mouse", viewModel.currentModes().mouseTracking)

        engine.modes = TerminalModes(mouseTracking = true, bracketedPaste = true)
        assertTrue("htop opened", viewModel.currentModes().mouseTracking)

        engine.modes = TerminalModes.NONE
        assertFalse("htop closed", viewModel.currentModes().mouseTracking)
    }

    @Test
    fun `encodeMouse without an engine returns nothing instead of inventing bytes`() = runViewModelTest {
        val (viewModel, _) = buildViewModel(FakeWebSocketFactory())

        val bytes = viewModel.encodeMouse(
            action = MouseAction.PRESS,
            button = MouseButton.LEFT,
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

        // The grid is only applied once it settles (see onGridSizeChanged).
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

        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        viewModel.onGridSizeChanged(80, 24)
        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals(1, factory.openedUrls.size)
        assertTrue(engine.resizeCalls.isEmpty())
    }

    @Test
    fun `resizing the window notifies the server and leaves the engine alone`() = runViewModelTest {
        // The server decides the grid size (the smallest of all clients); the engine
        // only resizes when the server announces it.
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

        assertEquals("a window change must not reconnect", 1, factory.openedUrls.size)
        assertTrue(
            "the engine must not follow the window, only the server announcement",
            engine.resizeCalls.isEmpty(),
        )
        assertTrue(
            "but the server must know this window size to compute the minimum",
            factory.sockets[0].textFrames.any { it.contains("\"cols\":100") && it.contains("\"rows\":30") },
        )
    }

    @Test
    fun `the first size is also sent to the server`() = runViewModelTest {
        // The size must be sent when the engine is created, not only on change;
        // `reassertSize` does nothing if no size was ever sent.
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        assertTrue(
            "the server must know the size on the first attach",
            factory.sockets[0].textFrames.any { it.contains("\"cols\":80") && it.contains("\"rows\":24") },
        )
    }

    @Test
    fun `the server announcement is what resizes the engine`() = runViewModelTest {
        // Every client draws a grid the size of the session, not of its own window.
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(100, 30)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()

        // Another client is attached, so the server announces a smaller session.
        factory.listeners[0].onTextMessage("""{"type":"size","cols":66,"rows":24}""")
        runCurrent()

        assertEquals(listOf(66 to 24), engine.resizeCalls)
    }

    @Test
    fun `an unknown text frame neither crashes nor resizes anything`() = runViewModelTest {
        // Frames other than the size announcement are ignored without error.
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()

        factory.listeners[0].onTextMessage("this is not json")
        factory.listeners[0].onTextMessage("""{"type":"something else"}""")
        runCurrent()

        assertTrue(engine.resizeCalls.isEmpty())
    }

    @Test
    fun `inbound bytes are written to the engine and become visible via currentSnapshot`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)

        // The grid is only applied once it settles (see onGridSizeChanged).
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

        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        // `send` only writes to an open socket.
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

        // The grid is only applied once it settles (see onGridSizeChanged).
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

        // The grid is only applied once it settles (see onGridSizeChanged).
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

        // The grid is only applied once it settles (see onGridSizeChanged).
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

        // The grid is only applied once it settles (see onGridSizeChanged).
        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        viewModel.byteSink.send(byteArrayOf(1))
        dispatcher.scheduler.advanceTimeBy(200L)
        dispatcher.scheduler.runCurrent()

        assertFalse(viewModel.isStalled.value)
    }

    // Session history is fetched and replayed before the live stream.

    @Test
    fun `the fetched history enters the engine before the live stream, never after`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val history = "old conversation\r\n".toByteArray()
        val (viewModel, engine) = buildViewModel(
            factory,
            rawLogSource = FakeRawLogSource(mutableListOf(RawLogResult.Success(history, history.size))),
        )

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.SIZE_SETTLE_MS + 1)
        runCurrent()
        // A live byte arrives before the history is written, as in the real race.
        factory.listeners.last().onOpen()
        factory.listeners.last().onBinaryMessage("live".toByteArray())
        runCurrent()

        val written = engine.writes.map { String(it) }
        assertEquals(listOf("old conversation\r\n", "live"), written)
    }

    @Test
    fun `a fresh attach skips the server history but stays a fresh attach`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.SIZE_SETTLE_MS + 1)
        runCurrent()

        val url = factory.openedUrls.first()
        assertTrue(
            "without replay=0 the history would appear twice, from the server and " +
                "from the app replay (URL=$url)",
            url.contains("replay=0"),
        )
        assertFalse(
            "attach=1 on the first connection would disable the repaint wobble, and the screen " +
                "would stay on the last recorded frame (URL=$url)",
            url.contains("attach=1"),
        )
    }

    @Test
    fun `the fetched byte ceiling comes from the line preference, not a guess`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val source = FakeRawLogSource(mutableListOf())
        val (viewModel, _) = buildViewModel(factory, rawLogSource = source)
        viewModel.scrollbackLines = TerminalScrollback.TEN_THOUSAND.lines

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.SIZE_SETTLE_MS + 1)
        runCurrent()

        assertEquals(listOf(TerminalScrollback.TEN_THOUSAND.logBytesToFetch), source.requestedBytes)
    }

    @Test
    fun `missing history must not hold back the live stream`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(
            factory,
            rawLogSource = FakeRawLogSource(
                MutableList(TerminalViewModel.PRIMER_ATTEMPTS) { RawLogResult.Error("network failure") },
            ),
        )

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.SIZE_SETTLE_MS + 1)
        runCurrent()

        // No socket yet: connecting only happens after the fetch (see `startPrimer`),
        // or the server would log bytes the socket also delivers.
        assertTrue(
            "connecting during the fetch would cause the overlap that garbled the screen",
            factory.listeners.isEmpty(),
        )

        // After all attempts and waits it connects. The `+ 1` leaves slack so tuning the
        // retry count does not break the test.
        advanceTimeBy(
            TerminalViewModel.RETRY_DELAY_MS * (TerminalViewModel.PRIMER_ATTEMPTS + 1),
        )
        runCurrent()

        assertEquals(1, factory.listeners.size)
        factory.listeners.last().onOpen()
        factory.listeners.last().onBinaryMessage("live".toByteArray())
        runCurrent()

        assertEquals(listOf("live"), engine.writes.map { String(it) })
    }

    @Test
    fun `the history is fetched before connecting so no byte is repeated`() = runViewModelTest {
        // The server only logs while someone is attached, so fetching first makes the
        // history end exactly where the live stream begins. Repeated bytes garble
        // programs that use relative cursor movement.
        val factory = FakeWebSocketFactory()
        val log = FakeRawLogSource(mutableListOf(RawLogResult.Success("history".toByteArray(), 9)))
        val (viewModel, engine) = buildViewModel(factory, rawLogSource = log)

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.SIZE_SETTLE_MS + 1)
        runCurrent()

        assertEquals("the log fetch must happen before any attach", 1, log.requestedBytes.size)
        assertEquals(1, factory.listeners.size)
        factory.listeners.last().onOpen()
        factory.listeners.last().onBinaryMessage("live".toByteArray())
        runCurrent()

        // History first, then the live stream, every byte exactly once.
        assertEquals(listOf("history", "live"), engine.writes.map { String(it) })
    }

    @Test
    fun `too much live output during the fetch drops the history instead of filling memory`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        // The fetch never answers in time while the session floods output.
        val (viewModel, engine) = buildViewModel(
            factory,
            rawLogSource = FakeRawLogSource(mutableListOf(RawLogResult.Error("timed out"))),
        )

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.SIZE_SETTLE_MS + 1)
        runCurrent()
        // After the timeout the socket opens; the replay yields between chunks, so a
        // live flood can still arrive during it.
        advanceTimeBy(TerminalViewModel.PRIMER_TIMEOUT_MS + 1)
        runCurrent()
        factory.listeners.last().onOpen()
        val flood = ByteArray(TerminalViewModel.MAX_PENDING_BYTES + 1) { 'x'.code.toByte() }
        factory.listeners.last().onBinaryMessage(flood)
        runCurrent()

        assertEquals(
            "the flood must reach the engine immediately, without waiting for the fetch",
            1,
            engine.writes.size,
        )
        assertEquals(flood.size, engine.writes.first().size)
    }

}
