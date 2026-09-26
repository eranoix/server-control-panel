package com.vpsmanager.data.events

import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Hands out an unlimited ticket per call — MobileEventsClient's tests only care about frames, not ticket exhaustion. */
private class ClientUnlimitedTicketSource : MobileEventsTicketSource {
    private var counter = 0
    override suspend fun wsTicket(): WsTicketResult {
        counter++
        return WsTicketResult.Success(ticket = "t$counter", expiresIn = 60)
    }
}

/** No real socket — just records every frame it was asked to send. */
private class ClientRecordingWebSocket : MobileEventsWebSocket {
    val textFrames = mutableListOf<String>()
    override fun sendText(text: String): Boolean {
        textFrames += text
        return true
    }
    override fun close(code: Int, reason: String): Boolean = true
}

/** No network — records every open() call and hands back a fresh [ClientRecordingWebSocket]. */
private class ClientFakeWebSocketFactory : MobileEventsWebSocketFactory {
    val sockets = mutableListOf<ClientRecordingWebSocket>()
    val listeners = mutableListOf<MobileEventsWebSocketListener>()
    override fun open(url: String, listener: MobileEventsWebSocketListener): MobileEventsWebSocket {
        listeners += listener
        val socket = ClientRecordingWebSocket()
        sockets += socket
        return socket
    }
}

class MobileEventsClientTest {

    // Property 1: reference-counted subscribe — one socket, one frame per channel ---

    @Test
    fun `two concurrent subscribers to the same channel share one subscribe frame and unsubscribe only after both cancel`() =
        runTest {
            val factory = ClientFakeWebSocketFactory()
            val socket = MobileEventsSocket(
                ticketSource = ClientUnlimitedTicketSource(),
                scope = backgroundScope,
                wsBaseUrl = "wss://vpsm.example",
                webSocketFactory = factory,
            )
            val client = MobileEventsClient(socket, backgroundScope)
            socket.start()
            runCurrent()
            factory.listeners[0].onOpen()
            runCurrent()

            val job1 = backgroundScope.launch { client.subscribe("notify.inbox").collect {} }
            runCurrent()
            val job2 = backgroundScope.launch { client.subscribe("notify.inbox").collect {} }
            runCurrent()

            assertEquals(
                "exactly one subscribe frame for two concurrent subscribers to the same channel",
                listOf("""{"op":"subscribe","channel":"notify.inbox"}"""),
                factory.sockets[0].textFrames,
            )

            job1.cancel()
            runCurrent()
            assertEquals(
                "cancelling only one of two subscribers must not unsubscribe yet",
                1,
                factory.sockets[0].textFrames.size,
            )

            job2.cancel()
            runCurrent()
            assertEquals(
                listOf(
                    """{"op":"subscribe","channel":"notify.inbox"}""",
                    """{"op":"unsubscribe","channel":"notify.inbox"}""",
                ),
                factory.sockets[0].textFrames,
            )
        }

    // Property 2: channel isolation — a subscriber on A must never see B ---

    @Test
    fun `subscriber on one channel never receives events published on another channel`() = runTest {
        val factory = ClientFakeWebSocketFactory()
        val socket = MobileEventsSocket(
            ticketSource = ClientUnlimitedTicketSource(),
            scope = backgroundScope,
            wsBaseUrl = "wss://vpsm.example",
            webSocketFactory = factory,
        )
        val client = MobileEventsClient(socket, backgroundScope)
        socket.start()
        runCurrent()
        factory.listeners[0].onOpen()
        runCurrent()

        val receivedOnA = mutableListOf<MobileEvent>()
        val receivedOnB = mutableListOf<MobileEvent>()
        backgroundScope.launch { client.subscribe("channel.a").collect { receivedOnA += it } }
        backgroundScope.launch { client.subscribe("channel.b").collect { receivedOnB += it } }
        runCurrent()

        factory.listeners[0].onTextMessage("""{"v":1,"channel":"channel.b","type":"only-b","data":{}}""")
        runCurrent()

        assertTrue("channel A's subscriber must never see a channel B event", receivedOnA.isEmpty())
        assertEquals(1, receivedOnB.size)
        assertEquals("only-b", receivedOnB[0].type)
    }

    // Property 3: reconnect resubscribes active channels — the actual resume mechanism ---

    @Test
    fun `reconnecting resends subscribe for every active channel — there is no cursor or sequence to resume from`() =
        runTest {
            val factory = ClientFakeWebSocketFactory()
            val socket = MobileEventsSocket(
                ticketSource = ClientUnlimitedTicketSource(),
                scope = backgroundScope,
                wsBaseUrl = "wss://vpsm.example",
                webSocketFactory = factory,
                delayer = { kotlinx.coroutines.delay(it) },
            )
            val client = MobileEventsClient(socket, backgroundScope)
            socket.start()
            runCurrent()
            factory.listeners[0].onOpen()
            runCurrent()

            backgroundScope.launch { client.subscribe("notify.inbox").collect {} }
            runCurrent()
            assertEquals(
                listOf("""{"op":"subscribe","channel":"notify.inbox"}"""),
                factory.sockets[0].textFrames,
            )

            // Simulate a drop and successful reconnect: the socket reopens a brand-new
            // connection (fresh ticket, new ClientRecordingWebSocket) and reaches CONNECTED again.
            factory.listeners[0].onFailure("conexao perdida")
            runCurrent()
            advanceTimeBy(70_000)
            runCurrent()
            factory.listeners[1].onOpen()
            runCurrent()

            assertEquals(
                "the second socket instance must see the resubscribe frame — no server-side replay to rely on",
                listOf("""{"op":"subscribe","channel":"notify.inbox"}"""),
                factory.sockets[1].textFrames,
            )
        }
}
