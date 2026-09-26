package com.vpsmanager.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-JVM pins for [MainActivity.onCreate]'s two non-UI decisions, extracted into
 * [shouldShowDiagnosticScreen] and [shouldSeedDefaultServer] precisely so they can be tested here
 * without Robolectric or Compose. This app has never run on a real device or emulator: a wrong
 * answer to either question on first boot (showing Home instead of the crash report, or silently
 * skipping the one-time server seed) would ship unnoticed.
 */
class LaunchDecisionsTest {

    @Test
    fun `no crash and no init failures does not show the diagnostic screen`() {
        assertFalse(shouldShowDiagnosticScreen(lastCrash = null, initFailures = emptyList()))
    }

    @Test
    fun `a persisted crash alone shows the diagnostic screen`() {
        assertTrue(
            shouldShowDiagnosticScreen(
                lastCrash = "thread: main\njava.lang.IllegalStateException: falha simulada",
                initFailures = emptyList(),
            ),
        )
    }

    @Test
    fun `an init failure alone, with no crash, shows the diagnostic screen`() {
        assertTrue(
            shouldShowDiagnosticScreen(
                lastCrash = null,
                initFailures = listOf("canais de notificacao: SecurityException: canal recusado"),
            ),
        )
    }

    @Test
    fun `both a crash and init failures still show only the one diagnostic screen decision`() {
        assertTrue(
            shouldShowDiagnosticScreen(
                lastCrash = "thread: main\njava.lang.RuntimeException: crash fatal",
                initFailures = listOf("appScope: IllegalStateException: falha simulada"),
            ),
        )
    }

    @Test
    fun `an unconfigured device with a non-blank default server URL seeds it`() {
        assertTrue(shouldSeedDefaultServer(currentBaseUrl = null, defaultServerUrl = "https://panel.northwind.example"))
    }

    @Test
    fun `an unconfigured device with a blank default server URL (no build property) does not seed`() {
        assertFalse(shouldSeedDefaultServer(currentBaseUrl = null, defaultServerUrl = ""))
    }

    @Test
    fun `uma instalacao nova, com servidor semeado e sem sessao, vai para o login e nao para a Home`() {
        // The exact regression this method exists to lock down. `onCreate`
        // decided by `currentBaseUrl() != null`, and `onCreate` itself seeds
        // BuildConfig.DEFAULT_SERVER_URL a few lines above — so on a fresh
        // install the test was born true and the app opened AppNavHost with no
        // credentials at all. Every screen hit a 401 with a "Try again" that
        // could never work.
        assertEquals(
            LaunchDestination.AuthGate,
            decideLaunchDestination(hasServerConfigured = true, hasSession = false),
        )
    }

    @Test
    fun `servidor configurado mais sessao valida vao para a Home`() {
        assertEquals(
            LaunchDestination.Home,
            decideLaunchDestination(hasServerConfigured = true, hasSession = true),
        )
    }

    @Test
    fun `sem servidor e sem sessao a primeira tela e o portao de autenticacao`() {
        assertEquals(
            LaunchDestination.AuthGate,
            decideLaunchDestination(hasServerConfigured = false, hasSession = false),
        )
    }

    @Test
    fun `uma sessao gravada sem servidor configurado nao leva a Home`() {
        // Corrupt state (or a partial backup restore): there is a token, but
        // nobody to talk to. Sending them to Home would reproduce the same wall
        // of errors by another route.
        assertEquals(
            LaunchDestination.AuthGate,
            decideLaunchDestination(hasServerConfigured = false, hasSession = true),
        )
    }

    @Test
    fun `an already-configured device is never reseeded, even with a non-blank default`() {
        assertFalse(
            shouldSeedDefaultServer(
                currentBaseUrl = "https://ja-pareado.example.com",
                defaultServerUrl = "https://panel.northwind.example",
            ),
        )
    }
}
