package com.vpsmanager.data.videocall

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

private const val LOCAL_AUDIO_TRACK_ID = "vpsm-audio0"
private const val LOCAL_VIDEO_TRACK_ID = "vpsm-video0"
private const val CAPTURE_THREAD_NAME = "VpsmVideocallCapture"
private const val CAPTURE_WIDTH = 1280
private const val CAPTURE_HEIGHT = 720
private const val CAPTURE_FPS = 30

/**
 * W3C "perfect negotiation" politeness: the side with the lexicographically smaller
 * peer id is polite (rolls back and accepts a colliding offer). Decided per
 * [PeerConnection], since a peer can be polite to one mesh member and not another.
 */
fun isPolite(localPeerId: String, remotePeerId: String): Boolean = localPeerId < remotePeerId

/**
 * One `IceServer` per URL in [turn] (a TURN allocation is often advertised over
 * both UDP and TCP/TLS). `credential` feeds `setPassword`, which is just
 * `org.webrtc`'s builder name.
 */
fun buildIceServers(turn: TurnCredentials): List<PeerConnection.IceServer> = turn.urls.map { url ->
    PeerConnection.IceServer.builder(url)
        .setUsername(turn.username)
        .setPassword(turn.credential)
        .createIceServer()
}

/**
 * Seam over a local track's mute state. `org.webrtc.MediaStreamTrack` is backed by
 * JNI and cannot be faked on the JVM, so [WebRtcSessionManagerTest] injects a fake
 * to prove mic/camera toggles reach the real track.
 */
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

/**
 * The seam `CallViewModel` depends on instead of [WebRtcSessionManager]. Every
 * member needs the native WebRTC library (and a camera for [startLocalMedia]), so
 * `CallViewModelTest` uses a fake.
 */
interface VideoCallSessionController {
    /** Shared EGL context every [org.webrtc.SurfaceViewRenderer] must `init` with to avoid a texture copy. */
    val eglBaseContext: EglBase.Context

    /** The local camera's [VideoTrack], once [startLocalMedia] has run; `null` before that. */
    val localVideoTrack: VideoTrack?

    /**
     * The local microphone's [AudioTrack] once [startLocalMedia] has run. Add it
     * along with [localVideoTrack] to every [PeerConnection], or the call is silent.
     */
    val localAudioTrack: AudioTrack?

    fun startLocalMedia()
    fun createPeerConnectionFor(peerId: String, turn: TurnCredentials, observer: PeerConnection.Observer): PeerConnection?
    fun closePeerConnectionFor(peerId: String)
    fun setMicEnabled(enabled: Boolean)
    fun setCameraEnabled(enabled: Boolean)
    fun switchCamera()
    fun dispose()
}

/**
 * Owns the call's `PeerConnectionFactory`, the local capture pipeline (front
 * camera by default, one audio track) and one `PeerConnection` per mesh peer,
 * created on `peer-joined` and closed on `peer-left` (the 4-peer cap is enforced
 * by the server).
 *
 * The factory and capture need the native library and real hardware, so they are
 * not unit tested; [micControl], [cameraControl] and [cameraCapturer] are
 * `internal` so [WebRtcSessionManagerTest] can inject fakes.
 */
class WebRtcSessionManager internal constructor(
    private val context: Context,
    private val eglBaseProvider: () -> EglBase,
    private val peerConnectionFactoryProvider: (EglBase) -> PeerConnectionFactory,
) : VideoCallSessionController {
    /** Public constructor: always wires the real `org.webrtc` factory (built lazily, on first use). */
    constructor(context: Context) : this(
        context = context.applicationContext,
        eglBaseProvider = { EglBase.create() },
        peerConnectionFactoryProvider = { eglBase ->
            // Load the native library before anything else in WebRTC, or
            // `DefaultVideoEncoderFactory` crashes the process with
            // `UnsatisfiedLinkError` (SoftwareVideoEncoderFactory.nativeCreateFactory).
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

    /**
     * Starts local capture: front camera (or the first available), one [VideoTrack]
     * and one [AudioTrack], wired so [setMicEnabled]/[setCameraEnabled] work
     * immediately. Needs real hardware, not unit-testable.
     */
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

    /**
     * Creates and registers a [PeerConnection] for [peerId] with [turn] as its ICE
     * servers. The [observer] handles glare using [isPolite]. Not unit-testable.
     */
    override fun createPeerConnectionFor(peerId: String, turn: TurnCredentials, observer: PeerConnection.Observer): PeerConnection? {
        val configuration = PeerConnection.RTCConfiguration(buildIceServers(turn)).apply {
            sdpSemantics = PeerConnection.SdpSemantics.UNIFIED_PLAN
        }
        val connection = peerConnectionFactory.createPeerConnection(configuration, observer) ?: return null
        peerConnections[peerId] = connection
        return connection
    }

    /** Tears down and forgets the [PeerConnection] for [peerId] (a `peer-left` event). */
    override fun closePeerConnectionFor(peerId: String) {
        peerConnections.remove(peerId)?.close()
    }

    /** Toggles the local microphone, independent of connection state. */
    override fun setMicEnabled(enabled: Boolean) {
        micControl?.enabled = enabled
    }

    /** Toggles the local camera (distinct from ending the call). */
    override fun setCameraEnabled(enabled: Boolean) {
        cameraControl?.enabled = enabled
    }

    /** Flips front/back camera. A no-op if local media has not been started yet. */
    override fun switchCamera() {
        cameraCapturer?.switchCamera(null)
    }

    /** Ends every peer connection and releases the capture pipeline. */
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

/**
 * Loads WebRTC's native library once per process. Until it runs, any `org.webrtc`
 * type with a native constructor call crashes the process.
 *
 * A process-wide [AtomicBoolean] rather than a per-instance `lazy`, and
 * `compareAndSet` so two screens opening on different threads cannot both initialize.
 */
private val webRtcLoaded = java.util.concurrent.atomic.AtomicBoolean(false)

internal fun ensureWebRtcLoaded(context: Context) {
    if (!webRtcLoaded.compareAndSet(false, true)) return
    PeerConnectionFactory.initialize(
        PeerConnectionFactory.InitializationOptions.builder(context)
            .createInitializationOptions(),
    )
}
