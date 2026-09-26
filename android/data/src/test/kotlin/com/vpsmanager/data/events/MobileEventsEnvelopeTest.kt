package com.vpsmanager.data.events

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Test

class MobileEventsEnvelopeTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `decoding a subscribed ack produces SubscribedAck with the same channel`() {
        val decoded = json.decodeFromString(SubscribedAck.serializer(), """{"op":"subscribed","channel":"notify.inbox"}""")

        assertEquals(SubscribedAck(op = "subscribed", channel = "notify.inbox"), decoded)
    }

    @Test
    fun `decoding an event frame produces MobileEvent with the raw data payload`() {
        val decoded = json.decodeFromString(
            MobileEvent.serializer(),
            """{"v":1,"channel":"queue.job:abc123","type":"progress","data":{"pct":42}}""",
        )

        assertEquals(1, decoded.v)
        assertEquals("queue.job:abc123", decoded.channel)
        assertEquals("progress", decoded.type)
        assertEquals(json.parseToJsonElement("""{"pct":42}"""), decoded.data)
    }

    @Test
    fun `encoding a subscribe request produces exactly the op and channel fields`() {
        val encoded = json.encodeToString(ClientOp.serializer(), ClientOp(op = "subscribe", channel = "notify.inbox"))

        assertEquals("""{"op":"subscribe","channel":"notify.inbox"}""", encoded)
    }

    @Test
    fun `encoding an unsubscribe request uses the same shape with a different op value`() {
        val encoded = json.encodeToString(ClientOp.serializer(), ClientOp(op = "unsubscribe", channel = "notify.inbox"))

        assertEquals("""{"op":"unsubscribe","channel":"notify.inbox"}""", encoded)
    }
}
