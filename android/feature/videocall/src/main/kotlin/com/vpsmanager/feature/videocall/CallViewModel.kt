package com.vpsmanager.feature.videocall

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.videocall.IceCandidatePayload
import com.vpsmanager.data.videocall.JoinResponse
import com.vpsmanager.data.videocall.PeerInfo
import com.vpsmanager.data.videocall.SdpPayload
import com.vpsmanager.data.videocall.SignalingMessage
import com.vpsmanager.data.videocall.TurnCredentials
import com.vpsmanager.data.videocall.VideoCallSessionController
import com.vpsmanager.data.videocall.VideocallSignaling
import com.vpsmanager.data.videocall.VideocallSignalingClient
import com.vpsmanager.data.videocall.VideocallSignalingRepository
import com.vpsmanager.data.videocall.WebRtcSessionManager
import com.vpsmanager.data.videocall.isPolite
import com.vpsmanager.data.videocall.resolveVideocallWsBaseUrl
import com.vpsmanager.feature.videocall.call.AndroidCallForegroundServiceController
import com.vpsmanager.feature.videocall.call.CallForegroundServiceController
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
 * The runtime permissions [CallViewModel] requires before it will touch the camera, microphone,
 * or signaling connection at all. Kept as a narrow interface — mirroring
 * [com.vpsmanager.data.videocall.LocalMediaTrackControl]'s existing seam pattern — so
 * [CallViewModelTest] can inject a fake instead of exercising Android's real permission APIs on
 * the JVM.
 */
fun interface CallPermissionChecker {
    /** The RECORD_AUDIO/CAMERA permissions still missing; empty when both are granted. */
    fun missingPermissions(): List<String>
}

/** The only production [CallPermissionChecker]: reads real runtime grant state via [context]. */
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

/**
 * Default [CallForegroundServiceController] for every test that does not care about the
 * foreground service — keeps [CallViewModel]'s constructor call sites in [CallViewModelTest]
 * unchanged. Only [createCallViewModel] wires the real [AndroidCallForegroundServiceController].
 */
private object NoopCallForegroundServiceController : CallForegroundServiceController {
    override fun start() = Unit
    override fun stop() = Unit
}

/**
 * Mirrors `HomeViewModel`'s established `StateFlow<UiState>` shape — no new
 * state-management library introduced. [InCall.remoteTracks] is keyed by each peer's stable
 * `client_id` (falling back to their connection id when absent, see [PeerInfo]'s doc comment),
 * never by the raw signaling connection id alone — a reconnecting peer gets a fresh connection id
 * on every attach, so keying by connection id would show a ghost tile for the stale connection
 * until its `peer-left` arrives, alongside a second tile for the fresh `peer-joined`.
 *
 * [InCall.remoteTracks]'s value is nullable — deviating from the design's literal
 * `Map<String, VideoTrack>` — because a peer's tile must exist (and render a placeholder) from
 * the moment `peer-joined` arrives, before that peer's `onTrack` callback has fired; a non-null
 * map could only represent "tile exists with video" or "tile does not exist yet", not the
 * real intermediate "tile exists, still connecting" state the UI requirement describes.
 */
