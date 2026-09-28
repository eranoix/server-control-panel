package dev.servercontrolpanel.feature.terminal.transport

import dev.servercontrolpanel.data.terminal.TerminalTicketSource
import dev.servercontrolpanel.data.terminal.TerminalWebSocket
import dev.servercontrolpanel.data.terminal.TerminalWebSocketFactory
import dev.servercontrolpanel.data.terminal.TerminalWebSocketListener
import dev.servercontrolpanel.data.terminal.WsTicketResult
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private class FakeTicketSource(private val tickets: MutableList<String>) : TerminalTicketSource {
    val requestedNames = mutableListOf<String>()
    var callCount = 0
        private set

    override suspend fun wsTicket(name: String): WsTicketResult {
        callCount++
        requestedNames += name
        if (tickets.isEmpty()) return WsTicketResult.Error("no more fake tickets")
        return WsTicketResult.Success(ticket = tickets.removeAt(0), expiresIn = 60)
    }
}

private class RecordingWebSocket : TerminalWebSocket {
    val binaryFrames = mutableListOf<ByteArray>()
    val textFrames = mutableListOf<String>()
    var closed: Pair<Int, String>? = null

    override fun sendBytes(bytes: ByteArray): Boolean {
        binaryFrames += bytes
        return true
    }

    override fun sendText(text: String): Boolean {
        textFrames += text
        return true
    }

    override fun close(code: Int, reason: String): Boolean {
        closed = code to reason
        return true
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

class TerminalSocketClientTest {

    private fun recordingDelayer(sink: MutableList<Long>): suspend (Long) -> Unit = { sink += it }

    @Test
    fun `send writes exactly one binary frame unmodified`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        val payload = byteArrayOf(1, 2, 3, 4)
        client.send(payload)

        assertEquals(1, factory.sockets[0].binaryFrames.size)
        assertTrue(factory.sockets[0].binaryFrames[0].contentEquals(payload))
    }

    @Test
    fun `sendResize sends exactly one resize control frame with no data key`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        client.connect()
        runCurrent()

        client.sendResize(120, 40)

        val frames = factory.sockets[0].textFrames
        assertEquals(1, frames.size)
        assertTrue(frames[0].contains("\"type\":\"resize\""))
        assertTrue(frames[0].contains("\"cols\":120"))
        assertTrue(frames[0].contains("\"rows\":40"))
        assertFalse("resize must not carry data", frames[0].contains("\"data\""))
    }

    @Test
    fun `inbound binary frames are forwarded to onBytes in arrival order`() = runTest {
        val factory = FakeWebSocketFactory()
        val received = mutableListOf<ByteArray>()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = { received += it },
            delayer = recordingDelayer(mutableListOf()),
        )
        client.connect()
        runCurrent()

        factory.listeners[0].onBinaryMessage(byteArrayOf(1))
        factory.listeners[0].onBinaryMessage(byteArrayOf(2))

