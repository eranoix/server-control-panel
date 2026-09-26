package dev.servercontrolpanel.feature.videocall

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.videocall.IceCandidatePayload
import dev.servercontrolpanel.data.videocall.JoinResponse
import dev.servercontrolpanel.data.videocall.PeerInfo
import dev.servercontrolpanel.data.videocall.SdpPayload
import dev.servercontrolpanel.data.videocall.SignalingMessage
import dev.servercontrolpanel.data.videocall.TurnCredentials
import dev.servercontrolpanel.data.videocall.VideoCallSessionController
import dev.servercontrolpanel.data.videocall.VideocallSignaling
import dev.servercontrolpanel.data.videocall.VideocallSignalingClient
import dev.servercontrolpanel.data.videocall.VideocallSignalingRepository
import dev.servercontrolpanel.data.videocall.WebRtcSessionManager
import dev.servercontrolpanel.data.videocall.isPolite
import dev.servercontrolpanel.data.videocall.resolveVideocallWsBaseUrl
import dev.servercontrolpanel.feature.videocall.call.AndroidCallForegroundServiceController
import dev.servercontrolpanel.feature.videocall.call.CallForegroundServiceController
import java.util.UUID
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.encodeToJsonElement
import org.webrtc.IceCandidate
import org.webrtc.MediaConstraints
import org.webrtc.MediaStream
import org.webrtc.PeerConnection
import org.webrtc.RtpReceiver
import org.webrtc.SdpObserver
import org.webrtc.SessionDescription
import org.webrtc.VideoTrack

/**
 * Reports the runtime permissions [CallViewModel] needs before touching camera, mic or signaling.
 * An interface so JVM tests can inject a fake.
 */
fun interface CallPermissionChecker {
    /** The RECORD_AUDIO/CAMERA permissions still missing; empty when both are granted. */
    fun missingPermissions(): List<String>
}

/** Production [CallPermissionChecker] reading the real runtime grant state. */
class AndroidCallPermissionChecker(context: Context) : CallPermissionChecker {
    private val appContext = context.applicationContext

    override fun missingPermissions(): List<String> =
        REQUIRED_PERMISSIONS.filter {
            ContextCompat.checkSelfPermission(appContext, it) != PackageManager.PERMISSION_GRANTED
        }

    companion object {
        val REQUIRED_PERMISSIONS = listOf(Manifest.permission.RECORD_AUDIO, Manifest.permission.CAMERA)
    }
}

/** Default no-op controller; only [createCallViewModel] wires the real [AndroidCallForegroundServiceController]. */
private object NoopCallForegroundServiceController : CallForegroundServiceController {
    override fun start() = Unit
    override fun stop() = Unit
}

/**
 * Call screen state. [InCall.remoteTracks] is keyed by the peer's stable `client_id` (or its
 * connection id when absent), because a reconnecting peer gets a new connection id and keying by
 * it would leave a ghost tile. Values are nullable so a tile can show "connecting" before
 * `onTrack` fires.
 */
sealed interface CallUiState {
    data object Loading : CallUiState
    data class PermissionRequired(val missing: List<String>) : CallUiState
    data class Error(val message: String) : CallUiState
    /**
     * Pre-join lobby: local camera is live, signaling is not connected yet. Joining is public
     * and irreversible, so mic and camera can be adjusted first.
     */
    data class Lobby(
        val localTrack: VideoTrack?,
        val micEnabled: Boolean,
        val cameraEnabled: Boolean,
        val noCamera: Boolean,
    ) : CallUiState

    data class InCall(
        val localTrack: VideoTrack?,
        val remoteTracks: Map<String, VideoTrack?>,
        val micEnabled: Boolean,
        val cameraEnabled: Boolean,
    ) : CallUiState
}

private val payloadJson = Json { ignoreUnknownKeys = true }

private inline fun <reified T> decodePayload(payload: JsonElement?): T? {
    if (payload == null) return null
    return try {
        payloadJson.decodeFromJsonElement<T>(payload)
    } catch (_: Exception) {
        null
    }
}

/**
 * Drives one call's signaling and WebRTC session: permission gate, join, peer bookkeeping with
 * reconnect dedup by `client_id`, perfect-negotiation offer/answer/ICE, and the call controls.
 *
 * Takes no Android [Context], so it runs on the JVM with fakes; [createCallViewModel] wires the
 * real dependencies. Real SDP negotiation needs native WebRTC and is only verified on a device.
 */
