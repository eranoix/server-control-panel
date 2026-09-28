package dev.servercontrolpanel.feature.videocall

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
