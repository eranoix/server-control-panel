package dev.servercontrolpanel.feature.terminal.ui

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModelStore
import dev.servercontrolpanel.data.terminal.RawLogResult
import dev.servercontrolpanel.data.terminal.TerminalRawLogSource
import dev.servercontrolpanel.data.terminal.TerminalTicketSource
import dev.servercontrolpanel.data.terminal.TerminalWebSocket
import dev.servercontrolpanel.data.terminal.TerminalWebSocketFactory
import dev.servercontrolpanel.data.terminal.TerminalWebSocketListener
import dev.servercontrolpanel.data.terminal.WsTicketResult
import dev.servercontrolpanel.feature.terminal.prefs.TerminalScrollback
import dev.servercontrolpanel.feature.terminal.transport.ConnectionState
import dev.servercontrolpanel.terminalengine.CellSnapshot
import dev.servercontrolpanel.terminalengine.MouseAction
import dev.servercontrolpanel.terminalengine.MouseButton
import dev.servercontrolpanel.terminalengine.MouseGeometry
import dev.servercontrolpanel.terminalengine.TerminalModes
import dev.servercontrolpanel.terminalengine.TerminalScrollState
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

private class FakeTicketSource(private val tickets: MutableList<String>) : TerminalTicketSource {
    override suspend fun wsTicket(name: String): WsTicketResult {
        if (tickets.isEmpty()) return WsTicketResult.Error("no more fake tickets")
        return WsTicketResult.Success(ticket = tickets.removeAt(0), expiresIn = 60)
    }
}

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

private class FakeRawLogSource(private val results: MutableList<RawLogResult>) : TerminalRawLogSource {
    val requestedBytes = mutableListOf<Int>()

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

private class FakeGridEngine(private val snapshotToReturn: CellSnapshot) : GridEngine {
    val writes = mutableListOf<ByteArray>()
    val resizeCalls = mutableListOf<Pair<Int, Int>>()
    var closed = false

    var historyClears = 0
        private set

    override fun clearHistory() {
        historyClears++
    }

    var modes = TerminalModes.NONE

    var mouseBytes: ByteArray? = null
    val mouseCalls = mutableListOf<MouseAction>()
    val encodedPastes = mutableListOf<String>()

    val scrolls = mutableListOf<Int>()
    var backToEndCount = 0

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
        return if (modes.bracketedPaste) {
            "\u001b[200~$text\u001b[201~".toByteArray(Charsets.UTF_8)
        } else {
            text.replace('\n', '\r').toByteArray(Charsets.UTF_8)
        }
    }
}

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

    private val viewModelStore = ViewModelStore()
    private var viewModelKeyCounter = 0

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        dispatcher.scheduler.advanceUntilIdle()
        Dispatchers.resetMain()
    }

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
            wsBaseUrl = "wss://panel.example",
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

    @Test
    fun `the primer uses the rendered history when it exists`() = runViewModelTest {
        val source = FakeRawLogSource(mutableListOf())
        source.fromHistory += RawLogResult.Success("HISTORY\r\n".toByteArray(), 11)
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
            engine.writes.any { String(it).contains("HISTORY") },
        )
    }

    @Test
    fun `without rendered history the primer falls back to the raw log`() = runViewModelTest {
        val source = FakeRawLogSource(mutableListOf(RawLogResult.Success("RAW\r\n".toByteArray(), 5)))
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
            engine.writes.any { String(it).contains("RAW") },
        )
    }

    @Test
    fun `a quick reconnect shows no banner`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, _) = buildViewModel(factory)
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()
        assertEquals(ConnectionState.Live, viewModel.bannerState.value)

        factory.listeners[0].onFailure("network cut in the background")
        runCurrent()
        assertEquals(
            "the real state is already reconnecting",
            ConnectionState.Reconnecting(1),
            viewModel.connectionState.value,
        )
        assertEquals("but the banner is not", ConnectionState.Live, viewModel.bannerState.value)

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
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        factory.listeners[0].onFailure("server down")
        runCurrent()
        advanceTimeBy(TerminalViewModel.BANNER_GRACE_MS_DEFAULT + 200)
        runCurrent()

        assertEquals(
            "it really took a while, so the banner is useful",
            ConnectionState.Reconnecting(1),
            viewModel.bannerState.value,
        )
    }

    @Test
    fun `paste with bracketed paste on sends the markers in one binary frame`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot()).apply {
            modes = TerminalModes(mouseTracking = false, bracketedPaste = true)
        }
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
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
        val factory = FakeWebSocketFactory()
        val engine = FakeGridEngine(trivialSnapshot()).apply { modes = TerminalModes.NONE }
        val (viewModel, _) = buildViewModel(factory, engine = engine)
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
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

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()

        assertEquals(1, factory.openedUrls.size)
        assertTrue(engine.resizeCalls.isEmpty())
    }

    @Test
    fun `resizing the window notifies the server and leaves the engine alone`() = runViewModelTest {
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
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(factory)

        viewModel.onGridSizeChanged(100, 30)
        advanceTimeBy(300)
        runCurrent()
        runCurrent()
        factory.listeners[0].onOpen()

        factory.listeners[0].onTextMessage("""{"type":"size","cols":66,"rows":24}""")
        runCurrent()

        assertEquals(listOf(66 to 24), engine.resizeCalls)
    }

    @Test
    fun `an unknown text frame neither crashes nor resizes anything`() = runViewModelTest {
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

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
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

        advanceTimeBy(300)

        runCurrent()
        runCurrent()
        viewModel.byteSink.send(byteArrayOf(1))
        dispatcher.scheduler.advanceTimeBy(200L)
        dispatcher.scheduler.runCurrent()

        assertFalse(viewModel.isStalled.value)
    }

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

        assertTrue(
            "connecting during the fetch would cause the overlap that garbled the screen",
            factory.listeners.isEmpty(),
        )

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

        assertEquals(listOf("history", "live"), engine.writes.map { String(it) })
    }

    @Test
    fun `too much live output during the fetch drops the history instead of filling memory`() = runViewModelTest {
        val factory = FakeWebSocketFactory()
        val (viewModel, engine) = buildViewModel(
            factory,
            rawLogSource = FakeRawLogSource(mutableListOf(RawLogResult.Error("timed out"))),
        )

        viewModel.onGridSizeChanged(80, 24)
        advanceTimeBy(TerminalViewModel.SIZE_SETTLE_MS + 1)
        runCurrent()
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
