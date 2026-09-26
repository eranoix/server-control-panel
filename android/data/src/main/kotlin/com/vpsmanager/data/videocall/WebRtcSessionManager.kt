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
 * Pure W3C "perfect negotiation" politeness decision (https://w3c.github.io/webrtc-pc/#perfect-negotiation-example):
 * the side with the lexicographically smaller peer id is "polite" (rolls back its own local
 * description and accepts a colliding incoming offer), the other is "impolite" (keeps its
 * outgoing offer and ignores a colliding incoming one). Applied per [PeerConnection], not as a
 * single global flag, because a peer can be polite towards one mesh member and impolite towards
 * another in the same call.
 */
fun isPolite(localPeerId: String, remotePeerId: String): Boolean = localPeerId < remotePeerId

/**
 * One `org.webrtc.PeerConnection.IceServer` per URL in [turn] — `internal/videocall/types.go`'s
 * `TURNCredentials.URLs` is already a list because a single TURN allocation is commonly
 * advertised under both a UDP and a TCP/TLS URL. `credential` (never `password`, see
 * [TurnCredentials]) feeds `.setPassword(...)`, which is `org.webrtc`'s own builder method name
 * for that value, unrelated to the wire's field name.
 */
fun buildIceServers(turn: TurnCredentials): List<PeerConnection.IceServer> = turn.urls.map { url ->
    PeerConnection.IceServer.builder(url)
        .setUsername(turn.username)
        .setPassword(turn.credential)
        .createIceServer()
}

/**
 * Seam over a captured local track's mute state. `org.webrtc.MediaStreamTrack` (the common base
 * of `AudioTrack`/`VideoTrack`) has a native-pointer constructor and JNI-backed
 * `enabled()`/`setEnabled()` methods — it cannot be constructed or faked directly in a plain JVM
 * unit test without the native WebRTC library loaded. [WebRtcSessionManager] depends on this
 * narrow interface instead, so [WebRtcSessionManagerTest] can inject a hand-rolled fake that
 * records every call, proving mic/camera toggles reach the underlying track rather than a
 * UI-only boolean.
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
 * The narrow seam `CallViewModel` (`:feature-videocall`) depends on instead of the concrete
 * [WebRtcSessionManager]. Every member here requires the real native WebRTC library — and, for
 * [startLocalMedia], an actual camera/microphone — so none of it can be exercised against the
 * real implementation on the JVM. [WebRtcSessionManager] is the only production implementation;
 * `CallViewModelTest` injects a hand-rolled fake instead, mirroring [VideocallTicketSource]'s and
 * [LocalMediaTrackControl]'s existing seam pattern in this file.
 */
interface VideoCallSessionController {
    /** Shared EGL context every [org.webrtc.SurfaceViewRenderer] must `init` with to render
     * hardware-captured frames from this session without a costly texture copy. */
    val eglBaseContext: EglBase.Context

    /** The local camera's [VideoTrack], once [startLocalMedia] has run; `null` before that. */
    val localVideoTrack: VideoTrack?

    /** The local microphone's [AudioTrack], once [startLocalMedia] has run; `null` before that.
     * Callers add both this and [localVideoTrack] to every [PeerConnection] created via
     * [createPeerConnectionFor] — a call with a video track but no audio track reaches other
     * participants silently, which is not a real call. */
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
 * Owns the call's `org.webrtc.PeerConnectionFactory`, the local capture pipeline (front-facing
 * camera by default, one audio track), and a `Map<peerId, PeerConnection>` for the mesh — one
 * `PeerConnection` per `peer-joined` event, torn down per `peer-left`, up to the server's
 * existing 4-peer room cap (enforced server-side on `join`, not re-enforced here).
 *
 * The factory and capture pipeline are only ever touched from [startLocalMedia] and
 * [createPeerConnectionFor], both of which require the real native WebRTC library and a device
 * camera/microphone — they cannot be exercised on the JVM and are exempt from unit testing (see
 * the plan's human-verification script for the on-device checks this manager still needs).
 * [isPolite], [setMicEnabled], [setCameraEnabled], and [switchCamera] are the testable surface:
 * [micControl]/[cameraControl]/[cameraCapturer] are `internal` precisely so
 * [WebRtcSessionManagerTest] can inject fakes for them without constructing the real factory.
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
            // BEFORE ANYTHING ELSE IN WEBRTC: load the native library.
            //
            // Without this the app DIES — `DefaultVideoEncoderFactory` builds a
            // `SoftwareVideoEncoderFactory`, whose constructor calls straight
            // into a native method, and the process goes down with
            // `UnsatisfiedLinkError: No implementation found for long
            // SoftwareVideoEncoderFactory.nativeCreateFactory()`.
            //
            // This NEVER existed in this code. The call was nowhere in the app
            // — the video call had been broken since it was written, and it
            // only stayed hidden because the factory was built only after the
            // server answered `joined`, a path that almost never got exercised
            // in testing. The waiting room started turning the camera on BEFORE
            // the signal, and the latent defect became a crash in the face of
            // whoever opened the screen.
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
     * Starts local capture: front-facing camera by default (falls back to the first available
     * device if no front camera is reported), one [VideoTrack] and one [AudioTrack], both wired
     * through [micControl]/[cameraControl] so [setMicEnabled]/[setCameraEnabled] work immediately.
     * Requires the real native library and an actual camera/microphone — not unit-testable.
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
     * Creates and registers a [PeerConnection] for [peerId], applying [turn]'s credentials as its
     * `RTCConfiguration.iceServers` (see [buildIceServers]). Requires the real native factory —
     * not unit-testable; the perfect-negotiation glare handling this connection's [observer] must
     * implement is decided by calling [isPolite] with this manager's own peer id and [peerId].
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

    /** Toggles the local microphone. Always available, independent of any call's connection state. */
    override fun setMicEnabled(enabled: Boolean) {
        micControl?.enabled = enabled
    }

    /** Toggles the local camera — distinct from ending the call. */
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
 * Loads WebRTC's native library, once per process.
 *
 * `PeerConnectionFactory.initialize` is WebRTC's `System.loadLibrary`: until it
 * runs, EVERY `org.webrtc` type with a native method in its constructor brings
 * the process down instead of throwing something you can handle. It is neither
 * optional nor an optimisation — it is the line without which no video exists
 * at all.
 *
 * Guarded by an [AtomicBoolean] and not by `lazy`: the initialisation is a
 * GLOBAL effect on the process, not the value of an object. Two instances of
 * [WebRtcSessionManager] — one from the call, another from an instrumented test
 * — have to share the same "already done", and a per-instance `lazy` would call
 * `initialize` twice.
 *
 * `compareAndSet` and not `get`-then-`set`: two screens opening at once (the
 * waiting room and an incoming call on the lock screen) would arrive here on
 * different threads, and a two-step check would let both through.
 */
private val webRtcLoaded = java.util.concurrent.atomic.AtomicBoolean(false)

internal fun ensureWebRtcLoaded(context: Context) {
    if (!webRtcLoaded.compareAndSet(false, true)) return
    PeerConnectionFactory.initialize(
        PeerConnectionFactory.InitializationOptions.builder(context)
            .createInitializationOptions(),
    )
}
