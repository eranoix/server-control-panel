package com.vpsmanager.feature.videocall.call

import android.content.Context
import android.Manifest
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.rule.GrantPermissionRule
import androidx.test.uiautomator.UiDevice
import com.vpsmanager.data.videocall.PeerInfo
import com.vpsmanager.data.videocall.RoomInfo
import com.vpsmanager.data.videocall.SignalingMessage
import com.vpsmanager.data.videocall.TurnCredentials
import com.vpsmanager.data.videocall.VideoCallSessionController
import com.vpsmanager.data.videocall.VideocallSignaling
import com.vpsmanager.feature.videocall.CallPermissionChecker
import com.vpsmanager.feature.videocall.CallViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.encodeToJsonElement
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.webrtc.EglBase
import org.webrtc.PeerConnection
import org.webrtc.VideoTrack

private val testJson = Json { ignoreUnknownKeys = true }
private const val ROOM_ID = "sala-bg"

/**
 * Never touches a real socket — same seam pattern as [com.vpsmanager.feature.videocall.CallViewModelTest]'s
 * `FakeSignaling`. Reused here (not imported from the `test` source set, which androidTest cannot
 * see) because a real `VideocallSignalingClient` would require a live server and TURN
 * credentials this instrumented run does not have, and because a test double must never reach
 * the real signaling server.
 */
private class FakeSignaling : VideocallSignaling {
    private val inbound = MutableSharedFlow<SignalingMessage>(extraBufferCapacity = 16)

    override fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage> = inbound.asSharedFlow()

    override fun send(msg: SignalingMessage): Boolean = true

    override fun close() = Unit

    fun push(message: SignalingMessage) = check(inbound.tryEmit(message))
}

/**
 * Records whether anything tore the call down. A real [org.webrtc.PeerConnection] needs the
 * native WebRTC library plus a live signaling/TURN round-trip — outside what an instrumented
 * test can set up without a second physical client (that gap is exactly what the
 * human-verification checkpoint covers). What this fake CAN prove, running on the real Android
 * runtime, is that backgrounding/screen-off never reaches this class's [dispose] or
 * [closePeerConnectionFor] — i.e. nothing in [CallViewModel] or [CallForegroundService] wires a
 * lifecycle/screen event to a teardown call.
 */
private class FakeSessionController : VideoCallSessionController {
    override val eglBaseContext: EglBase.Context = object : EglBase.Context {
        override fun getNativeEglContext(): Long = 0L
    }
    override var localVideoTrack: VideoTrack? = null
    override var localAudioTrack: org.webrtc.AudioTrack? = null

    var disposeCalls = 0
        private set
    val closedConnections = mutableListOf<String>()

    override fun startLocalMedia() = Unit
    override fun createPeerConnectionFor(peerId: String, turn: TurnCredentials, observer: PeerConnection.Observer): PeerConnection? = null
    override fun closePeerConnectionFor(peerId: String) {
        closedConnections += peerId
    }
    override fun setMicEnabled(enabled: Boolean) = Unit
    override fun setCameraEnabled(enabled: Boolean) = Unit
    override fun switchCamera() = Unit
    override fun dispose() {
        disposeCalls += 1
    }
}

private inline fun <reified T> jsonPayload(value: T): JsonElement = testJson.encodeToJsonElement(value)

private fun joinedMessage() = SignalingMessage(
    type = "joined",
    payload = jsonPayload(
        com.vpsmanager.data.videocall.JoinResponse(
            peerId = "self-1",
            room = RoomInfo(id = ROOM_ID, name = "Sala BG", owner = "admin", members = emptyList(), createdAt = 0L),
            peers = emptyList<PeerInfo>(),
            turn = TurnCredentials(urls = listOf("turn:example.org"), username = "u", credential = "c", ttl = 60L),
            politenessSeed = "self-1",
        ),
    ),
)

