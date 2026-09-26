package com.vpsmanager.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-JVM tests for [MainActivity.onCreate]'s non-UI launch decisions, extracted into
 * [shouldShowDiagnosticScreen], [shouldSeedDefaultServer] and [decideLaunchDestination] so they
 * can be tested without Robolectric or Compose.
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
                lastCrash = "thread: main\njava.lang.IllegalStateException: simulated failure",
                initFailures = emptyList(),
            ),
        )
    }

    @Test
    fun `an init failure alone, with no crash, shows the diagnostic screen`() {
        assertTrue(
            shouldShowDiagnosticScreen(
                lastCrash = null,
                initFailures = listOf("notification channels: SecurityException: channel refused"),
            ),
        )
    }

    @Test
    fun `both a crash and init failures still show only the one diagnostic screen decision`() {
        assertTrue(
            shouldShowDiagnosticScreen(
                lastCrash = "thread: main\njava.lang.RuntimeException: fatal crash",
                initFailures = listOf("appScope: IllegalStateException: simulated failure"),
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
    fun `a fresh install with a seeded server and no session goes to sign-in, not Home`() {
        // `onCreate` seeds the default server itself, so "server configured" alone must
        // never open the shell without credentials.
        assertEquals(
            LaunchDestination.AuthGate,
            decideLaunchDestination(hasServerConfigured = true, hasSession = false),
        )
    }

    @Test
    fun `a configured server plus a valid session goes to Home`() {
        assertEquals(
            LaunchDestination.Home,
            decideLaunchDestination(hasServerConfigured = true, hasSession = true),
        )
    }

    @Test
    fun `with no server and no session the first screen is the auth gate`() {
        assertEquals(
            LaunchDestination.AuthGate,
            decideLaunchDestination(hasServerConfigured = false, hasSession = false),
        )
    }

    @Test
    fun `a stored session without a configured server does not lead to Home`() {
        // Corrupt state or a partial backup restore: a token but no server to talk to.
        assertEquals(
            LaunchDestination.AuthGate,
            decideLaunchDestination(hasServerConfigured = false, hasSession = true),
        )
    }

    @Test
    fun `an already-configured device is never reseeded, even with a non-blank default`() {
        assertFalse(
            shouldSeedDefaultServer(
                currentBaseUrl = "https://already-paired.example.com",
                defaultServerUrl = "https://panel.northwind.example",
            ),
        )
    }
}
