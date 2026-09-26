package dev.servercontrolpanel.feature.videocall

import dev.servercontrolpanel.data.videocall.PeerInfo
import dev.servercontrolpanel.data.videocall.RoomInfo
import dev.servercontrolpanel.data.videocall.SignalingMessage
import dev.servercontrolpanel.data.videocall.TurnCredentials
import dev.servercontrolpanel.data.videocall.VideocallSignaling
import dev.servercontrolpanel.data.videocall.VideoCallSessionController
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
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.webrtc.EglBase
import org.webrtc.PeerConnection
import org.webrtc.VideoTrack

private val testJson = Json { ignoreUnknownKeys = true }

private inline fun <reified T> jsonPayload(value: T): JsonElement = testJson.encodeToJsonElement(value)

private const val ROOM_ID = "room-1"

/** Scriptable signaling stream for [CallViewModel], with no real socket. */
private class FakeSignaling : VideocallSignaling {
    private val inbound = MutableSharedFlow<SignalingMessage>(extraBufferCapacity = 16)
    var connectCalls = 0
        private set
    var closeCalls = 0
        private set
    val sent = mutableListOf<SignalingMessage>()

    override fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage> {
        connectCalls += 1
        return inbound.asSharedFlow()
    }

    override fun send(msg: SignalingMessage): Boolean {
        sent += msg
        return true
    }

    override fun close() {
        closeCalls += 1
    }

    fun push(message: SignalingMessage) = check(inbound.tryEmit(message))
}

/**
 * Records calls without the native library; [createPeerConnectionFor] returns `null`, so tiles
 * are added to [CallUiState.InCall.remoteTracks] without a live connection.
 */
private class FakeSessionController : VideoCallSessionController {
    override val eglBaseContext: EglBase.Context = object : EglBase.Context {
        override fun getNativeEglContext(): Long = 0L
    }
    override var localVideoTrack: VideoTrack? = null
    override var localAudioTrack: org.webrtc.AudioTrack? = null

    var startLocalMediaCalls = 0
        private set
    val closedConnections = mutableListOf<String>()
    val micEnabledCalls = mutableListOf<Boolean>()
    val cameraEnabledCalls = mutableListOf<Boolean>()
    var switchCameraCalls = 0
        private set
    var disposeCalls = 0
        private set

    override fun startLocalMedia() {
        startLocalMediaCalls += 1
    }

    override fun createPeerConnectionFor(peerId: String, turn: TurnCredentials, observer: PeerConnection.Observer): PeerConnection? = null

    override fun closePeerConnectionFor(peerId: String) {
        closedConnections += peerId
    }

    override fun setMicEnabled(enabled: Boolean) {
        micEnabledCalls += enabled
    }

    override fun setCameraEnabled(enabled: Boolean) {
        cameraEnabledCalls += enabled
    }

    override fun switchCamera() {
        switchCameraCalls += 1
    }

    override fun dispose() {
        disposeCalls += 1
    }
}

/** Records [start]/[stop] calls instead of touching the real [dev.servercontrolpanel.feature.videocall.call.CallForegroundService]. */
private class FakeForegroundServiceController : dev.servercontrolpanel.feature.videocall.call.CallForegroundServiceController {
    var startCalls = 0
        private set
    var stopCalls = 0
        private set

    override fun start() {
        startCalls += 1
    }

    override fun stop() {
        stopCalls += 1
    }
}

private fun joinedMessage(peers: List<PeerInfo> = emptyList()) = SignalingMessage(
    type = "joined",
    payload = jsonPayload(
        dev.servercontrolpanel.data.videocall.JoinResponse(
            peerId = "self-1",
            room = RoomInfo(id = ROOM_ID, name = "Room 1", owner = "admin", members = emptyList(), createdAt = 0L),
            peers = peers,
            turn = TurnCredentials(urls = listOf("turn:example.org"), username = "u", credential = "c", ttl = 60L),
            politenessSeed = "self-1",
        ),
    ),
)