class CallViewModel(
    private val permissionChecker: CallPermissionChecker,
    private val sessionController: VideoCallSessionController,
    private val signaling: VideocallSignaling,
    private val localClientId: String = UUID.randomUUID().toString(),
    private val foregroundService: CallForegroundServiceController = NoopCallForegroundServiceController,
) : ViewModel() {

    private val _uiState = MutableStateFlow<CallUiState>(CallUiState.Loading)
    val uiState: StateFlow<CallUiState> = _uiState.asStateFlow()

    /** The single EGL context every [VideoTile] must share. */
    val eglBaseContext: org.webrtc.EglBase.Context get() = sessionController.eglBaseContext

    private var ownPeerId: String? = null
    private var turnCredentials: TurnCredentials? = null

    /** Connection id to PeerConnection, one per live `peer-joined`. */
    private val peerConnections = mutableMapOf<String, PeerConnection>()

    /** Connection id to stable client key (`client_id`, or the connection id when absent). */
    private val connectionIdToClientKey = mutableMapOf<String, String>()

    /** Client key to its currently live connection id (reconnect dedup). */
    private val clientKeyToConnectionId = mutableMapOf<String, String>()

    /** Client key to remote video track, `null` until `onTrack` fires. */
    private val remoteTracks = mutableMapOf<String, VideoTrack?>()

    private var micEnabled = true
    private var cameraEnabled = true

    /**
     * Opens the lobby: checks permissions first (a denial touches neither media nor network),
     * then starts local camera and mic without connecting signaling.
     *
     * The foreground service deliberately does not start here: there is no call yet, and leaving
     * the app should turn the camera off.
     */
    fun openLobby() {
        val missing = permissionChecker.missingPermissions()
        if (missing.isNotEmpty()) {
            _uiState.value = CallUiState.PermissionRequired(missing)
            return
        }
        // startLocalMedia can throw (native library, no camera, camera in use); a failure must
        // degrade to an audio call, never crash.
        val started = runCatching { sessionController.startLocalMedia() }
        val trail = if (started.isSuccess) {
            runCatching { sessionController.localVideoTrack }.getOrNull()
        } else {
            null
        }
        _uiState.value = CallUiState.Lobby(
            localTrack = trail,
            micEnabled = micEnabled,
            cameraEnabled = cameraEnabled,
            // No video track means the camera failed; the lobby still allows joining with audio.
            noCamera = trail == null,
        )
    }

    fun joinRoom(roomId: String) {
        val missing = permissionChecker.missingPermissions()
        if (missing.isNotEmpty()) {
            _uiState.value = CallUiState.PermissionRequired(missing)
            return
        }
        // Show Loading immediately so the join tap gets feedback before the server answers.
        _uiState.value = CallUiState.Loading
        viewModelScope.launch {
            signaling.connect(roomId = roomId, clientId = localClientId, resume = false).collect { message ->
                handleMessage(message)
            }
        }
    }

    private fun handleMessage(message: SignalingMessage) {
        when (message.type) {
            "joined" -> handleJoined(message)
            "peer-joined" -> handlePeerJoined(message)
            "peer-left" -> handlePeerLeft(message)
            "offer" -> handleOffer(message)
            "answer" -> handleAnswer(message)
            "ice" -> handleIce(message)
            "error" -> _uiState.value = CallUiState.Error(message.error ?: "Unknown video call error.")
            // "leave", "chat", "state" and "ping" are ignored.
            else -> Unit
        }
    }

    private fun handleJoined(message: SignalingMessage) {
        val joinResponse = decodePayload<JoinResponse>(message.payload) ?: return
        ownPeerId = joinResponse.peerId
        turnCredentials = joinResponse.turn
        // Calls joined from the lobby skip PanelConnection.onAnswer, so the foreground service that
        // keeps camera and mic alive in the background is started here.
        foregroundService.start()
        sessionController.startLocalMedia()
        joinResponse.peers.forEach { peer -> registerPeer(connectionId = peer.id, peerInfo = peer) }
        pushInCallState()
    }

    private fun handlePeerJoined(message: SignalingMessage) {
        val connectionId = message.from ?: return
        val peerInfo = decodePayload<PeerInfo>(message.payload) ?: PeerInfo(id = connectionId, user = connectionId)
        registerPeer(connectionId, peerInfo)
        pushInCallState()
    }

    /**
     * `peer-left` carries only the connection id. The tile is removed only if that connection is
     * still current for its client, so a late `peer-left` after a reconnect keeps the new tile.
     */
    private fun handlePeerLeft(message: SignalingMessage) {
        val connectionId = message.from ?: return
        closePeerConnection(connectionId)
        val clientKey = connectionIdToClientKey.remove(connectionId) ?: return
        if (clientKeyToConnectionId[clientKey] == connectionId) {
            clientKeyToConnectionId.remove(clientKey)
            remoteTracks.remove(clientKey)
        }
        pushInCallState()
    }

    private fun handleOffer(message: SignalingMessage) {
        val connectionId = message.from ?: return
        val sdp = decodePayload<SdpPayload>(message.payload) ?: return
        val connection = peerConnections[connectionId] ?: return
        connection.setRemoteDescription(
            object : SdpObserver by NoopSdpObserver {
                override fun onSetSuccess() {
                    connection.createAnswer(
                        object : SdpObserver by NoopSdpObserver {
                            override fun onCreateSuccess(description: SessionDescription) {
                                connection.setLocalDescription(NoopSdpObserver, description)
                                signaling.send(
                                    SignalingMessage(
                                        type = "answer",
                                        to = connectionId,
                                        payload = payloadJson.encodeToJsonElement(
                                            SdpPayload.serializer(),
                                            SdpPayload(type = description.type.canonicalForm(), sdp = description.description),
                                        ),
                                    ),
                                )
                            }
                        },
                        MediaConstraints(),
                    )
                }
            },
            SessionDescription(SessionDescription.Type.fromCanonicalForm(sdp.type), sdp.sdp),
        )
    }

    private fun handleAnswer(message: SignalingMessage) {
        val connectionId = message.from ?: return
        val sdp = decodePayload<SdpPayload>(message.payload) ?: return
        val connection = peerConnections[connectionId] ?: return
        connection.setRemoteDescription(
            NoopSdpObserver,
            SessionDescription(SessionDescription.Type.fromCanonicalForm(sdp.type), sdp.sdp),
        )
    }

    private fun handleIce(message: SignalingMessage) {
        val connectionId = message.from ?: return
        val ice = decodePayload<IceCandidatePayload>(message.payload) ?: return
        val connection = peerConnections[connectionId] ?: return
        connection.addIceCandidate(IceCandidate(ice.sdpMid.orEmpty(), ice.sdpMLineIndex ?: 0, ice.candidate))
    }

    /**
     * Creates or replaces the [PeerConnection] for [connectionId], evicting the stale connection of
     * the same client. With a fake controller no connection is created, but the tile is still added.
     */
    private fun registerPeer(connectionId: String, peerInfo: PeerInfo) {
        val clientKey = peerInfo.clientId ?: peerInfo.id
        val staleConnectionId = clientKeyToConnectionId[clientKey]
        if (staleConnectionId != null && staleConnectionId != connectionId) {
            closePeerConnection(staleConnectionId)
            connectionIdToClientKey.remove(staleConnectionId)
        }
        connectionIdToClientKey[connectionId] = clientKey
        clientKeyToConnectionId[clientKey] = connectionId
        if (clientKey !in remoteTracks) remoteTracks[clientKey] = null

        val localPeerId = ownPeerId ?: return
        val turn = turnCredentials ?: return
        val observer = RemotePeerObserver(connectionId, clientKey, localPeerId, peerInfo.id)
        val connection = sessionController.createPeerConnectionFor(connectionId, turn, observer) ?: return
        peerConnections[connectionId] = connection
        sessionController.localVideoTrack?.let { connection.addTrack(it) }
        sessionController.localAudioTrack?.let { connection.addTrack(it) }
    }

    private fun closePeerConnection(connectionId: String) {
        peerConnections.remove(connectionId)
        sessionController.closePeerConnectionFor(connectionId)
    }

    /** Perfect negotiation for one remote peer: only the impolite side sends offers on renegotiation. */
    private inner class RemotePeerObserver(
        private val connectionId: String,
        private val clientKey: String,
        private val localPeerId: String,
        private val remotePeerId: String,
    ) : PeerConnection.Observer {
        override fun onSignalingChange(newState: PeerConnection.SignalingState?) = Unit
        override fun onIceConnectionChange(newState: PeerConnection.IceConnectionState?) = Unit
        override fun onIceConnectionReceivingChange(receiving: Boolean) = Unit
        override fun onIceGatheringChange(newState: PeerConnection.IceGatheringState?) = Unit
        override fun onIceCandidatesRemoved(candidates: Array<out IceCandidate>?) = Unit
        override fun onAddStream(stream: MediaStream?) = Unit
        override fun onRemoveStream(stream: MediaStream?) = Unit
        override fun onDataChannel(channel: org.webrtc.DataChannel?) = Unit

        override fun onIceCandidate(candidate: IceCandidate) {
            signaling.send(
                SignalingMessage(
                    type = "ice",
                    to = connectionId,
                    payload = payloadJson.encodeToJsonElement(
                        IceCandidatePayload.serializer(),
                        IceCandidatePayload(candidate.sdp, candidate.sdpMid, candidate.sdpMLineIndex),
                    ),
                ),
            )
        }

        override fun onRenegotiationNeeded() {
            if (isPolite(localPeerId, remotePeerId)) return
            val connection = peerConnections[connectionId] ?: return
            connection.createOffer(
                object : SdpObserver by NoopSdpObserver {
                    override fun onCreateSuccess(description: SessionDescription) {
                        connection.setLocalDescription(NoopSdpObserver, description)
                        signaling.send(
                            SignalingMessage(
                                type = "offer",
                                to = connectionId,
                                payload = payloadJson.encodeToJsonElement(
                                    SdpPayload.serializer(),
                                    SdpPayload(type = description.type.canonicalForm(), sdp = description.description),
                                ),
                            ),
                        )
                    }
                },
                MediaConstraints(),
            )
        }

        override fun onTrack(transceiver: org.webrtc.RtpTransceiver) {
            val track = transceiver.receiver.track()
            if (track is VideoTrack) {
                remoteTracks[clientKey] = track
                pushInCallState()
            }
        }

        override fun onAddTrack(receiver: RtpReceiver?, streams: Array<out MediaStream>?) = Unit
    }

    /** Re-emits the lobby state after a control changes, so the button reflects it. No-op outside the lobby. */
    private fun repaintLobby() {
        val current = _uiState.value as? CallUiState.Lobby ?: return
        _uiState.value = current.copy(
            localTrack = sessionController.localVideoTrack,
            micEnabled = micEnabled,
            cameraEnabled = cameraEnabled,
        )
    }

    private fun pushInCallState() {
        _uiState.value = CallUiState.InCall(
            localTrack = sessionController.localVideoTrack,
            remoteTracks = remoteTracks.toMap(),
            micEnabled = micEnabled,
            cameraEnabled = cameraEnabled,
        )
    }

    /** Toggles the mic, updating the real track and the UI state together. */
    fun onToggleMic() {
        if (_uiState.value !is CallUiState.InCall) return
        micEnabled = !micEnabled
        sessionController.setMicEnabled(micEnabled)
        repaintLobby()
        pushInCallState()
    }

    fun onToggleCamera() {
        if (_uiState.value !is CallUiState.InCall) return
        cameraEnabled = !cameraEnabled
        sessionController.setCameraEnabled(cameraEnabled)
        repaintLobby()
        pushInCallState()
    }

    fun onSwitchCamera() {
        if (_uiState.value !is CallUiState.InCall) return
        sessionController.switchCamera()
    }

    /** Ends the call: closes signaling first (sends `leave`), then tears down peers and capture. */
    fun onLeave() {
        signaling.close()
        sessionController.dispose()
        // Idempotent, so safe if PanelConnection.teardown() already stopped it.
        foregroundService.stop()
        peerConnections.clear()
        connectionIdToClientKey.clear()
        clientKeyToConnectionId.clear()
        remoteTracks.clear()
    }

    /**
     * Runs only when the call screen leaves the back stack or the process dies, not on
     * backgrounding; the foreground service keeps capture alive in the background.
     */
    override fun onCleared() {
        onLeave()
    }
}

/** No-op [SdpObserver], used directly and as a delegate base. */
private object NoopSdpObserver : SdpObserver {
    override fun onCreateSuccess(sessionDescription: SessionDescription?) = Unit
    override fun onSetSuccess() = Unit
    override fun onCreateFailure(error: String?) = Unit
    override fun onSetFailure(error: String?) = Unit
}

/** Builds the production [CallViewModel] with the real WebRTC, permission and service dependencies. */
fun createCallViewModel(context: Context): CallViewModel {
    val appContext = context.applicationContext
    return CallViewModel(
        permissionChecker = AndroidCallPermissionChecker(appContext),
        sessionController = WebRtcSessionManager(appContext),
        signaling = VideocallSignalingClient(VideocallSignalingRepository(), resolveVideocallWsBaseUrl().orEmpty()),
        foregroundService = AndroidCallForegroundServiceController(appContext),
    )
}