sealed interface CallUiState {
    data object Loading : CallUiState
    data class PermissionRequired(val missing: List<String>) : CallUiState
    data class Error(val message: String) : CallUiState
    /**
     * THE LOBBY: the camera is already live, the signaling is not yet.
     *
     * It exists because joining a call is the one action in this app that is
     * public and irreversible — by the time the person discovers they were
     * muted, or that the camera was pointing at the ceiling, the others have
     * already seen it. Adjusting beforehand costs seconds; adjusting
     * afterwards costs the impression.
     *
     * And it is the app's only "loading" that is CONTENT rather than waiting:
     * while the camera wakes up, the microphone can already be switched off.
     */
    data class Lobby(
        val localTrack: VideoTrack?,
        val micEnabled: Boolean,
        val cameraEnabled: Boolean,
        val semCamera: Boolean,
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
 * Drives one call's signaling + WebRTC session end to end: permission gate, `join`, remote-peer
 * bookkeeping (with `client_id`-based ghost-tile dedup on reconnect), full perfect-negotiation
 * offer/answer/ice handling, and the mic/camera/switch/hang-up intents the call UI requires.
 *
 * Every dependency is a required constructor parameter — no Android [Context] leaks into this
 * class at all, keeping it constructible on the plain JVM with hand-rolled fakes.
 * [createCallViewModel] is the one production call site that wires the real,
 * directly-constructed [WebRtcSessionManager]/[AndroidCallPermissionChecker], mirroring
 * `HomeViewModel`'s direct `SessionRepository()` construction.
 * [CallViewModelTest] constructs this class directly with fakes instead.
 *
 * Real SDP negotiation (`createOffer`/`createAnswer`/`setLocalDescription`/`setRemoteDescription`
 * and the resulting `onTrack`/`onIceCandidate` callbacks) requires the native WebRTC library and
 * is therefore not exercised by any test here — it is real production code, verified only by the
 * human-verification script on a real device (two peers must actually see/hear each
 * other), never claimed as unit-tested.
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

    /** The single EGL context every [VideoTile] `CallScreen` renders must share (see [VideoCallSessionController.eglBaseContext]'s doc comment). */
    val eglBaseContext: org.webrtc.EglBase.Context get() = sessionController.eglBaseContext

    private var ownPeerId: String? = null
    private var turnCredentials: TurnCredentials? = null

    /** Signaling connection-id -> PeerConnection, one per live `peer-joined` (not per client). */
    private val peerConnections = mutableMapOf<String, PeerConnection>()

    /** Signaling connection-id -> stable client key (`client_id` or, absent that, the connection id). */
    private val connectionIdToClientKey = mutableMapOf<String, String>()

    /** Client key -> the connection id CURRENTLY considered live for that client (reconnect dedup). */
    private val clientKeyToConnectionId = mutableMapOf<String, String>()

    /** Client key -> that peer's remote video track, `null` until `onTrack` fires. Rendered as [CallUiState.InCall.remoteTracks]. */
    private val remoteTracks = mutableMapOf<String, VideoTrack?>()

    private var micEnabled = true
    private var cameraEnabled = true

    /**
     * Checks RECORD_AUDIO/CAMERA BEFORE any capture or signaling call — a denial
     * reaches [CallUiState.PermissionRequired] having touched neither the camera/mic nor the
     * network, never as the result of a failed connect attempt.
     */
    /**
     * Opens the lobby: brings up the local camera and microphone WITHOUT
     * connecting the signaling.
     *
     * `startLocalMedia()` depends on nothing from the signaling — in the old
     * flow it was called right after `joined`, and the tracks only enter a
     * connection once a peer appears (`createPeerConnectionFor`). In other
     * words: bringing it forward gives a genuine preview without touching the
     * protocol.
     *
     * The foreground service does NOT start here, deliberately. It exists to
     * keep camera and microphone alive during a call; in the lobby there is
     * no call yet, and leaving the app really should switch the camera off.
     */
    fun abrirAntessala() {
        val missing = permissionChecker.missingPermissions()
        if (missing.isNotEmpty()) {
            _uiState.value = CallUiState.PermissionRequired(missing)
            return
        }
        // STARTING THE MEDIA CAN THROW, and throwing here takes the app down.
        //
        // `startLocalMedia()` does three things that fail in the real world:
        // it loads WebRTC's native library, enumerates cameras (and calls
        // `error(...)` when there is none) and opens the capture (which
        // throws if another app already holds the camera). I wrote "with no
        // camera the lobby degrades" and handled only the NULL TRACK case —
        // the EXCEPTION case was left out, and it was that one that brought
        // the app down on the call screen.
        //
        // Here the rule is the same one that was already written, now holding
        // for both paths: a camera that will not open becomes an audio call,
        // never an app that closes.
        val ligou = runCatching { sessionController.startLocalMedia() }
        val trilha = if (ligou.isSuccess) {
            runCatching { sessionController.localVideoTrack }.getOrNull()
        } else {
            null
        }
        _uiState.value = CallUiState.Lobby(
            localTrack = trilha,
            micEnabled = micEnabled,
            cameraEnabled = cameraEnabled,
            // With no video track, the camera failed (held by another app,
            // or the device has none, or the media never came up at all).
            // That does NOT block: the call degrades to audio only, which is
            // still the call. A lobby that refuses to let you in because the
            // camera would not open trades a feature for an obstacle.
            semCamera = trilha == null,
        )
    }

    fun joinRoom(roomId: String) {
        val missing = permissionChecker.missingPermissions()
        if (missing.isNotEmpty()) {
            _uiState.value = CallUiState.PermissionRequired(missing)
            return
        }
        // JOINING HAS TO GIVE FEEDBACK ON THE SPOT.
        //
        // Before the lobby this was unnecessary: the initial state was already
        // Loading and the screen opened with the spinner. With the lobby, and
        // without this line, the screen sat STILL on the lobby until the
        // server answered "joined" — the button accepted a tap and nothing
        // happened, which is how a button teaches itself to be tapped three
        // times. A test caught this.
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
            // "leave", "chat", "state", "ping": outside this plan's scope.
            else -> Unit
        }
    }

