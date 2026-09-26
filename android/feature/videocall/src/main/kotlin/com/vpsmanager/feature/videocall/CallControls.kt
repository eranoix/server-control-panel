package com.vpsmanager.feature.videocall

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
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
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.layout.Arrangement

/**
 * Bottom control bar for an active call: mic mute, camera off, front/back switch, and
 * hang-up, each a distinct pressed/toggled visual state (background color + symbol both change,
 * never just the symbol) so a muted mic or disabled camera is unmistakable at a glance.
 */
@Composable
fun CallControls(
    micEnabled: Boolean,
    cameraEnabled: Boolean,
    onToggleMic: () -> Unit,
    onToggleCamera: () -> Unit,
    onSwitchCamera: () -> Unit,
    onHangUp: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(24.dp),
        horizontalArrangement = Arrangement.SpaceEvenly,
    ) {
        ControlButton(
            symbol = if (micEnabled) "🎤" else "🔇",
            muted = !micEnabled,
            contentDescription = if (micEnabled) "Mute microphone" else "Unmute microphone",
            onClick = onToggleMic,
        )
        ControlButton(
            symbol = if (cameraEnabled) "📷" else "🚫",
            muted = !cameraEnabled,
            contentDescription = if (cameraEnabled) "Turn off camera" else "Turn on camera",
            onClick = onToggleCamera,
        )
        ControlButton(
            symbol = "🔄",
            muted = false,
            contentDescription = "Switch camera",
            onClick = onSwitchCamera,
        )
        ControlButton(
            symbol = "📴",
            muted = false,
            danger = true,
            contentDescription = "End call",
            onClick = onHangUp,
        )
    }
}

@Composable
private fun ControlButton(
    symbol: String,
    muted: Boolean,
    contentDescription: String,
    onClick: () -> Unit,
    danger: Boolean = false,
) {
    val background = when {
        danger -> MaterialTheme.colorScheme.error
        muted -> MaterialTheme.colorScheme.errorContainer
        else -> MaterialTheme.colorScheme.surfaceVariant
    }
    Box(
        modifier = Modifier
            .size(56.dp)
            .clip(CircleShape)
            .background(background)
            .clickable(onClick = onClick)
            .semantics { this.contentDescription = contentDescription },
        contentAlignment = Alignment.Center,
    ) {
        Text(text = symbol, style = MaterialTheme.typography.titleLarge)
    }
}
