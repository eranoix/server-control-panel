package dev.servercontrolpanel.data.videocall

import android.content.Context
import org.webrtc.AudioTrack
import org.webrtc.Camera2Enumerator
import org.webrtc.CameraVideoCapturer
import org.webrtc.DefaultVideoDecoderFactory
import org.webrtc.DefaultVideoEncoderFactory
import org.webrtc.EglBase
import org.webrtc.MediaConstraints
import org.webrtc.PeerConnection
import org.webrtc.PeerConnectionFactory
import org.webrtc.SurfaceTextureHelper
import org.webrtc.VideoTrack

private const val LOCAL_AUDIO_TRACK_ID = "panel-audio0"
private const val LOCAL_VIDEO_TRACK_ID = "panel-video0"
private const val CAPTURE_THREAD_NAME = "PanelVideocallCapture"
private const val CAPTURE_WIDTH = 1280
private const val CAPTURE_HEIGHT = 720
private const val CAPTURE_FPS = 30

fun isPolite(localPeerId: String, remotePeerId: String): Boolean = localPeerId < remotePeerId

fun buildIceServers(turn: TurnCredentials): List<PeerConnection.IceServer> = turn.urls.map { url ->
    PeerConnection.IceServer.builder(url)
        .setUsername(turn.username)
        .setPassword(turn.credential)
        .createIceServer()
}

interface LocalMediaTrackControl {
    var enabled: Boolean
}

private class AudioTrackControl(private val track: AudioTrack) : LocalMediaTrackControl {
    override var enabled: Boolean
        get() = track.enabled()
        set(value) {
            track.setEnabled(value)
        }
}

private class VideoTrackControl(private val track: VideoTrack) : LocalMediaTrackControl {
    override var enabled: Boolean
        get() = track.enabled()
        set(value) {
            track.setEnabled(value)
        }
}

interface VideoCallSessionController {
    val eglBaseContext: EglBase.Context

    val localVideoTrack: VideoTrack?

    val localAudioTrack: AudioTrack?

    fun startLocalMedia()
    fun createPeerConnectionFor(peerId: String, turn: TurnCredentials, observer: PeerConnection.Observer): PeerConnection?
    fun closePeerConnectionFor(peerId: String)
    fun setMicEnabled(enabled: Boolean)
    fun setCameraEnabled(enabled: Boolean)
    fun switchCamera()
    fun dispose()
}

class WebRtcSessionManager internal constructor(
    private val context: Context,
    private val eglBaseProvider: () -> EglBase,
    private val peerConnectionFactoryProvider: (EglBase) -> PeerConnectionFactory,
) : VideoCallSessionController {
    constructor(context: Context) : this(
        context = context.applicationContext,
        eglBaseProvider = { EglBase.create() },
        peerConnectionFactoryProvider = { eglBase ->
            ensureWebRtcLoaded(context.applicationContext)
            PeerConnectionFactory.builder()
                .setVideoEncoderFactory(DefaultVideoEncoderFactory(eglBase.eglBaseContext, true, true))
                .setVideoDecoderFactory(DefaultVideoDecoderFactory(eglBase.eglBaseContext))
                .createPeerConnectionFactory()
        },
    )

    private val eglBaseDelegate = lazy(eglBaseProvider)
    private val eglBase: EglBase by eglBaseDelegate
    private val peerConnectionFactory: PeerConnectionFactory by lazy { peerConnectionFactoryProvider(eglBase) }

    @Volatile
    internal var micControl: LocalMediaTrackControl? = null

    @Volatile
    internal var cameraControl: LocalMediaTrackControl? = null

    @Volatile
    internal var cameraCapturer: CameraVideoCapturer? = null

    private val peerConnections = mutableMapOf<String, PeerConnection>()

    @Volatile
    private var localVideoTrackField: VideoTrack? = null

    @Volatile
    private var localAudioTrackField: AudioTrack? = null

    override val eglBaseContext: EglBase.Context
        get() = eglBase.eglBaseContext

    override val localVideoTrack: VideoTrack?
        get() = localVideoTrackField

    override val localAudioTrack: AudioTrack?
        get() = localAudioTrackField

    override fun startLocalMedia() {
        val enumerator = Camera2Enumerator(context)
        val deviceName = enumerator.deviceNames.firstOrNull { enumerator.isFrontFacing(it) }
            ?: enumerator.deviceNames.firstOrNull()
            ?: error("No camera available on this device.")
        val capturer = enumerator.createCapturer(deviceName, null)
        cameraCapturer = capturer

        val surfaceTextureHelper = SurfaceTextureHelper.create(CAPTURE_THREAD_NAME, eglBase.eglBaseContext)
        val videoSource = peerConnectionFactory.createVideoSource(capturer.isScreencast)
        capturer.initialize(surfaceTextureHelper, context, videoSource.capturerObserver)
        capturer.startCapture(CAPTURE_WIDTH, CAPTURE_HEIGHT, CAPTURE_FPS)
        val videoTrack = peerConnectionFactory.createVideoTrack(LOCAL_VIDEO_TRACK_ID, videoSource)
        cameraControl = VideoTrackControl(videoTrack)
        localVideoTrackField = videoTrack

        val audioSource = peerConnectionFactory.createAudioSource(MediaConstraints())
        val audioTrack = peerConnectionFactory.createAudioTrack(LOCAL_AUDIO_TRACK_ID, audioSource)
        micControl = AudioTrackControl(audioTrack)
        localAudioTrackField = audioTrack
    }

    override fun createPeerConnectionFor(peerId: String, turn: TurnCredentials, observer: PeerConnection.Observer): PeerConnection? {
        val configuration = PeerConnection.RTCConfiguration(buildIceServers(turn)).apply {
            sdpSemantics = PeerConnection.SdpSemantics.UNIFIED_PLAN
        }
        val connection = peerConnectionFactory.createPeerConnection(configuration, observer) ?: return null
        peerConnections[peerId] = connection
        return connection
    }

    override fun closePeerConnectionFor(peerId: String) {
        peerConnections.remove(peerId)?.close()
    }

    override fun setMicEnabled(enabled: Boolean) {
        micControl?.enabled = enabled
    }

    override fun setCameraEnabled(enabled: Boolean) {
        cameraControl?.enabled = enabled
    }

    override fun switchCamera() {
        cameraCapturer?.switchCamera(null)
    }

    override fun dispose() {
        peerConnections.values.forEach { it.close() }
        peerConnections.clear()
        cameraCapturer?.stopCapture()
        cameraCapturer?.dispose()
        cameraCapturer = null
        if (eglBaseDelegate.isInitialized()) {
            eglBase.release()
        }
    }
}

private val webRtcLoaded = java.util.concurrent.atomic.AtomicBoolean(false)

internal fun ensureWebRtcLoaded(context: Context) {
    if (!webRtcLoaded.compareAndSet(false, true)) return
    PeerConnectionFactory.initialize(
        PeerConnectionFactory.InitializationOptions.builder(context)
            .createInitializationOptions(),
    )
}