    private fun handleJoined(message: SignalingMessage) {
        val joinResponse = decodePayload<JoinResponse>(message.payload) ?: return
        ownPeerId = joinResponse.peerId
        turnCredentials = joinResponse.turn
        // A call joined from RoomLobbyScreen never goes through VpsmConnection.onAnswer (that
        // path is only for an incoming ring answered from the lock screen) — this is the only
        // place a call started this way starts the foreground service that keeps its camera/mic
        // alive across backgrounding.
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
     * `peer-left` carries only the leaving connection's id, never a `client_id` (the server
     * relays it payload-less — see `internal/videocall/signaling.go`). Only remove the tile when
     * [connectionId] is still the CURRENT connection for its client key: if a fresher
     * `peer-joined` for the same client already replaced it (the reconnect race), that newer
     * tile must survive this stale connection's belated `peer-left`.
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
     * Creates (or, on reconnect, replaces) the [PeerConnection] for [connectionId], evicting any
     * stale connection this same [peerInfo]'s `client_id` was previously mapped to. Adding local
     * tracks and wiring negotiation callbacks requires the real native factory; when
     * [sessionController] is a test fake this returns `null` and the tile is added without a
     * live connection, which is exactly what the plan's `peerJoinedAddsRemoteTile` test asserts.
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

    /** Perfect-negotiation glue for one remote peer: only the "impolite" side initiates offers on renegotiation. */
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

    /**
     * Re-emits the lobby's state when one of its buttons changes something.
     *
     * `pushInCallState` only knows how to paint [CallUiState.InCall]; without
     * this counterpart, the microphone button in the lobby would change the
     * audio and NOT change the drawing — a control that does not confirm what
     * it did is a control nobody trusts. A no-op outside the lobby, so the
     * call flow stays identical.
     */
    private fun repintarAntessala() {
        val atual = _uiState.value as? CallUiState.Lobby ?: return
        _uiState.value = atual.copy(
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

    /** Always reachable, independent of connection state — flips both UI state and the real track together. */
    fun onToggleMic() {
        if (_uiState.value !is CallUiState.InCall) return
        micEnabled = !micEnabled
        sessionController.setMicEnabled(micEnabled)
        repintarAntessala()
        pushInCallState()
    }

    fun onToggleCamera() {
        if (_uiState.value !is CallUiState.InCall) return
        cameraEnabled = !cameraEnabled
        sessionController.setCameraEnabled(cameraEnabled)
        repintarAntessala()
        pushInCallState()
    }

    fun onSwitchCamera() {
        if (_uiState.value !is CallUiState.InCall) return
        sessionController.switchCamera()
    }

    /** Ends the call: closes signaling first (sends `leave`), then tears down every peer connection and the capture pipeline. */
    fun onLeave() {
        signaling.close()
        sessionController.dispose()
        // Safe even if this call was answered via Telecom and VpsmConnection.teardown() already
        // stopped it — CallForegroundService.stop is documented idempotent for exactly this race.
        foregroundService.stop()
        peerConnections.clear()
        connectionIdToClientKey.clear()
        clientKeyToConnectionId.clear()
        remoteTracks.clear()
    }

    /**
     * Backgrounding the app does NOT clear this ViewModel or call [onLeave] — a ViewModel is
     * only cleared when its owning `NavBackStackEntry` leaves the back stack (the user backs out
     * of the call screen) or the process dies, not when `onStop()` fires on the host Activity. So
     * an ongoing call's signaling connection and peer connections keep running while backgrounded
     * from this class's point of view; whether the OS lets camera capture keep running while the
     * app is not in the foreground is a device/OS-version concern this plan does not address —
     * this module's own build.gradle.kts comment ("unica excecao com foreground service
     * persistente") already earmarks a persistent foreground service as the fix, which is not
     * part of this plan's scope and must land before background/lock-screen calling is claimed to
     * work.
     */
    override fun onCleared() {
        onLeave()
    }
}

/** A no-op [SdpObserver] used as the base for the anonymous overrides above, and directly wherever no callback is needed. */
private object NoopSdpObserver : SdpObserver {
    override fun onCreateSuccess(sessionDescription: SessionDescription?) = Unit
    override fun onSetSuccess() = Unit
    override fun onCreateFailure(error: String?) = Unit
    override fun onSetFailure(error: String?) = Unit
}

/**
 * Builds the real, production [CallViewModel] — directly constructing [WebRtcSessionManager] and
 * [AndroidCallPermissionChecker] from [context] (mirrors `HomeViewModel`'s direct
 * `SessionRepository()` construction; `WebRtcSessionManager(context)` is the deliberate
 * "no intermediate abstraction" call site). This is the only production caller of
 * [CallViewModel]'s constructor; `CallViewModelTest` constructs it directly with fakes instead.
 */
fun createCallViewModel(context: Context): CallViewModel {
    val appContext = context.applicationContext
    return CallViewModel(
        permissionChecker = AndroidCallPermissionChecker(appContext),
        sessionController = WebRtcSessionManager(appContext),
        signaling = VideocallSignalingClient(VideocallSignalingRepository(), resolveVideocallWsBaseUrl().orEmpty()),
        foregroundService = AndroidCallForegroundServiceController(appContext),
    )
}
