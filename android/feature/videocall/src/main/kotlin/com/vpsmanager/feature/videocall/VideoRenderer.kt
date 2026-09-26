package com.vpsmanager.feature.videocall

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import io.getstream.webrtc.android.compose.VideoRenderer
import org.webrtc.EglBase
import org.webrtc.RendererCommon
import org.webrtc.VideoTrack

/**
 * One participant's video tile. Renders through
 * `io.getstream.webrtc.android.compose.VideoRenderer` (`stream-webrtc-android-compose`), which
 * already wraps `org.webrtc.SurfaceViewRenderer` with `init`/`release` tied to its own
 * composition lifecycle — the exact `DisposableEffect`-based glue the plan's action text
 * describes hand-rolling turns out to already exist in the vendor library, a lower-risk choice
 * than reimplementing it. [eglBaseContext] must be the SAME context the capture pipeline's
 * `PeerConnectionFactory` uses (`CallViewModel.eglBaseContext`) — a mismatched context can fail
 * to render hardware-captured (OES) frames.
 *
 * [track] is `null` before a peer's track has arrived (`peer-joined` received, `onTrack` not yet
 * fired) or while the local/remote camera is toggled off — both render an explicit placeholder,
 * never a frozen or black frame.
 */
@Composable
fun VideoTile(
    track: VideoTrack?,
    eglBaseContext: EglBase.Context,
    modifier: Modifier = Modifier,
) {
    if (track != null) {
        VideoRenderer(
            modifier = modifier,
            videoTrack = track,
            eglBaseContext = eglBaseContext,
            rendererEvents = NoopRendererEvents,
        )
    } else {
        CameraOffPlaceholder(modifier)
    }
}

/** [io.getstream.webrtc.android.compose.VideoRenderer] requires a callback; this tile has no use for frame-size/first-frame events. */
private object NoopRendererEvents : RendererCommon.RendererEvents {
    override fun onFirstFrameRendered() = Unit
    override fun onFrameResolutionChanged(videoWidth: Int, videoHeight: Int, rotation: Int) = Unit
}

@Composable
private fun CameraOffPlaceholder(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.surfaceVariant),
        contentAlignment = Alignment.Center,
    ) {
        Text(text = "📷", style = MaterialTheme.typography.displayMedium)
    }
}
