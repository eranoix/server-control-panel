package com.vpsmanager.data.whatsapp

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class WhatsAppWsUrlResolverTest {

    @Test
    fun `an absolute https base resolves to a wss URL rooted at ws-whatsapp`() {
        val result = resolveWhatsAppWsUrl("https://vpsm.example/api/mobile/v1")

        assertEquals("wss://vpsm.example/ws/whatsapp", result)
    }

    @Test
    fun `an absolute http base resolves to a plain ws URL`() {
        val result = resolveWhatsAppWsUrl("http://10.0.2.2:8765/api/mobile/v1")

        assertEquals("ws://10.0.2.2:8765/ws/whatsapp", result)
    }

    @Test
    fun `today's actual relative default has no resolvable host, so this returns null`() {
        val result = resolveWhatsAppWsUrl("/api/mobile/v1")

        assertNull(result)
    }
}
