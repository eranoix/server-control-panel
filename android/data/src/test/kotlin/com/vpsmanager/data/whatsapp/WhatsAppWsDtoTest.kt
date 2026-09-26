package com.vpsmanager.data.whatsapp

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class WhatsAppWsDtoTest {

    @Test
    fun `a message-kind frame maps to MessageReceived carrying the server id and chat`() {
        val frame = """
            {"kind":"message","ts":1700000000,"message":{"id":"m1","chat":"a@s.whatsapp.net","from_me":false,"ts":1700000000,"type":"text","body":"oi","ack":1}}
        """.trimIndent()

        val event = parseWhatsAppWsEvent(frame) as WhatsAppWsEvent.MessageReceived

        assertEquals("m1", event.message.id)
        assertEquals("a@s.whatsapp.net", event.message.chatJid)
        assertEquals("oi", event.message.text)
    }

    @Test
    fun `an ack-kind frame maps to Ack keyed by ack_id, with no chat_jid required`() {
        val frame = """{"kind":"ack","ts":1700000001,"ack_id":"m1","ack_n":2}"""

        val event = parseWhatsAppWsEvent(frame) as WhatsAppWsEvent.Ack

        assertEquals("m1", event.messageId)
        assertEquals(2, event.ackLevel)
    }

    @Test
    fun `a reaction-kind frame maps to Reaction carrying chat_jid, emoji and reactor`() {
        val frame = """
            {"kind":"reaction","ts":1700000002,"chat_jid":"a@s.whatsapp.net","ack_id":"m1","reaction_from":"a@s.whatsapp.net","reaction_emoji":"👍"}
        """.trimIndent()

        val event = parseWhatsAppWsEvent(frame) as WhatsAppWsEvent.Reaction

        assertEquals("a@s.whatsapp.net", event.chatJid)
        assertEquals("m1", event.messageId)
        assertEquals("👍", event.emoji)
    }

    @Test
    fun `a status-kind frame (no message data this plan acts on) maps to null`() {
        val frame = """{"kind":"status","ts":1700000003,"state":{"status":"CONNECTED"}}"""

        assertNull(parseWhatsAppWsEvent(frame))
    }

    @Test
    fun `malformed JSON maps to null instead of throwing`() {
        assertTrue(parseWhatsAppWsEvent("not json") == null)
    }
}
