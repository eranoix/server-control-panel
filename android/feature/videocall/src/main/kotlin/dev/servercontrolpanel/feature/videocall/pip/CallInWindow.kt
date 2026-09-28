package dev.servercontrolpanel.feature.videocall.pip

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.feature.videocall.CallUiState
import dev.servercontrolpanel.feature.videocall.VideoTile
import org.webrtc.EglBase

@Composable
fun CallInWindow(
    state: CallUiState.InCall,
    eglBaseContext: EglBase.Context,
    modifier: Modifier = Modifier,
) {
    val remote = state.remoteTracks.values.firstOrNull { it != null }
    val trail = remote ?: state.localTrack

    Box(modifier = modifier.fillMaxSize().background(Color.Black)) {
        VideoTile(
            track = trail,
            eglBaseContext = eglBaseContext,
            modifier = Modifier.fillMaxSize(),
        )

        Row(
            modifier = Modifier
                .align(Alignment.BottomStart)
                .fillMaxWidth()
                .background(Color.Black.copy(alpha = 0.45f))
                .padding(horizontal = 6.dp, vertical = 3.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (!state.micEnabled) {
                Box(
                    modifier = Modifier
                        .size(6.dp)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.error),
                )
                Text(
                    text = " muted",
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                )
                Text(text = " · ", style = MaterialTheme.typography.labelSmall, color = Color.White)
            }
            Text(
                text = participantsText(state.remoteTracks.size),
                style = MaterialTheme.typography.labelSmall,
                color = Color.White,
            )
        }
    }
}

internal fun participantsText(remotes: Int): String = when (remotes) {
    0 -> "alone in the room"
    1 -> "1 in the call"
    else -> "$remotes in the call"
}