        assertEquals(2, received.size)
        assertTrue(received[0].contentEquals(byteArrayOf(1)))
        assertTrue(received[1].contentEquals(byteArrayOf(2)))
    }

    @Test
    fun `state starts Connecting and moves to Live on first successful open, without attach`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        assertEquals(ConnectionState.Connecting, client.state.value)

        client.connect()
        runCurrent()
        assertFalse("the first connection never carries attach=1", factory.openedUrls[0].contains("attach=1"))
        assertTrue("every connection requests frame", factory.openedUrls[0].contains("frame=1"))

        factory.listeners[0].onOpen()
        assertEquals(ConnectionState.Live, client.state.value)
    }

    @Test
    fun `unexpected drop while Live reconnects with attach=1 and a fresh ticket, then recovers to Live`() = runTest {
        val factory = FakeWebSocketFactory()
        val ticketSource = FakeTicketSource(mutableListOf("first-ticket", "second-ticket"))
        val delays = mutableListOf<Long>()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = ticketSource,
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(delays),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        assertEquals(ConnectionState.Live, client.state.value)

        factory.listeners[0].onClosed(1006, "connection lost")
        runCurrent()

        assertEquals(2, factory.openedUrls.size)
        assertFalse("the first attempt does not carry attach=1", factory.openedUrls[0].contains("attach=1"))
        assertTrue("every reconnect carries attach=1", factory.openedUrls[1].contains("attach=1"))
        assertTrue(factory.openedUrls[0].contains("ticket=first-ticket"))
        assertTrue(factory.openedUrls[1].contains("ticket=second-ticket"))
        assertFalse("a reconnect never reuses the previous ticket", factory.openedUrls[1].contains("ticket=first-ticket"))
        assertEquals(ConnectionState.Reconnecting(1), client.state.value)
        assertEquals(listOf(500L), delays)

        factory.listeners[1].onOpen()
        assertEquals(ConnectionState.Live, client.state.value)
    }

    @Test
    fun `backoff delay is non-decreasing and caps at 15s across repeated failures`() = runTest {
        val tickets = MutableList(8) { "ticket-$it" }
        val factory = FakeWebSocketFactory()
        val delays = mutableListOf<Long>()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(tickets),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(delays),
        )

        client.connect()
        runCurrent()
        repeat(6) { index ->
            factory.listeners[index].onFailure("simulated drop")
            runCurrent()
        }

        assertEquals(listOf(500L, 1000L, 2000L, 4000L, 8000L, 15000L), delays)
        for (i in 1 until delays.size) {
            assertTrue("the delay must never decrease", delays[i] >= delays[i - 1])
        }
        assertTrue("the delay must never exceed the 15s cap", delays.all { it <= 15_000L })
    }

    @Test
    fun `a drop after connecting restarts backoff at 500ms instead of inheriting the ladder`() = runTest {
        val factory = FakeWebSocketFactory()
        val delays = mutableListOf<Long>()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(10) { "ticket-$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(delays),
        )

        client.connect()
        runCurrent()

        repeat(3) { index ->
            factory.listeners[index].onOpen()
            assertEquals(ConnectionState.Live, client.state.value)
            factory.listeners[index].onClosed(1006, "app went to the background")
            runCurrent()
        }

        assertEquals("every drop after Live restarts the ladder", listOf(500L, 500L, 500L), delays)
        assertEquals(ConnectionState.Reconnecting(1), client.state.value)
    }

    @Test
    fun `reconnectNow tries immediately without waiting for the backoff`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = { delay(it) },
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onFailure("network cut when going to the background")
        runCurrent()

        assertEquals("the loop is sleeping through the backoff", 1, factory.openedUrls.size)

        client.reconnectNow()
        runCurrent()

        assertEquals("back in the app, it tries immediately", 2, factory.openedUrls.size)
        assertTrue(
            "the reattach keeps the screen (attach=1 prevents a duplicate scrollback replay)",
            factory.openedUrls[1].contains("attach=1"),
        )

        factory.listeners[1].onOpen()
        client.reconnectNow()
        runCurrent()
        assertEquals("a live connection is not dropped", 2, factory.openedUrls.size)
    }

    @Test
    fun `input typed while the socket is down is queued and sent in order on reconnect`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onFailure("network cut when going to the background")

        client.send(byteArrayOf('l'.code.toByte()))
        client.send(byteArrayOf('s'.code.toByte()))
        assertFalse("nothing was discarded", client.typingDiscarded.value)

        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()

        val frames = factory.sockets[1].binaryFrames
        assertEquals("both bytes were sent, no more", 2, frames.size)
        assertTrue(frames[0].contentEquals(byteArrayOf('l'.code.toByte())))
        assertTrue(frames[1].contentEquals(byteArrayOf('s'.code.toByte())))
    }

    @Test
    fun `an overflowing queue is discarded with a warning instead of vanishing silently`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onFailure("drop")

        client.send(ByteArray(MAX_PENDING_SEND_BYTES))
        assertFalse(client.typingDiscarded.value)
        client.send(byteArrayOf(1))
        assertTrue("the cap was exceeded, so the user must be warned", client.typingDiscarded.value)

        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()

        assertEquals("nothing queued is sent after being discarded", 0, factory.sockets[1].binaryFrames.size)
        assertFalse("a new connection clears the warning", client.typingDiscarded.value)
    }

    @Test
    fun `a 4404 close settles on SessionEnded and stops retrying, whether on first connect or a reconnect`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onClosed(4404, "session ended")
        runCurrent()

        assertEquals(ConnectionState.SessionEnded, client.state.value)
        assertEquals("no new attempt after 4404", 1, factory.openedUrls.size)

        runCurrent()
        assertEquals(1, factory.openedUrls.size)
    }

    @Test
    fun `4404 on a reconnect attempt (not just the first) also settles on SessionEnded`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1", "t2")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onClosed(1006, "drop")
        runCurrent()
        assertEquals(2, factory.openedUrls.size)

        factory.listeners[1].onClosed(4404, "session ended")
        runCurrent()

        assertEquals(ConnectionState.SessionEnded, client.state.value)
        assertEquals(2, factory.openedUrls.size)
    }

    @Test
    fun `explicit disconnect settles on Disconnected and never auto-reconnects`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()

        client.disconnect()
        runCurrent()

        assertEquals(ConnectionState.Disconnected, client.state.value)
        assertEquals(1000, factory.sockets[0].closed?.first)
        assertEquals(1, factory.openedUrls.size)
    }

    @Test
    fun `a reconnect reasserts the grid size without being asked again`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1", "t2")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()
        client.sendResize(67, 53)
        runCurrent()

        factory.listeners[0].onClosed(1006, "network cut in the background")
        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()

        val frames = factory.sockets[1].textFrames
        assertTrue(
            "the new socket must receive the size, otherwise the server keeps " +
                "the geometry the wobble left (frames=$frames)",
            frames.any {
                it.contains("\"type\":\"resize\"") &&
                    it.contains("\"cols\":67") &&
                    it.contains("\"rows\":53")
            },
        )
    }

    @Test
    fun `without a known size the connection does not invent a resize`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        assertTrue(
            "nothing may be sent before the first grid measurement",
            factory.sockets[0].textFrames.none { it.contains("\"type\":\"resize\"") },
        )
    }
    @Test
    fun `typing during a reconnect is not lost because an attempt in flight is not a connection`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://panel.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onFailure("network cut")

        runCurrent()
        assertEquals("the second attempt must be in flight", 2, factory.sockets.size)

        client.send(byteArrayOf('l'.code.toByte()))
        client.send(byteArrayOf('s'.code.toByte()))

        assertTrue(
            "nothing may go to a socket that has not opened yet",
            factory.sockets[1].binaryFrames.isEmpty(),
        )
        assertEquals(
            "and what was typed must stay visible while waiting",
            "ls",
            client.pendingTyping.value,
        )

        factory.listeners[1].onOpen()
        runCurrent()

        val frames = factory.sockets[1].binaryFrames
        assertEquals("both bytes were sent, no more", 2, frames.size)
        assertTrue(frames[0].contentEquals(byteArrayOf('l'.code.toByte())))
        assertTrue(frames[1].contentEquals(byteArrayOf('s'.code.toByte())))
        assertEquals("and the strip clears once they were actually sent", "", client.pendingTyping.value)
    }

}
