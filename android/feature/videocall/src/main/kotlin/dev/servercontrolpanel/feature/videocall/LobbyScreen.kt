package dev.servercontrolpanel.feature.videocall

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import org.webrtc.EglBase

@Composable
internal fun LobbyScreen(
    state: CallUiState.Lobby,
    eglBaseContext: EglBase.Context,
    onToggleMic: () -> Unit,
    onToggleCamera: () -> Unit,
    onSwitchCamera: () -> Unit,
    onEnter: () -> Unit,
    onGiveUp: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = "Before joining",
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
        )

        Box(
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(16f / 9f)
                .clip(RoundedCornerShape(12.dp))
                .background(Color.Black),
            contentAlignment = Alignment.Center,
        ) {
            if (state.noCamera) {
                Text(
                    text = "No camera image.\nAnother app may be using it.",
                    color = Color.White,
                    style = MaterialTheme.typography.bodyMedium,
                    textAlign = TextAlign.Center,
                )
            } else {
                VideoTile(
                    track = state.localTrack,
                    eglBaseContext = eglBaseContext,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }

        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(
                selected = state.micEnabled,
                onClick = onToggleMic,
                label = { Text(text = if (state.micEnabled) "Microphone on" else "Microphone muted") },
            )
            FilterChip(
                selected = state.cameraEnabled && !state.noCamera,
                onClick = onToggleCamera,
                enabled = !state.noCamera,
                label = { Text(text = if (state.cameraEnabled) "Camera on" else "Camera off") },
            )
        }

        if (!state.noCamera) {
            TextButton(onClick = onSwitchCamera) { Text(text = "Flip camera") }
        }

        Button(onClick = onEnter, modifier = Modifier.fillMaxWidth()) {
            Text(
                text = when {
                    state.noCamera -> "Join with audio only"
                    !state.cameraEnabled -> "Join with audio only"
                    else -> "Join call"
                },
            )
        }
        TextButton(onClick = onGiveUp) { Text(text = "Not now") }
    }
}
