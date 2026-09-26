package com.vpsmanager.feature.videocall

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

/**
 * The call's green room: what you see before anyone else sees you.
 *
 * ## Why a whole screen just to "join"
 *
 * Joining a call is the one action in this app that is **public and
 * irreversible**. A mistaken `docker restart` can be undone; showing up muted,
 * or with the camera pointed at the ceiling, has already been seen by everyone
 * the instant it happened. Adjusting beforehand costs seconds; adjusting
 * afterwards costs the impression you left.
 *
 * ## The "loading" that is content
 *
 * This is the only screen in the app whose loading state is not waiting: the
 * camera takes real time to wake up, and while it wakes the person is already
 * deciding about the microphone. A `CircularProgressIndicator` here would
 * trade useful work for a spinning clock.
 *
 * ## Errors degrade, they never block
 *
 * The camera being held by another app is the common case, not the exception.
 * The green room says so and offers **joining with audio only** — an audio
 * call is still the call. Blocking entry because the camera would not open
 * trades a feature for an obstacle.
 */
@Composable
internal fun Antessala(
    state: CallUiState.Lobby,
    eglBaseContext: EglBase.Context,
    onToggleMic: () -> Unit,
    onToggleCamera: () -> Unit,
    onSwitchCamera: () -> Unit,
    onEntrar: () -> Unit,
    onDesistir: () -> Unit,
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
                // 16:9 and not a fixed height: the preview has to have the
                // aspect ratio of what everyone else will receive, or it lies
                // about the framing — which is precisely what you came to
                // check.
                .aspectRatio(16f / 9f)
                .clip(RoundedCornerShape(12.dp))
                .background(Color.Black),
            contentAlignment = Alignment.Center,
        ) {
            if (state.semCamera) {
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

        // Chips rather than icons: in the green room, the state matters more
        // than the gesture. "Microphone on" reads; a struck-through microphone
        // icon makes you remember whether the stroke means "you are muted" or
        // "tap to mute" — an ambiguity that has cost every calling app dearly.
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            FilterChip(
                selected = state.micEnabled,
                onClick = onToggleMic,
                label = { Text(text = if (state.micEnabled) "Microphone on" else "Microphone muted") },
            )
            FilterChip(
                selected = state.cameraEnabled && !state.semCamera,
                onClick = onToggleCamera,
                enabled = !state.semCamera,
                label = { Text(text = if (state.cameraEnabled) "Camera on" else "Camera off") },
            )
        }

        if (!state.semCamera) {
            TextButton(onClick = onSwitchCamera) { Text(text = "Flip camera") }
        }

        Button(onClick = onEntrar, modifier = Modifier.fillMaxWidth()) {
            Text(
                text = when {
                    state.semCamera -> "Join with audio only"
                    !state.cameraEnabled -> "Join with audio only"
                    else -> "Join call"
                },
            )
        }
        TextButton(onClick = onDesistir) { Text(text = "Not now") }
    }
}