/**
 * The requirement under test: a call, once joined, must keep [CallForegroundService] alive when
 * the app is backgrounded or the screen turns off — Telecom/the OS must never treat either
 * event as a hangup signal.
 *
 * Deviation from the specification's literal `<behavior>` text (documented here, not silently
 * substituted):
 * - The specification describes driving `ActivityScenario` through `Lifecycle.State.CREATED` for
 *   a "call screen Activity/host". `:feature-videocall` is a library module with no Activity of
 *   its own (`CallScreen` is a Composable hosted by `:app`'s single `MainActivity`, which this
 *   module cannot depend on — see `VpsmConnection`'s own doc comment on that same boundary).
 *   There is therefore no Activity class this module's androidTest can launch via
 *   `ActivityScenario`. [CallForegroundService] is a plain `Service`, independent of any
 *   Activity's lifecycle by design (that independence is the entire mechanism) — so this test
 *   instead drives the REAL equivalent of backgrounding, [UiDevice.pressHome], which moves the
 *   whole instrumented process out of the foreground exactly as a user tapping Home would, and
 *   asserts against the real [CallForegroundService.isRunning] flag rather than a
 *   lifecycle-state mock.
 * - The second specified test names `Intent.ACTION_SCREEN_OFF` as the simulated event. That is a
 *   protected system broadcast (`android.permission.BROADCAST_SCREEN_OFF` is signature-only) —
 *   an app process calling `sendBroadcast(Intent(ACTION_SCREEN_OFF))` gets a `SecurityException`,
 *   not a delivered broadcast. [UiDevice.sleep]/[UiDevice.wakeUp] (`androidx.test.uiautomator`)
 *   is the standard, permission-safe way to simulate a real screen-off/on cycle in an
 *   instrumented test, so this test uses that instead.
 */
@RunWith(AndroidJUnit4::class)
@OptIn(ExperimentalCoroutinesApi::class)
class BackgroundedCallSurvivesTest {

    /**
     * CAMERA/RECORD_AUDIO must be GRANTED before `startForeground()`, because
     * [CallForegroundService] comes up with the composite type `phoneCall|camera|microphone`.
     * Without them the system returns `SecurityException: Starting FGS with type microphone ...
     * requires ... RECORD_AUDIO` — measured on this emulator — and the service dies in
     * `onStartCommand`. This is not test slack: it is exactly the precondition
     * [VpsmConnection.onAnswer] checks in production before bringing the service up, and that
     * [com.vpsmanager.feature.videocall.RoomLobbyScreen] asks for proactively. The operator's
     * device needs those same two permissions granted.
     */
    @get:Rule
    val grantCameraAndMic: GrantPermissionRule =
        GrantPermissionRule.grant(Manifest.permission.CAMERA, Manifest.permission.RECORD_AUDIO)

    private val dispatcher = StandardTestDispatcher()
    private val context: Context = ApplicationProvider.getApplicationContext()
    private val uiDevice: UiDevice = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation())

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        // Symmetric with every test's own cleanup below — never leaves a real Service running
        // for a later test in this class (or a later class in the same instrumentation run) to
        // trip over.
        CallForegroundService.stop(context)
        waitUntil(timeoutMs = 5_000) { !CallForegroundService.isRunning }
        Dispatchers.resetMain()
    }

    @Test
    fun foregroundServiceAndPeerConnectionSurviveBackgrounding() = runTest {
        val signaling = FakeSignaling()
        val sessionController = FakeSessionController()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = sessionController,
            signaling = signaling,
            foregroundService = AndroidCallForegroundServiceController(context),
        )

        viewModel.joinRoom(ROOM_ID)
        dispatcher.scheduler.advanceUntilIdle()
        signaling.push(joinedMessage())
        dispatcher.scheduler.advanceUntilIdle()

        // startForegroundService() dispatches onStartCommand() asynchronously on the real
        // Service's own thread — poll instead of asserting immediately after the call returns.
        assertTrue(
            "CallForegroundService never reported running after join",
            waitUntil(timeoutMs = 5_000) { CallForegroundService.isRunning },
        )

        uiDevice.pressHome()
        Thread.sleep(1_000)

        assertTrue(
            "CallForegroundService was stopped merely by backgrounding the app",
            CallForegroundService.isRunning,
        )
        assertEquals(
            "backgrounding alone must never dispose the session/peer connection",
            0,
            sessionController.disposeCalls,
        )

        viewModel.onLeave()
        assertTrue(
            "CallForegroundService did not stop after onLeave()",
            waitUntil(timeoutMs = 5_000) { !CallForegroundService.isRunning },
        )
    }

    @Test
    fun screenOffDoesNotStopForegroundService() {
        CallForegroundService.start(context)
        assertTrue(
            "CallForegroundService never reported running after start()",
            waitUntil(timeoutMs = 5_000) { CallForegroundService.isRunning },
        )

        uiDevice.sleep()
        Thread.sleep(1_000)
        try {
            assertTrue(
                "CallForegroundService was stopped merely by the screen turning off",
                CallForegroundService.isRunning,
            )
        } finally {
            uiDevice.wakeUp()
        }

        CallForegroundService.stop(context)
        assertTrue(
            "CallForegroundService did not stop after stop()",
            waitUntil(timeoutMs = 5_000) { !CallForegroundService.isRunning },
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
