package dev.servercontrolpanel.feature.videocall.call

import android.content.Context
import android.Manifest
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.rule.GrantPermissionRule
import androidx.test.uiautomator.UiDevice
import dev.servercontrolpanel.data.videocall.PeerInfo
import dev.servercontrolpanel.data.videocall.RoomInfo
import dev.servercontrolpanel.data.videocall.SignalingMessage
import dev.servercontrolpanel.data.videocall.TurnCredentials
import dev.servercontrolpanel.data.videocall.VideoCallSessionController
import dev.servercontrolpanel.data.videocall.VideocallSignaling
import dev.servercontrolpanel.feature.videocall.CallPermissionChecker
import dev.servercontrolpanel.feature.videocall.CallViewModel
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
private const val ROOM_ID = "room-bg"

/** Fake signaling with no socket; duplicated because androidTest cannot see the `test` source set. */
private class FakeSignaling : VideocallSignaling {
    private val inbound = MutableSharedFlow<SignalingMessage>(extraBufferCapacity = 16)

    override fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage> = inbound.asSharedFlow()

    override fun send(msg: SignalingMessage): Boolean = true

    override fun close() = Unit

    fun push(message: SignalingMessage) = check(inbound.tryEmit(message))
}

/**
 * Records teardown calls. A real peer connection needs a second client, but this proves that
 * backgrounding or screen-off never reaches [dispose] or [closePeerConnectionFor].
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
        dev.servercontrolpanel.data.videocall.JoinResponse(
            peerId = "self-1",
            room = RoomInfo(id = ROOM_ID, name = "Room BG", owner = "admin", members = emptyList(), createdAt = 0L),
            peers = emptyList<PeerInfo>(),
            turn = TurnCredentials(urls = listOf("turn:example.org"), username = "u", credential = "c", ttl = 60L),
            politenessSeed = "self-1",
        ),
    ),
)

/**
 * A joined call must keep [CallForegroundService] alive when the app is backgrounded or the
 * screen turns off.
 *
 * This library module has no Activity, so backgrounding is driven with [UiDevice.pressHome].
 * `ACTION_SCREEN_OFF` is a protected broadcast apps cannot send, so screen-off uses
 * [UiDevice.sleep] and [UiDevice.wakeUp].
 */
@RunWith(AndroidJUnit4::class)
@OptIn(ExperimentalCoroutinesApi::class)
class BackgroundedCallSurvivesTest {

    /**
     * The camera and microphone service types require CAMERA and RECORD_AUDIO before
     * `startForeground()`, or it throws `SecurityException`; production checks the same thing.
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
        // Never leave the real service running for later tests.
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

        // onStartCommand() runs asynchronously, so poll.
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
