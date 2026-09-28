package dev.servercontrolpanel.data.videocall

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.webrtc.CameraVideoCapturer

private class FakeTrackControl(initial: Boolean = true) : LocalMediaTrackControl {
    var writes = mutableListOf<Boolean>()
    override var enabled: Boolean = initial
        set(value) {
            writes += value
            field = value
        }
}

private class FakeCameraVideoCapturer : CameraVideoCapturer {
    var switchCameraCallCount = 0
        private set

    override fun switchCamera(switchEventsHandler: CameraVideoCapturer.CameraSwitchHandler?) {
        switchCameraCallCount++
    }

    override fun switchCamera(switchEventsHandler: CameraVideoCapturer.CameraSwitchHandler?, cameraName: String?) {
        switchCameraCallCount++
    }

    override fun initialize(
        surfaceTextureHelper: org.webrtc.SurfaceTextureHelper?,
        applicationContext: android.content.Context?,
        capturerObserver: org.webrtc.CapturerObserver?,
    ) = Unit

    override fun startCapture(width: Int, height: Int, framerate: Int) = Unit

    override fun stopCapture() = Unit

    override fun changeCaptureFormat(width: Int, height: Int, framerate: Int) = Unit

    override fun dispose() = Unit

    override fun isScreencast(): Boolean = false
}

@RunWith(RobolectricTestRunner::class)
class WebRtcSessionManagerTest {

    private fun newManager(): WebRtcSessionManager = WebRtcSessionManager(
        context = RuntimeEnvironment.getApplication(),
        eglBaseProvider = { error("EglBase.create() must not be called by this test") },
        peerConnectionFactoryProvider = { error("PeerConnectionFactory must not be built by this test") },
    )

    @Test
    fun politenessDecisionMatchesLexicographicRule() {
        assertTrue(isPolite(localPeerId = "peerA1", remotePeerId = "peerB2"))
        assertFalse(isPolite(localPeerId = "peerB2", remotePeerId = "peerA1"))
        assertFalse("equal ids: neither side rolls back, so this must not claim politeness", isPolite("peerX", "peerX"))
    }

    @Test
    fun toggleMicFlipsLocalAudioTrackEnabled() {
        val manager = newManager()
        val fakeTrack = FakeTrackControl(initial = true)
        manager.micControl = fakeTrack

        manager.setMicEnabled(false)
        assertFalse(fakeTrack.enabled)
        assertEquals(listOf(false), fakeTrack.writes)

        manager.setMicEnabled(true)
        assertTrue(fakeTrack.enabled)
        assertEquals(listOf(false, true), fakeTrack.writes)
    }

    @Test
    fun toggleCameraFlipsLocalVideoTrackEnabled() {
        val manager = newManager()
        val fakeTrack = FakeTrackControl(initial = true)
        manager.cameraControl = fakeTrack

        manager.setCameraEnabled(false)
        assertFalse(fakeTrack.enabled)
        assertEquals(listOf(false), fakeTrack.writes)

        manager.setCameraEnabled(true)
        assertTrue(fakeTrack.enabled)
        assertEquals(listOf(false, true), fakeTrack.writes)
    }

    @Test
    fun switchCameraInvokesCapturerSwitch() {
        val manager = newManager()
        val fakeCapturer = FakeCameraVideoCapturer()
        manager.cameraCapturer = fakeCapturer

        manager.switchCamera()

        assertEquals(1, fakeCapturer.switchCameraCallCount)
    }

    @Test
    fun buildIceServersProducesOneServerPerTurnUrl() {
        val turn = TurnCredentials(
            urls = listOf("turn:vps.example.com:3478?transport=udp", "turns:vps.example.com:5349?transport=tcp"),
            username = "1735693200:sam",
            credential = "aGVsbG8td29ybGQtaG1hYy1iNjQ=",
            ttl = 3600,
        )

        val servers = buildIceServers(turn)

        assertEquals(2, servers.size)
    }
}
