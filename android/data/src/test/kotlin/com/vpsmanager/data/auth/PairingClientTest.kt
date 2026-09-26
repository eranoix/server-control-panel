package com.vpsmanager.data.auth

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PairingClientTest {

    private val client = PairingClient()
    private val validTicket = "a".repeat(48)
    private val serverUrl = "https://vpsm.example.com"

    private fun envelope(
        v: Int = 1,
        ticket: String = validTicket,
        serverUrl: String = this.serverUrl,
    ): String = """{"v":$v,"ticket":"$ticket","server_url":"$serverUrl","expires_in":300}"""

    @Test
    fun `parsePairingPayload accepts a well-formed envelope`() {
        val result = client.parsePairingPayload(envelope())
        assertEquals(PairingPayload(ticket = validTicket, serverUrl = serverUrl), result)
    }

    @Test
    fun `parsePairingPayload trims surrounding whitespace from the scanned payload`() {
        val result = client.parsePairingPayload("  ${envelope()}\n")
        assertEquals(PairingPayload(ticket = validTicket, serverUrl = serverUrl), result)
    }

    @Test
    fun `parsePairingPayload ignores unknown fields`() {
        val payload = """{"v":1,"ticket":"$validTicket","server_url":"$serverUrl","expires_in":300,"extra":"field"}"""
        val result = client.parsePairingPayload(payload)
        assertEquals(PairingPayload(ticket = validTicket, serverUrl = serverUrl), result)
    }

    @Test
    fun `parsePairingPayload rejects an unrecognized envelope version`() {
        assertNull(client.parsePairingPayload(envelope(v = 2)))
        assertNull(client.parsePairingPayload(envelope(v = 0)))
    }

    @Test
    fun `parsePairingPayload rejects a ticket that is not well-formed hex`() {
        assertNull(client.parsePairingPayload(envelope(ticket = "a".repeat(47))))
        assertNull(client.parsePairingPayload(envelope(ticket = "a".repeat(49))))
        assertNull(client.parsePairingPayload(envelope(ticket = "g".repeat(48))))
    }

    @Test
    fun `parsePairingPayload rejects a blank server_url`() {
        assertNull(client.parsePairingPayload(envelope(serverUrl = "")))
        assertNull(client.parsePairingPayload(envelope(serverUrl = "   ")))
    }

    @Test
    fun `parsePairingPayload rejects a bare ticket with no envelope`() {
        assertNull(client.parsePairingPayload(validTicket))
    }

    @Test
    fun `parsePairingPayload rejects malformed JSON`() {
        assertNull(client.parsePairingPayload("{not json"))
        assertNull(client.parsePairingPayload("https://evil.example/?ticket=$validTicket"))
    }

    @Test
    fun `parsePairingPayload rejects empty and oversized payloads without touching the network`() {
        assertNull(client.parsePairingPayload(""))
        assertNull(client.parsePairingPayload("a".repeat(10_000)))
    }
}
