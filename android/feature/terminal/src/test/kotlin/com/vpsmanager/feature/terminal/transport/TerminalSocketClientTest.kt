package com.vpsmanager.feature.terminal.transport

import com.vpsmanager.data.terminal.TerminalTicketSource
import com.vpsmanager.data.terminal.TerminalWebSocket
import com.vpsmanager.data.terminal.TerminalWebSocketFactory
import com.vpsmanager.data.terminal.TerminalWebSocketListener
import com.vpsmanager.data.terminal.WsTicketResult
import kotlinx.coroutines.delay
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Hands out a queued sequence of tickets — proves a reconnect never reuses a consumed one. */
private class FakeTicketSource(private val tickets: MutableList<String>) : TerminalTicketSource {
    val requestedNames = mutableListOf<String>()
    var callCount = 0
        private set

    override suspend fun wsTicket(name: String): WsTicketResult {
        callCount++
        requestedNames += name
        if (tickets.isEmpty()) return WsTicketResult.Error("sem mais tickets fake")
        return WsTicketResult.Success(ticket = tickets.removeAt(0), expiresIn = 60)
    }
}

/** No real socket — just records every frame it was asked to send. */
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

/** No network — records the URL of every open() call and hands back a [RecordingWebSocket]. */
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
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        client.connect()
        runCurrent()
        // Actually OPEN it. This test used to send bytes with the socket
        // merely CREATED, and passed — because `send` accepted a non-null
        // socket even when it was closed. That was exactly the defect that
        // made typing vanish during a reconnect; the test was unwittingly
        // going along with it.
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
            wsBaseUrl = "wss://vpsm.example",
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
        assertFalse("resize não deve carregar data", frames[0].contains("\"data\""))
    }

    @Test
    fun `inbound binary frames are forwarded to onBytes in arrival order`() = runTest {
        val factory = FakeWebSocketFactory()
        val received = mutableListOf<ByteArray>()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
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
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        assertEquals(ConnectionState.Connecting, client.state.value)

        client.connect()
        runCurrent()
        assertFalse("primeira conexão nunca leva attach=1", factory.openedUrls[0].contains("attach=1"))
        // Without `quadro=1` the device goes back to being the CEILING for
        // the session's size — that is, the phone shrinks the desktop again.
        assertTrue("toda conexão pede quadro", factory.openedUrls[0].contains("quadro=1"))

        factory.listeners[0].onOpen()
        assertEquals(ConnectionState.Live, client.state.value)
    }

    @Test
    fun `unexpected drop while Live reconnects with attach=1 and a fresh ticket, then recovers to Live`() = runTest {
        val factory = FakeWebSocketFactory()
        val ticketSource = FakeTicketSource(mutableListOf("primeiro-ticket", "segundo-ticket"))
        val delays = mutableListOf<Long>()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = ticketSource,
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(delays),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        assertEquals(ConnectionState.Live, client.state.value)

        // Unexpected drop, not a 4404 — must retry, never settle on SessionEnded.
        factory.listeners[0].onClosed(1006, "conexão perdida")
        runCurrent()

        assertEquals(2, factory.openedUrls.size)
        assertFalse("primeira tentativa não leva attach=1", factory.openedUrls[0].contains("attach=1"))
        assertTrue("todo reconnect leva attach=1", factory.openedUrls[1].contains("attach=1"))
        assertTrue(factory.openedUrls[0].contains("ticket=primeiro-ticket"))
        assertTrue(factory.openedUrls[1].contains("ticket=segundo-ticket"))
        assertFalse("reconnect nunca reusa o ticket anterior", factory.openedUrls[1].contains("ticket=primeiro-ticket"))
        assertEquals(ConnectionState.Reconnecting(1), client.state.value)
        assertEquals(listOf(500L), delays)

        // The fake factory's second attempt finally succeeds.
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
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(delays),
        )

        client.connect()
        runCurrent()
        // Fail the next 6 attempts in a row without ever reaching Live.
        repeat(6) { index ->
            factory.listeners[index].onFailure("queda simulada")
            runCurrent()
        }

        assertEquals(listOf(500L, 1000L, 2000L, 4000L, 8000L, 15000L), delays)
        for (i in 1 until delays.size) {
            assertTrue("atraso nunca deve diminuir", delays[i] >= delays[i - 1])
        }
        assertTrue("atraso nunca deve passar do teto de 15s", delays.all { it <= 15_000L })
    }

    @Test
    fun `uma queda DEPOIS de ter conectado recomeca o backoff em 500ms em vez de herdar a escada`() = runTest {
        val factory = FakeWebSocketFactory()
        val delays = mutableListOf<Long>()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(10) { "ticket-$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(delays),
        )

        client.connect()
        runCurrent()

        // Three "connected and dropped" cycles — exactly what going into the
        // background provokes on Android 15+, which cuts the app's network a
        // few seconds after it leaves the foreground. Before this fix
        // `attempt` only ever grew between cycles, and the third return to the
        // app sat for 2s (the sixth, 15s) on "Reconnecting (n)…" with the
        // server up on the other side.
        repeat(3) { index ->
            factory.listeners[index].onOpen()
            assertEquals(ConnectionState.Live, client.state.value)
            factory.listeners[index].onClosed(1006, "app foi para o segundo plano")
            runCurrent()
        }

        assertEquals("toda queda após Live recomeça a escada", listOf(500L, 500L, 500L), delays)
        assertEquals(ConnectionState.Reconnecting(1), client.state.value)
    }

    @Test
    fun `reconectarAgora tenta na hora, sem esperar o backoff correr`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            // A REAL delayer (runTest's virtual time): the test needs the
            // loop to actually sleep through the backoff, or there would be
            // nothing to "not wait for".
            delayer = { delay(it) },
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onFailure("rede cortada ao ir pro segundo plano")
        runCurrent()

        assertEquals("o laço está dormindo o backoff", 1, factory.openedUrls.size)

        client.reconectarAgora()
        runCurrent()

        assertEquals("voltou ao app: tenta na hora", 2, factory.openedUrls.size)
        assertTrue(
            "o reattach preserva a tela — attach=1 impede replay duplicado do scrollback",
            factory.openedUrls[1].contains("attach=1"),
        )

        // And it does not disturb one that is already up.
        factory.listeners[1].onOpen()
        client.reconectarAgora()
        runCurrent()
        assertEquals("conexão viva não é derrubada", 2, factory.openedUrls.size)
    }

    @Test
    fun `o que foi digitado com o socket caido e enfileirado e sai na ordem ao reconectar`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        // It drops: from here to the next open there is no socket at all, and
        // that is the window in which `send` used to swallow the bytes in
        // silence.
        factory.listeners[0].onFailure("rede cortada ao ir pro segundo plano")

        client.send(byteArrayOf('l'.code.toByte()))
        client.send(byteArrayOf('s'.code.toByte()))
        assertFalse("nada foi descartado", client.digitacaoDescartada.value)

        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()

        val frames = factory.sockets[1].binaryFrames
        assertEquals("os dois bytes saíram, nenhum a mais", 2, frames.size)
        assertTrue(frames[0].contentEquals(byteArrayOf('l'.code.toByte())))
        assertTrue(frames[1].contentEquals(byteArrayOf('s'.code.toByte())))
    }

    @Test
    fun `fila estourada descarta e AVISA em vez de sumir em silencio`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onFailure("queda")

        client.send(ByteArray(MAX_PENDENTE_BYTES))
        assertFalse(client.digitacaoDescartada.value)
        // This one no longer fits: the whole queue goes, and the warning lights up.
        client.send(byteArrayOf(1))
        assertTrue("estourou o teto: alguém precisa ser avisado", client.digitacaoDescartada.value)

        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()

        assertEquals("nada represado é mandado depois de descartado", 0, factory.sockets[1].binaryFrames.size)
        assertFalse("conexão nova limpa o aviso", client.digitacaoDescartada.value)
    }

    @Test
    fun `a 4404 close settles on SessionEnded and stops retrying, whether on first connect or a reconnect`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onClosed(4404, "session ended")
        runCurrent()

        assertEquals(ConnectionState.SessionEnded, client.state.value)
        assertEquals("nenhuma nova tentativa após 4404", 1, factory.openedUrls.size)

        // Confirm it really stopped: advancing further time still makes no new attempt.
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
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onClosed(1006, "queda")
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
            wsBaseUrl = "wss://vpsm.example",
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

    /**
     * THE DEFECT THIS TEST EXISTS TO PREVENT
     *
     * The PTY's size is not the client's property alone: the server moves it
     * itself during the attach's repaint wobble, and restores it to the last
     * value the client reported — which may be stale. Because the app only
     * spoke up when ITS OWN size changed
     * (`aplicarTamanho` starts with `if (cols == gridCols && rows == gridRows) return`),
     * the two sides drifted apart with nothing to reconcile them, and the
     * remote program painted for a screen of the wrong height: text at the
     * top, emptiness below. Only typing fixed it, because the IME's
     * composition band changed the height for real and finally triggered a
     * send.
     *
     * Proven in the service log:
     * `repaint-wobble no attach: 67x48 → 67x24 → 67x48` right after
     * `resize do cliente: 67x53 → 67x47`.
     */
    @Test
    fun `a reconexao reafirma o tamanho da grade sem ninguem pedir de novo`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1", "t2")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
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

        // The connection drops and comes back — and NOBODY calls sendResize
        // again, which is exactly what happens on the device: the height did
        // not change from the app's point of view.
        factory.listeners[0].onClosed(1006, "rede cortada no segundo plano")
        runCurrent()
        factory.listeners[1].onOpen()
        runCurrent()

        val frames = factory.sockets[1].textFrames
        assertTrue(
            "o socket novo precisa receber o tamanho: sem isso o servidor segue " +
                "com a geometria que o wobble deixou (frames=$frames)",
            frames.any {
                it.contains("\"type\":\"resize\"") &&
                    it.contains("\"cols\":67") &&
                    it.contains("\"rows\":53")
            },
        )
    }

    /**
     * Before a known size exists there is nothing to reassert — and sending an
     * invented resize would be worse than sending nothing.
     */
    @Test
    fun `sem tamanho conhecido a conexao nao inventa um resize`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )
        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        assertTrue(
            "nada deve ser enviado antes da primeira medição da grade",
            factory.sockets[0].textFrames.none { it.contains("\"type\":\"resize\"") },
        )
    }
    @Test
    fun `digitar DURANTE a reconexao nao some — a tentativa em voo nao conta como conexao`() = runTest {
        // THE OWNER'S COMPLAINT, word for word: "I can't type in the terminal
        // while it's reconnecting."
        //
        // The defect: `socket` was assigned the instant the attempt BEGAN
        // (webSocketFactory.open returns immediately; onOpen arrives later, on
        // another thread). `send` saw a non-null field, concluded there was a
        // connection, and sent to a still-closed socket — which discards
        // silently. And because it was not null, the queue was never engaged:
        // the text neither reached the server NOR became pending input. It
        // vanished.
        val factory = FakeWebSocketFactory()
        val client = TerminalSocketClient(
            name = "main",
            ticketSource = FakeTicketSource(MutableList(6) { "t$it" }),
            webSocketFactory = factory,
            wsBaseUrl = "wss://vpsm.example",
            scope = backgroundScope,
            onBytes = {},
            delayer = recordingDelayer(mutableListOf()),
        )

        client.connect()
        runCurrent()
        factory.listeners[0].onOpen()
        factory.listeners[0].onFailure("rede cortada")

        // The next attempt ALREADY EXISTS (the socket has been created and
        // assigned), but it has NOT opened yet. This is the whole window of
        // the defect.
        runCurrent()
        assertEquals("a segunda tentativa tem que estar em voo", 2, factory.sockets.size)

        client.send(byteArrayOf('l'.code.toByte()))
        client.send(byteArrayOf('s'.code.toByte()))

        assertTrue(
            "nada pode ir para um socket que ainda nao abriu",
            factory.sockets[1].binaryFrames.isEmpty(),
        )
        assertEquals(
            "e o que foi digitado tem que ficar VISIVEL enquanto espera",
            "ls",
            client.digitacaoPendente.value,
        )

        factory.listeners[1].onOpen()
        runCurrent()

        val frames = factory.sockets[1].binaryFrames
        assertEquals("os dois bytes sairam, nenhum a mais", 2, frames.size)
        assertTrue(frames[0].contentEquals(byteArrayOf('l'.code.toByte())))
        assertTrue(frames[1].contentEquals(byteArrayOf('s'.code.toByte())))
        assertEquals("e a faixa some quando eles REALMENTE subiram", "", client.digitacaoPendente.value)
    }

}
