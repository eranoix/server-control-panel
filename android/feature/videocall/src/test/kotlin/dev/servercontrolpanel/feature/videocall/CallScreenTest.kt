package dev.servercontrolpanel.feature.videocall

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import dev.servercontrolpanel.data.videocall.JoinResponse
import dev.servercontrolpanel.data.videocall.PeerInfo
import dev.servercontrolpanel.data.videocall.RoomInfo
import dev.servercontrolpanel.data.videocall.SignalingMessage
import dev.servercontrolpanel.data.videocall.TurnCredentials
import dev.servercontrolpanel.data.videocall.VideoCallSessionController
import dev.servercontrolpanel.data.videocall.VideocallSignaling
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
private const val ROOM_ID = "room-1"

/**
 * Renders [CallScreen] under Robolectric across every reachable [CallUiState]. The fake
 * controller never yields a real [VideoTrack], so [VideoTile] shows its placeholder and no
 * real WebRTC rendering runs; SDP and video frames need a real device.
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
     * [throwOnStartMedia] mirrors real devices: `startLocalMedia` throws when there is no
     * camera, the native library fails, or another app holds the camera.
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
            if (throwOnStartMedia) error("camera in use by another app")
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
                room = RoomInfo(id = ROOM_ID, name = "Room 1", owner = "admin", members = emptyList(), createdAt = 0L),
                peers = emptyList(),
                turn = TurnCredentials(urls = listOf("turn:example.org"), username = "u", credential = "c", ttl = 60L),
                politenessSeed = "self-1",
            ),
        ),
    )

    /** Starting media may throw; that must degrade to an audio-only lobby, never a crash. */
    @Test
    fun `media that throws does NOT crash the screen, it becomes a lobby without camera`() {
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

    /** Joining is public and irreversible, so the screen opens on the lobby first. */
    @Test
    fun `the screen opens on the lobby, not straight into the call`() {
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
    fun `after tapping join, the spinner shows until signalling responds`() {
        val viewModel = CallViewModel(
            permissionChecker = CallPermissionChecker { emptyList() },
            sessionController = FakeSessionController(),
            signaling = FakeSignaling(),
        )
        composeRule.setContent { CallScreen(roomId = ROOM_ID, onLeaveCall = {}, viewModel = viewModel) }

        composeRule.enterThroughLobby()

        composeRule.onNodeWithText("Joining the call…").assertExists()
    }

    /** A busy camera is common, so the lobby must degrade to audio instead of blocking. */
    @Test
    fun `without a camera the lobby offers audio only and does not block`() {
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
        signaling.push(SignalingMessage(type = "error", error = "The room was closed by the administrator."))
        composeRule.waitForIdle()

        composeRule.onNodeWithText("The room was closed by the administrator.").assertExists()
        composeRule.onNodeWithText("Leave").performClick()
        assert(left)
    }
}

/**
 * Leaves the lobby and joins the call. The join label depends on the camera state, and
 * the fake controller has no video track, so it is always the audio-only label.
 */
private fun androidx.compose.ui.test.junit4.ComposeContentTestRule.enterThroughLobby() {
    waitForIdle()
    onNodeWithText("Join with audio only").performClick()
    waitForIdle()
}
