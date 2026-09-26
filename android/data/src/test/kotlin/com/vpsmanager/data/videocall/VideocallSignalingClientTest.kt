package com.vpsmanager.data.videocall

import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.yield
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Hands back a fixed ticket outcome and counts how many times it was asked. */
private class FakeTicketSource(private val result: VideocallWsTicketResult) : VideocallTicketSource {
    var callCount = 0
        private set

    override suspend fun wsTicket(): VideocallWsTicketResult {
        yield() // real suspension point, matching a real network call
        callCount++
        return result
    }
}

/** No real socket — just records every frame it was asked to send. */
private class RecordingWebSocket : VideocallWebSocket {
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
private class FakeWebSocketFactory : VideocallWebSocketFactory {
    val openedUrls = mutableListOf<String>()
    val sockets = mutableListOf<RecordingWebSocket>()

    override fun open(url: String, listener: VideocallWebSocketListener): VideocallWebSocket {
        openedUrls += url
        val socket = RecordingWebSocket()
        sockets += socket
        return socket
    }
}

class VideocallSignalingClientTest {

    @Test
    fun connectFetchesTicketThenOpensSocketWithIt() = runTest {
        val ticketSource = FakeTicketSource(VideocallWsTicketResult.Success("ticket-abc"))
        val factory = FakeWebSocketFactory()
        val client = VideocallSignalingClient(ticketSource, "ws://vps.example.com", factory)

        val job = launch { client.connect("room123", "client-uuid-1", resume = false).collect {} }
        advanceUntilIdle()

        assertEquals("exactly one ticket fetch, never a second/guessed call", 1, ticketSource.callCount)
        assertEquals("exactly one WS open, only after the ticket resolved", 1, factory.openedUrls.size)

        val url = factory.openedUrls.single()
        assertTrue("connects to /ws/videocall on the resolved base", url.startsWith("ws://vps.example.com/ws/videocall"))
        assertTrue("carries the exact ticket the fake GET returned", url.contains("ticket=ticket-abc"))
        assertTrue("carries the room id", url.contains("room_id=room123"))
        assertTrue("carries the client id", url.contains("client_id=client-uuid-1"))

        job.cancel()
    }

    @Test
    fun connectEncodesResumeAsLiteralOneOrZero() = runTest {
        val ticketSource = FakeTicketSource(VideocallWsTicketResult.Success("t"))
        val factory = FakeWebSocketFactory()
        val client = VideocallSignalingClient(ticketSource, "ws://vps.example.com", factory)

        val job = launch { client.connect("room1", "c1", resume = true).collect {} }
        advanceUntilIdle()

        // internal/videocall/ws.go: r.URL.Query().Get("resume") == "1" — never "true"/"false".
        assertTrue(factory.openedUrls.single().contains("resume=1"))
        job.cancel()
    }

    @Test
    fun ticketFailureEmitsErrorFrameAndNeverOpensSocket() = runTest {
        val ticketSource = FakeTicketSource(VideocallWsTicketResult.Error("sem rede"))
        val factory = FakeWebSocketFactory()
        val client = VideocallSignalingClient(ticketSource, "ws://vps.example.com", factory)

        val received = mutableListOf<SignalingMessage>()
        val job = launch { client.connect("room1", "c1", resume = false).collect { received += it } }
        advanceUntilIdle()

        assertEquals(0, factory.openedUrls.size)
        assertEquals(1, received.size)
        assertEquals("error", received.single().type)
        assertEquals("sem rede", received.single().error)

        job.cancel()
    }

    @Test
    fun closeSendsLeaveFrameThenClosesSocket() = runTest {
        val ticketSource = FakeTicketSource(VideocallWsTicketResult.Success("t"))
        val factory = FakeWebSocketFactory()
        val client = VideocallSignalingClient(ticketSource, "ws://vps.example.com", factory)

        val job = launch { client.connect("room1", "c1", resume = false).collect {} }
        advanceUntilIdle()

        client.close()

        val socket = factory.sockets.single()
        assertTrue(socket.textFrames.any { it.contains("\"leave\"") })
        assertEquals(1000, socket.closed?.first)

        job.cancel()
    }
}
