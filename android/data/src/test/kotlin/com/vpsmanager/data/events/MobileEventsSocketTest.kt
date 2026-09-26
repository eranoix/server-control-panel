package com.vpsmanager.data.events

import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import kotlin.random.Random

/**
 * Hands out a queued sequence of tickets — proves a reconnect never reuses a consumed one.
 * [yield] is a real (if instant) suspension point, matching a real network call: without it,
 * [MobileEventsSocket]'s MINTING_TICKET->CONNECTING transition happens synchronously and a
 * StateFlow collector observing from another coroutine can miss the intermediate value
 * entirely (StateFlow conflates emissions a slow collector never got a chance to see).
 */
private class FakeTicketSource(private val tickets: MutableList<String>) : MobileEventsTicketSource {
    var callCount = 0
        private set

    override suspend fun wsTicket(): WsTicketResult {
        yield()
        callCount++
        if (tickets.isEmpty()) return WsTicketResult.Error("sem mais tickets fake")
        return WsTicketResult.Success(ticket = tickets.removeAt(0), expiresIn = 60)
    }
}

/** No real socket — just records every frame it was asked to send. */
private class RecordingWebSocket : MobileEventsWebSocket {
    val textFrames = mutableListOf<String>()
    var closed: Pair<Int, String>? = null

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
private class FakeWebSocketFactory : MobileEventsWebSocketFactory {
    val openedUrls = mutableListOf<String>()
    val sockets = mutableListOf<RecordingWebSocket>()
    val listeners = mutableListOf<MobileEventsWebSocketListener>()

    override fun open(url: String, listener: MobileEventsWebSocketListener): MobileEventsWebSocket {
        openedUrls += url
        listeners += listener
        val socket = RecordingWebSocket()
        sockets += socket
        return socket
    }
}

class MobileEventsSocketTest {

    private fun recordingDelayer(sink: MutableList<Long>): suspend (Long) -> Unit = { sink += it }

    // --- Property: pure backoff function actually applies the injected jitter source ---

    @Test
    fun `eventsBackoffDelayMs uses the injected random source, not a fixed formula`() {
        val same1 = eventsBackoffDelayMs(1, Random(42))
        val same2 = eventsBackoffDelayMs(1, Random(42))
        assertEquals("same seed must reproduce the same jittered delay", same1, same2)

        val differentSeed = eventsBackoffDelayMs(1, Random(7))
        assertNotEquals("a different seed must move the jitter away from the first delay", same1, differentSeed)

        // Both stay within the documented +/-20% jitter band around the base delay.
        assertTrue(same1 in 800L..1200L)
        assertTrue(differentSeed in 800L..1200L)
    }

    @Test
    fun `eventsBackoffDelayMs doubles per attempt and caps at 60s`() {
        val random = Random(1)
        val delays = (1..8).map { eventsBackoffDelayMs(it, random) }
        assertTrue("delay must never exceed the 60s cap", delays.all { it <= 60_000L })
        // Attempt 7 (base 64s, pre-cap) and attempt 8 must both land at the 60s ceiling.
        assertTrue(delays[6] in 48_000L..60_000L)
        assertTrue(delays[7] in 48_000L..60_000L)
    }

    // Property 1 + state machine: DISCONNECTED -> MINTING_TICKET -> CONNECTING -> CONNECTED ---

    @Test
    fun `start mints exactly one ticket then opens exactly one socket, walking the full state sequence`() = runTest {
        val factory = FakeWebSocketFactory()
        val ticketSource = FakeTicketSource(mutableListOf("t1"))
        val client = MobileEventsSocket(
            ticketSource = ticketSource,
            scope = backgroundScope,
            wsBaseUrl = "wss://vpsm.example",
            webSocketFactory = factory,
            delayer = recordingDelayer(mutableListOf()),
        )
        val observedStates = mutableListOf(client.state.value)
        backgroundScope.launch { client.state.collect { observedStates += it } }
        runCurrent()

        client.start()
        runCurrent()
        assertEquals(1, ticketSource.callCount)
        assertEquals(1, factory.openedUrls.size)
        assertTrue(factory.openedUrls[0].contains("ticket=t1"))

        factory.listeners[0].onOpen()
        runCurrent()

        assertEquals(
            listOf(
                ConnectionState.DISCONNECTED,
                ConnectionState.MINTING_TICKET,
                ConnectionState.CONNECTING,
                ConnectionState.CONNECTED,
            ),
            observedStates.distinct(),
        )
        assertEquals(ConnectionState.CONNECTED, client.state.value)
    }

    // Property 3: reconnect with backoff, fresh ticket every attempt, never a duplicate ticket ---

