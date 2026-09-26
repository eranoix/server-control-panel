package com.vpsmanager.feature.auth

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Pins down the separation between "has a server address" and "has a session",
 * which `MainActivity` treated as a single boolean.
 *
 * With the server baked into the build (`BuildConfig.DEFAULT_SERVER_URL`,
 * seeded on first boot), "configured" stopped meaning "ready to use". A
 * freshly installed device has an address and no credential: the right
 * starting point is the login screen — which offers pairing — and not the
 * camera.
 */
class AuthGateScreenTest {

    @Test
    fun `com servidor configurado o portao comeca no login`() {
        assertEquals(AuthGateStep.Login, initialAuthGateStep(serverConfigured = true))
    }

    @Test
    fun `sem servidor nenhum o portao comeca no pareamento por QR`() {
        // The QR carries the address with it, so pairing is the only path that
        // does not force you to type a hostname on the phone.
        assertEquals(AuthGateStep.Pairing, initialAuthGateStep(serverConfigured = false))
    }
}
