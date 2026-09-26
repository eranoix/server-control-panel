package com.vpsmanager.feature.videocall.call

import android.Manifest
import android.content.Context
import android.telecom.Connection
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.rule.GrantPermissionRule
import com.vpsmanager.data.videocall.ActiveCallRegistry
import com.vpsmanager.feature.videocall.CallPermissionChecker
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Answering or declining from the lock screen must reach the right side effects —
 * `onAnswer()` starts [CallForegroundService] and hands off to the app's own UI; `onReject()`
 * tears everything down without ever doing either.
 *
 * Deviation from the literal `<behavior>` text originally specified (documented here, not
 * silently substituted, and repeated in the delivery summary):
 * - The specification asks for "Telecom's test connection APIs" or a fake
 *   `Connection.Listener`, with a fallback to constructing [VpsmConnection] directly.
 *   `VpsmConnectionService` already constructs [VpsmConnection] directly with no Telecom
 *   round-trip needed to reach it (see its `onCreateIncomingConnection`), and
 *   [android.telecom.Connection]'s own state/`setActive`/`setDisconnected` methods work
 *   standalone (no bound `ConnectionService` required to observe them) — Robolectric's shadow
 *   of this exact class is the one that was explicitly distrusted here (see this class's own
 *   package doc comments), which is why this is a real `androidTest`, not a `Robolectric`
 *   unit test. So this test constructs [VpsmConnection] directly, on the real
 *   `android.telecom.Connection` runtime, exactly the fallback path the `<action>` describes.
 * - The specification says `onAnswer()` should reach "the shared join use-case" and
 *   `onReject()` should call "`WebRtcSessionManager.leave()`/cleanup". The actual, shipped
 *   design (see [VpsmConnection]'s class doc) never calls a join use-case or
 *   `WebRtcSessionManager` directly from either method — join is deliberately deferred to
 *   [CallViewModel.joinRoom] once `launchHostActivity` foregrounds the app's own `CallScreen`
 *   (a second Telecom round-trip would duplicate, not converge with, that one join path).
 *   This test therefore asserts the real observable contract instead: `onAnswer()` starts
 *   [CallForegroundService] and invokes the injected `launchHostActivity` with the ringing
 *   room's ID; `onReject()` invokes neither and leaves the call unregistered in
 *   [ActiveCallRegistry].
 */
@RunWith(AndroidJUnit4::class)
class LockScreenAnswerDeclineTest {

    /**
     * `onAnswer()` only starts [CallForegroundService] after checking CAMERA+RECORD_AUDIO, and
     * `startForeground()` itself with the `camera|microphone` types requires both to be granted
     * ALREADY (otherwise: `SecurityException: Starting FGS with type microphone ... requires ...
     * RECORD_AUDIO`). Granting them here reproduces the real state of a user who has already
     * been through the lobby's proactive request — the denied path stays covered by the
     * injectable `permissionChecker`, with no dependency on `pm revoke`.
     */
    @get:Rule
    val grantCameraAndMic: GrantPermissionRule =
        GrantPermissionRule.grant(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO)

    private val context: Context = ApplicationProvider.getApplicationContext()

    @Before
    fun setUp() {
        // Isolates this class from whatever state a previous instrumented test class in the
        // same run left CallForegroundService in.
        CallForegroundService.stop(context)
        waitUntil(timeoutMs = 5_000) { !CallForegroundService.isRunning }
    }

    @After
    fun tearDown() {
        CallForegroundService.stop(context)
        waitUntil(timeoutMs = 5_000) { !CallForegroundService.isRunning }
    }

    @Test
    fun onAnswerJoinsRoomAndStartsForegroundService() {
        var launchedRoomId: String? = null
        val callId = "call-answer-1"
        val connection = VpsmConnection(
            context = context,
            callId = callId,
            roomId = "sala-lock-answer",
            permissionChecker = CallPermissionChecker { emptyList() },
            launchHostActivity = { _, roomId -> launchedRoomId = roomId },
        )

        connection.onAnswer()

        assertTrue(
            "CallForegroundService never reported running after onAnswer()",
            waitUntil(timeoutMs = 5_000) { CallForegroundService.isRunning },
        )
        assertEquals("sala-lock-answer", launchedRoomId)
        assertEquals(Connection.STATE_ACTIVE, connection.state)

        // Cleanup mirrors what a real call end does — proves onDisconnect() also stops the
        // service it started, not just onReject()'s path.
        connection.onDisconnect()
        assertTrue(
            "CallForegroundService did not stop after onDisconnect()",
            waitUntil(timeoutMs = 5_000) { !CallForegroundService.isRunning },
        )
        assertFalse(
            "call was still registered in ActiveCallRegistry after teardown",
            ActiveCallRegistry.endCall(callId),
        )
    }

    @Test
    fun onRejectTearsDownWithoutJoining() {
        var launchedRoomId: String? = null
        val callId = "call-reject-1"
        val connection = VpsmConnection(
            context = context,
            callId = callId,
            roomId = "sala-lock-reject",
            permissionChecker = CallPermissionChecker { emptyList() },
            launchHostActivity = { _, roomId -> launchedRoomId = roomId },
        )

        connection.onReject()

        assertNull("declining a call must never launch the host activity", launchedRoomId)
        assertFalse(
            "declining a call must never start CallForegroundService",
            CallForegroundService.isRunning,
        )
        assertEquals(Connection.STATE_DISCONNECTED, connection.state)
        assertFalse(
            "call was still registered in ActiveCallRegistry after onReject()",
            ActiveCallRegistry.endCall(callId),
        )
    }
}

/** Polls [condition] until it is true or [timeoutMs] elapses; returns the final observed value. */
private fun waitUntil(timeoutMs: Long, pollMs: Long = 100, condition: () -> Boolean): Boolean {
    val deadline = System.currentTimeMillis() + timeoutMs
    while (System.currentTimeMillis() < deadline) {
        if (condition()) return true
        Thread.sleep(pollMs)
    }
    return condition()
}