    @Test
    fun `handshake failure moves CONNECTING to BACKOFF and mints a fresh ticket before the next attempt`() = runTest {
        val factory = FakeWebSocketFactory()
        val ticketSource = FakeTicketSource(mutableListOf("t1", "t2", "t3"))
        val delays = mutableListOf<Long>()
        // A REAL delay (not a no-op recorder) is required here: it is the only way to freeze
        // the loop mid-BACKOFF under virtual time so the test can observe that exact state
        // before deciding to advance the clock — a no-op delayer would race straight through
        // BACKOFF into the next MINTING_TICKET/CONNECTING within the same runCurrent() drain.
        val client = MobileEventsSocket(
            ticketSource = ticketSource,
            scope = backgroundScope,
            wsBaseUrl = "wss://vpsm.example",
            webSocketFactory = factory,
            random = Random(99),
            delayer = { ms -> delays += ms; kotlinx.coroutines.delay(ms) },
        )

        client.start()
        runCurrent()
        factory.listeners[0].onFailure("handshake falhou")
        runCurrent()

        assertEquals(ConnectionState.BACKOFF, client.state.value)
        assertEquals(1, delays.size)
        assertEquals(1, ticketSource.callCount) // only the first attempt so far — backoff hasn't elapsed yet

        // Advance virtual time past the recorded backoff delay: only now must a fresh ticket
        // be minted and a second socket opened, never reusing the first ticket.
        advanceTimeBy(delays[0] + 1)
        runCurrent()

        assertEquals(2, ticketSource.callCount)
        assertEquals(2, factory.openedUrls.size)
        assertTrue(factory.openedUrls[0].contains("ticket=t1"))
        assertTrue(factory.openedUrls[1].contains("ticket=t2"))
        assertFalse("reconnect must never reuse the previous ticket", factory.openedUrls[1].contains("ticket=t1"))

        // A second consecutive failure must back off for at least as long as the first.
        factory.listeners[1].onFailure("handshake falhou de novo")
        runCurrent()
        assertEquals(ConnectionState.BACKOFF, client.state.value)
        assertEquals(2, delays.size)
        assertTrue("backoff must not shrink across consecutive failures", delays[1] >= delays[0])

        advanceTimeBy(delays[1] + 1)
        runCurrent()
        assertEquals(3, ticketSource.callCount)
        assertTrue(factory.openedUrls[2].contains("ticket=t3"))
    }

    // Property 4: stop() unconditionally closes and never reconnects ---

    @Test
    fun `stop while CONNECTED closes with 1000 background, settles DISCONNECTED, and never reconnects`() = runTest {
        val factory = FakeWebSocketFactory()
        val ticketSource = FakeTicketSource(mutableListOf("t1"))
        val delays = mutableListOf<Long>()
        val client = MobileEventsSocket(
            ticketSource = ticketSource,
            scope = backgroundScope,
            wsBaseUrl = "wss://vpsm.example",
            webSocketFactory = factory,
            delayer = recordingDelayer(delays),
        )

        client.start()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()
        assertEquals(ConnectionState.CONNECTED, client.state.value)

        client.stop()
        runCurrent()

        assertEquals(ConnectionState.DISCONNECTED, client.state.value)
        assertEquals(1000 to "background", factory.sockets[0].closed)

        // Advancing the (fake) clock well past any backoff window must trigger nothing further:
        // no new ticket call, no new socket open. This is the un-bypassable half of that rule.
        val ticketCallsAtStop = ticketSource.callCount
        val socketsOpenedAtStop = factory.openedUrls.size
        runCurrent()
        assertEquals(ticketCallsAtStop, ticketSource.callCount)
        assertEquals(socketsOpenedAtStop, factory.openedUrls.size)
    }

    @Test
    fun `start after stop restarts from a fresh ticket and a brand-new socket instance`() = runTest {
        val factory = FakeWebSocketFactory()
        val ticketSource = FakeTicketSource(mutableListOf("t1", "t2"))
        val client = MobileEventsSocket(
            ticketSource = ticketSource,
            scope = backgroundScope,
            wsBaseUrl = "wss://vpsm.example",
            webSocketFactory = factory,
            delayer = recordingDelayer(mutableListOf()),
        )

        client.start()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()
        client.stop()
        runCurrent()

        client.start()
        runCurrent()

        assertEquals(2, ticketSource.callCount)
        assertEquals(2, factory.openedUrls.size)
        assertTrue(factory.openedUrls[1].contains("ticket=t2"))
        assertFalse(factory.openedUrls[1].contains("ticket=t1"))
        assertNotEquals("must be a brand-new socket instance, not the closed one", factory.sockets[0], factory.sockets[1])

        factory.listeners[1].onOpen()
        runCurrent()
        assertEquals(ConnectionState.CONNECTED, client.state.value)
    }

    // --- Ack/event decoding + send() gating ---

    @Test
    fun `inbound text frames dispatch to acks or events depending on the op field`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = MobileEventsSocket(
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            scope = backgroundScope,
            wsBaseUrl = "wss://vpsm.example",
            webSocketFactory = factory,
            delayer = recordingDelayer(mutableListOf()),
        )
        val acks = mutableListOf<SubscribedAck>()
        val events = mutableListOf<MobileEvent>()
        backgroundScope.launch { client.acks.collect { acks += it } }
        backgroundScope.launch { client.events.collect { events += it } }

        client.start()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        factory.listeners[0].onTextMessage("""{"op":"subscribed","channel":"notify.inbox"}""")
        factory.listeners[0].onTextMessage("""{"v":1,"channel":"notify.inbox","type":"job.done","data":{"id":"1"}}""")
        runCurrent()

        assertEquals(listOf(SubscribedAck("subscribed", "notify.inbox")), acks)
        assertEquals(1, events.size)
        assertEquals("job.done", events[0].type)
    }

    @Test
    fun `send drops the frame when not CONNECTED and delivers it once CONNECTED`() = runTest {
        val factory = FakeWebSocketFactory()
        val client = MobileEventsSocket(
            ticketSource = FakeTicketSource(mutableListOf("t1")),
            scope = backgroundScope,
            wsBaseUrl = "wss://vpsm.example",
            webSocketFactory = factory,
            delayer = recordingDelayer(mutableListOf()),
        )

        // Not started yet: DISCONNECTED, must be dropped, not queued.
        assertFalse(client.send(ClientOp("subscribe", "notify.inbox")))

        client.start()
        runCurrent()
        // Still CONNECTING at this point (no onOpen yet) — must also be dropped.
        assertFalse(client.send(ClientOp("subscribe", "notify.inbox")))

        factory.listeners[0].onOpen()
        runCurrent()
        assertTrue(client.send(ClientOp("subscribe", "notify.inbox")))
        assertEquals(listOf("""{"op":"subscribe","channel":"notify.inbox"}"""), factory.sockets[0].textFrames)
    }
}
