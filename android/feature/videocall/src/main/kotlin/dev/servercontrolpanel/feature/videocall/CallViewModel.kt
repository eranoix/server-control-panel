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

fun interface CallPermissionChecker {
    fun missingPermissions(): List<String>
}

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

private object NoopCallForegroundServiceController : CallForegroundServiceController {
    override fun start() = Unit
    override fun stop() = Unit
}

sealed interface CallUiState {
    data object Loading : CallUiState
    data class PermissionRequired(val missing: List<String>) : CallUiState
    data class Error(val message: String) : CallUiState
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

class CallViewModel(
    private val permissionChecker: CallPermissionChecker,
    private val sessionController: VideoCallSessionController,
    private val signaling: VideocallSignaling,
    private val localClientId: String = UUID.randomUUID().toString(),
    private val foregroundService: CallForegroundServiceController = NoopCallForegroundServiceController,
) : ViewModel() {

    private val _uiState = MutableStateFlow<CallUiState>(CallUiState.Loading)
    val uiState: StateFlow<CallUiState> = _uiState.asStateFlow()

    val eglBaseContext: org.webrtc.EglBase.Context get() = sessionController.eglBaseContext

    private var ownPeerId: String? = null
    private var turnCredentials: TurnCredentials? = null

    private val peerConnections = mutableMapOf<String, PeerConnection>()

    private val connectionIdToClientKey = mutableMapOf<String, String>()

    private val clientKeyToConnectionId = mutableMapOf<String, String>()

    private val remoteTracks = mutableMapOf<String, VideoTrack?>()

    private var micEnabled = true
    private var cameraEnabled = true

    fun openLobby() {
        val missing = permissionChecker.missingPermissions()
        if (missing.isNotEmpty()) {
            _uiState.value = CallUiState.PermissionRequired(missing)
            return
        }
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
            noCamera = trail == null,
        )
    }

    fun joinRoom(roomId: String) {
        val missing = permissionChecker.missingPermissions()
        if (missing.isNotEmpty()) {
            _uiState.value = CallUiState.PermissionRequired(missing)
            return
        }
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
            else -> Unit
        }
    }

    private fun handleJoined(message: SignalingMessage) {
        val joinResponse = decodePayload<JoinResponse>(message.payload) ?: return
        ownPeerId = joinResponse.peerId
        turnCredentials = joinResponse.turn
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

    fun onLeave() {
        signaling.close()
        sessionController.dispose()
        foregroundService.stop()
        peerConnections.clear()
        connectionIdToClientKey.clear()
        clientKeyToConnectionId.clear()
        remoteTracks.clear()
    }

    override fun onCleared() {
        onLeave()
    }
}

private object NoopSdpObserver : SdpObserver {
    override fun onCreateSuccess(sessionDescription: SessionDescription?) = Unit
    override fun onSetSuccess() = Unit
    override fun onCreateFailure(error: String?) = Unit
    override fun onSetFailure(error: String?) = Unit
}

fun createCallViewModel(context: Context): CallViewModel {
    val appContext = context.applicationContext
    return CallViewModel(
        permissionChecker = AndroidCallPermissionChecker(appContext),
        sessionController = WebRtcSessionManager(appContext),
        signaling = VideocallSignalingClient(VideocallSignalingRepository(), resolveVideocallWsBaseUrl().orEmpty()),
        foregroundService = AndroidCallForegroundServiceController(appContext),
    )
}