@OptIn(ExperimentalCoroutinesApi::class)
class CallViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(dispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    @Test
    fun startsInLoadingThenTransitionsToInCallOnJoined() = runTest {
        val signaling = FakeSignaling()
        val sessionController = FakeSessionController()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = sessionController,
            signaling = signaling,
        )

        assertEquals(CallUiState.Loading, viewModel.uiState.value)

        viewModel.joinRoom(ROOM_ID)
        dispatcher.scheduler.advanceUntilIdle()
        signaling.push(joinedMessage())
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value as CallUiState.InCall
        assertTrue(state.remoteTracks.isEmpty())
        assertEquals(1, sessionController.startLocalMediaCalls)
    }

    @Test
    fun peerJoinedAddsRemoteTile() = runTest {
        val signaling = FakeSignaling()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = signaling,
        )
        viewModel.joinRoom(ROOM_ID)
        dispatcher.scheduler.advanceUntilIdle()
        signaling.push(joinedMessage())
        dispatcher.scheduler.advanceUntilIdle()

        signaling.push(
            SignalingMessage(
                type = "peer-joined",
                from = "conn-2",
                payload = jsonPayload(PeerInfo(id = "conn-2", user = "Bob", clientId = "client-bob")),
            ),
        )
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value as CallUiState.InCall
        assertTrue(state.remoteTracks.containsKey("client-bob"))
    }

    @Test
    fun peerLeftRemovesRemoteTile() = runTest {
        val signaling = FakeSignaling()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = signaling,
        )
        viewModel.joinRoom(ROOM_ID)
        dispatcher.scheduler.advanceUntilIdle()
        signaling.push(joinedMessage())
        dispatcher.scheduler.advanceUntilIdle()
        signaling.push(
            SignalingMessage(
                type = "peer-joined",
                from = "conn-2",
                payload = jsonPayload(PeerInfo(id = "conn-2", user = "Bob", clientId = "client-bob")),
            ),
        )
        dispatcher.scheduler.advanceUntilIdle()

        signaling.push(SignalingMessage(type = "peer-left", from = "conn-2"))
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value as CallUiState.InCall
        assertFalse(state.remoteTracks.containsKey("client-bob"))
    }

    @Test
    fun micPermissionDeniedTransitionsToPermissionRequired() = runTest {
        val signaling = FakeSignaling()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { listOf(android.Manifest.permission.RECORD_AUDIO) },
            sessionController = FakeSessionController(),
            signaling = signaling,
        )

        viewModel.joinRoom(ROOM_ID)
        dispatcher.scheduler.advanceUntilIdle()

        val state = viewModel.uiState.value as CallUiState.PermissionRequired
        assertEquals(listOf(android.Manifest.permission.RECORD_AUDIO), state.missing)
        assertEquals(0, signaling.connectCalls)
    }

    @Test
    fun toggleMicUpdatesStateAndCallsSessionManager() = runTest {
        val signaling = FakeSignaling()
        val sessionController = FakeSessionController()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = sessionController,
            signaling = signaling,
        )
        viewModel.joinRoom(ROOM_ID)
        dispatcher.scheduler.advanceUntilIdle()
        signaling.push(joinedMessage())
        dispatcher.scheduler.advanceUntilIdle()

        val before = viewModel.uiState.value as CallUiState.InCall
        assertTrue(before.micEnabled)

        viewModel.onToggleMic()

        val after = viewModel.uiState.value as CallUiState.InCall
        assertFalse(after.micEnabled)
        assertEquals(listOf(false), sessionController.micEnabledCalls)
    }

    @Test
    fun joiningStartsForegroundServiceAndLeavingStopsIt() = runTest {
        val signaling = FakeSignaling()
        val foregroundService = FakeForegroundServiceController()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = signaling,
            foregroundService = foregroundService,
        )

        viewModel.joinRoom(ROOM_ID)
        dispatcher.scheduler.advanceUntilIdle()
        assertEquals(0, foregroundService.startCalls)

        signaling.push(joinedMessage())
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(1, foregroundService.startCalls)
        assertEquals(0, foregroundService.stopCalls)

        viewModel.onLeave()

        assertEquals(1, foregroundService.stopCalls)
    }
}
