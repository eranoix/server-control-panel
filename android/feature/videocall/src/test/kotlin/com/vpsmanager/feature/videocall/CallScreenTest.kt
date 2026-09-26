package com.vpsmanager.feature.videocall

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.vpsmanager.data.videocall.JoinResponse
import com.vpsmanager.data.videocall.PeerInfo
import com.vpsmanager.data.videocall.RoomInfo
import com.vpsmanager.data.videocall.SignalingMessage
import com.vpsmanager.data.videocall.TurnCredentials
import com.vpsmanager.data.videocall.VideoCallSessionController
import com.vpsmanager.data.videocall.VideocallSignaling
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.encodeToJsonElement
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.webrtc.EglBase
import org.webrtc.PeerConnection
import org.webrtc.VideoTrack

private val testJson = Json { ignoreUnknownKeys = true }
private inline fun <reified T> jsonPayload(value: T): JsonElement = testJson.encodeToJsonElement(value)
private const val ROOM_ID = "sala-1"

/**
 * Renders [CallScreen] under Robolectric across every reachable [CallUiState] -- never composed
 * before this. [CallUiState.InCall] stays safely renderable because [FakeSessionController] never
 * produces a real [VideoTrack] -- [VideoTile] falls back to its no-native-call placeholder
 * whenever `track == null` (see `VideoRenderer.kt`'s doc comment), so this never touches the real
 * WebRTC/SurfaceViewRenderer pipeline. Real SDP negotiation and rendered video frames need a real
 * device -- same boundary [CallViewModelTest]'s own doc comment already draws.
 */
@RunWith(RobolectricTestRunner::class)
class CallScreenTest {

    @get:Rule
    val composeRule = createComposeRule()

    private class FakeSignaling : VideocallSignaling {
        private val inbound = MutableSharedFlow<SignalingMessage>(extraBufferCapacity = 16)
        override fun connect(roomId: String, clientId: String, resume: Boolean): Flow<SignalingMessage> =
            inbound.asSharedFlow()
        override fun send(msg: SignalingMessage): Boolean = true
        override fun close() {}
        fun push(message: SignalingMessage) = check(inbound.tryEmit(message))
    }

    /**
     * [throwOnStartMedia] reproduces the REAL case seen on the device, not a
     * test exaggeration: `startLocalMedia` loads a native library, enumerates
     * cameras (and calls `error(...)` when there is none) and opens the
     * capture (which throws if another app already holds the camera). This is
     * the path along which the app crashed on the call screen.
     */
    private class FakeSessionController(
        private val throwOnStartMedia: Boolean = false,
    ) : VideoCallSessionController {
        override val eglBaseContext: EglBase.Context = object : EglBase.Context {
            override fun getNativeEglContext(): Long = 0L
        }
        override var localVideoTrack: VideoTrack? = null
        override var localAudioTrack: org.webrtc.AudioTrack? = null
        override fun startLocalMedia() {
            if (throwOnStartMedia) error("camera ocupada por outro app")
        }
        override fun createPeerConnectionFor(peerId: String, turn: TurnCredentials, observer: PeerConnection.Observer): PeerConnection? = null
        override fun closePeerConnectionFor(peerId: String) {}
        override fun setMicEnabled(enabled: Boolean) {}
        override fun setCameraEnabled(enabled: Boolean) {}
        override fun switchCamera() {}
        override fun dispose() {}
    }

    private fun joinedMessage() = SignalingMessage(
        type = "joined",
        payload = jsonPayload(
            JoinResponse(
                peerId = "self-1",
                room = RoomInfo(id = ROOM_ID, name = "Sala 1", owner = "admin", members = emptyList(), createdAt = 0L),
                peers = emptyList(),
                turn = TurnCredentials(urls = listOf("turn:example.org"), username = "u", credential = "c", ttl = 60L),
                politenessSeed = "self-1",
            ),
        ),
    )

