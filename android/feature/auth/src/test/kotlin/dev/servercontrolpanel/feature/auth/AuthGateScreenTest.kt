package dev.servercontrolpanel.feature.auth

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * "Has a server address" and "has a session" are different states. With a
 * built-in default server, a fresh install has an address but no credential,
 * so it starts at login (which offers pairing), not the camera.
 */
class AuthGateScreenTest {

    @Test
    fun `with a configured server the gate starts at login`() {
        assertEquals(AuthGateStep.Login, initialAuthGateStep(serverConfigured = true))
    }

    @Test
    fun `with no server the gate starts at QR pairing`() {
        // The QR carries the address, so the user never types a hostname.
        assertEquals(AuthGateStep.Pairing, initialAuthGateStep(serverConfigured = false))
    }
}
