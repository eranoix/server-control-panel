package dev.servercontrolpanel.feature.auth

import org.junit.Assert.assertEquals
import org.junit.Test

class AuthGateScreenTest {

    @Test
    fun `with a configured server the gate starts at login`() {
        assertEquals(AuthGateStep.Login, initialAuthGateStep(serverConfigured = true))
    }

    @Test
    fun `with no server the gate starts at QR pairing`() {
        assertEquals(AuthGateStep.Pairing, initialAuthGateStep(serverConfigured = false))
    }
}
