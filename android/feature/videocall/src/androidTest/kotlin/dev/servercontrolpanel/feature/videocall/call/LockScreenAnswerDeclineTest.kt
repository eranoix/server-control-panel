package dev.servercontrolpanel.feature.videocall.call

import android.Manifest
import android.content.Context
import android.telecom.Connection
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.rule.GrantPermissionRule
import dev.servercontrolpanel.data.videocall.ActiveCallRegistry
import dev.servercontrolpanel.feature.videocall.CallPermissionChecker
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
 * Answering from the lock screen starts [CallForegroundService] and opens the app with the room
 * id; declining does neither and unregisters the call from [ActiveCallRegistry].
 *
 * Runs on a device rather than Robolectric because the shadow of `android.telecom.Connection`
 * is not trusted; [PanelConnection] is constructed directly, as `PanelConnectionService` does.
 */
@RunWith(AndroidJUnit4::class)
class LockScreenAnswerDeclineTest {

    /**
     * `startForeground()` with camera and microphone types throws `SecurityException` unless both
     * are granted. The denied path is covered through the injectable `permissionChecker`.
     */
    @get:Rule
    val grantCameraAndMic: GrantPermissionRule =
        GrantPermissionRule.grant(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO)

    private val context: Context = ApplicationProvider.getApplicationContext()

    @Before
    fun setUp() {
        // Isolates this class from service state left by earlier instrumented tests.
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
        val connection = PanelConnection(
            context = context,
            callId = callId,
            roomId = "room-lock-answer",
            permissionChecker = CallPermissionChecker { emptyList() },
            launchHostActivity = { _, roomId -> launchedRoomId = roomId },
        )

        connection.onAnswer()

        assertTrue(
            "CallForegroundService never reported running after onAnswer()",
            waitUntil(timeoutMs = 5_000) { CallForegroundService.isRunning },
        )
        assertEquals("room-lock-answer", launchedRoomId)
        assertEquals(Connection.STATE_ACTIVE, connection.state)

        // onDisconnect() must also stop the service it started.
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
        val connection = PanelConnection(
            context = context,
            callId = callId,
            roomId = "room-lock-reject",
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