    /**
     * THE FLOW CHANGED: the screen opens on the LOBBY, not already joining.
     *
     * Joining a call is the only action in this app that is public and
     * irreversible — by the time the person finds out they were muted, the
     * others have already seen. This test pins the lobby coming first; the
     * spinner only appears after they DECIDE to join.
     */
    /**
     * THE CALL SCREEN CRASH — pinned so it cannot come back.
     *
     * The app died with `UnsatisfiedLinkError` when opening a call. The root
     * cause was a different and older one (`PeerConnectionFactory.initialize`
     * was never called anywhere in the app, so the video call had been broken
     * ever since it was written), but what turned a latent defect into a crash
     * was the lobby starting the media WITHOUT any protection.
     *
     * This test pins the half that is this layer's responsibility: starting
     * the media may fail, and failing has to turn into an audio call — never
     * into an app that closes.
     */
    @Test
    fun `midia que lanca NAO derruba a tela — vira antessala sem camera`() {
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(throwOnStartMedia = true),
            signaling = FakeSignaling(),
        )
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = {}, viewModel = viewModel) }
        composeRule.waitForIdle()

        composeRule.onNodeWithText("Before joining").assertExists()
        composeRule.onNodeWithText("Join with audio only").assertExists()
    }

    @Test
    fun `a tela abre na antessala, nao entrando direto na chamada`() {
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = FakeSignaling(),
        )
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = {}, viewModel = viewModel) }

        composeRule.onNodeWithText("Before joining").assertExists()
        composeRule.onNodeWithText("Joining the call…").assertDoesNotExist()
    }

    @Test
    fun `depois de tocar entrar, o spinner aparece enquanto o sinal nao responde`() {
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = FakeSignaling(),
        )
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = {}, viewModel = viewModel) }

        composeRule.enterThroughLobby()

        composeRule.onNodeWithText("Joining the call…").assertExists()
    }

    /**
     * The camera being held by another app is the common case, not the
     * exception. The lobby has to DEGRADE to audio, never block: an audio call
     * is still the call, and refusing entry because of the camera trades a
     * feature for an obstacle.
     */
    @Test
    fun `sem camera a antessala oferece entrar so com audio, e nao bloqueia`() {
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            // localVideoTrack null = the camera did not open.
            sessionController = FakeSessionController(),
            signaling = FakeSignaling(),
        )
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = {}, viewModel = viewModel) }

        composeRule.onNodeWithText("No camera image.\nAnother app may be using it.").assertExists()
        composeRule.onNodeWithText("Join with audio only").assertExists()
    }

    @Test
    fun `missing permissions surface the grant-access card instead of joining`() {
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { listOf(android.Manifest.permission.CAMERA) },
            sessionController = FakeSessionController(),
            signaling = FakeSignaling(),
        )
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = {}, viewModel = viewModel) }

        composeRule.onNodeWithText("Permissions required").assertExists()
    }

    @Test
    fun `an in-call state renders the control bar and hang up leaves the call`() {
        val signaling = FakeSignaling()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = signaling,
        )
        var left = false
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = { left = true }, viewModel = viewModel) }
        composeRule.enterThroughLobby()
        signaling.push(joinedMessage())
        composeRule.waitForIdle()

        composeRule.onNodeWithContentDescription("Mute microphone").assertExists()
        composeRule.onNodeWithContentDescription("Turn off camera").assertExists()
        composeRule.onNodeWithContentDescription("End call").performClick()
        assert(left) { "expected onLeaveCall to fire when hang-up is pressed" }
    }

    @Test
    fun `a signaling error surfaces the reason with a way back out`() {
        val signaling = FakeSignaling()
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = signaling,
        )
        var left = false
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = { left = true }, viewModel = viewModel) }
        composeRule.enterThroughLobby()
        signaling.push(SignalingMessage(type = "error", error = "A sala foi encerrada pelo administrador."))
        composeRule.waitForIdle()

        composeRule.onNodeWithText("A sala foi encerrada pelo administrador.").assertExists()
        composeRule.onNodeWithText("Leave").performClick()
        assert(left)
    }
}

/**
 * Leaves the lobby and joins the call.
 *
 * In a single place because the button's label CHANGES with the state of the
 * camera ("Entrar na chamada" with video, "Entrar so com audio" without) — and
 * in the tests FakeSessionController has no video track, so the label is
 * always the second one. Spreading that subtlety over four tests would be four
 * places to get wrong when the label changes.
 */
private fun androidx.compose.ui.test.junit4.ComposeContentTestRule.enterThroughLobby() {
    waitForIdle()
    onNodeWithText("Join with audio only").performClick()
    waitForIdle()
}
