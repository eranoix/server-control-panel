package com.vpsmanager.feature.terminal.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.vpsmanager.feature.terminal.transport.ConnectionState

/**
 * Always composed above the terminal grid, so a frozen-looking screen is never
 * confused with one that is reconnecting. Every [ConnectionState] renders a
 * distinct label; [isStalled] adds one for a `Live` socket that has not noticed a
 * problem (see `TerminalViewModel.evaluateStall`). Hidden only for a healthy `Live`.
 */
@Composable
fun ConnectionBanner(
    state: ConnectionState,
    isStalled: Boolean,
    modifier: Modifier = Modifier,
    typingDiscarded: Boolean = false,
) {
    val content = bannerContent(state, isStalled, typingDiscarded)
    AnimatedVisibility(visible = content.visible, modifier = modifier) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(content.background)
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (state is ConnectionState.Connecting || state is ConnectionState.Reconnecting) {
                CircularProgressIndicator(
                    modifier = Modifier.padding(end = 8.dp),
                    color = content.onBackground,
                )
            }
            Text(text = content.text, color = content.onBackground)
        }
    }
}

private data class BannerContent(val visible: Boolean, val text: String, val background: Color, val onBackground: Color)

@Composable
private fun bannerContent(
    state: ConnectionState,
    isStalled: Boolean,
    typingDiscarded: Boolean = false,
): BannerContent = when {
    // Takes precedence over any connection state: discarded typing is the only
    // case here that has already cost the user work.
    typingDiscarded -> BannerContent(
        visible = true,
        text = "The connection dropped and what you typed was not sent — run the command again",
        background = MaterialTheme.colorScheme.errorContainer,
        onBackground = MaterialTheme.colorScheme.onErrorContainer,
    )
    state is ConnectionState.Live && isStalled -> BannerContent(
        visible = true,
        text = "No response from the server — the connection seems stuck",
        background = MaterialTheme.colorScheme.errorContainer,
        onBackground = MaterialTheme.colorScheme.onErrorContainer,
    )
    state is ConnectionState.Live -> BannerContent(
        visible = false,
        text = "",
        background = Color.Unspecified,
        onBackground = Color.Unspecified,
    )
    state is ConnectionState.Connecting -> BannerContent(
        visible = true,
        text = "Connecting…",
        background = MaterialTheme.colorScheme.tertiaryContainer,
        onBackground = MaterialTheme.colorScheme.onTertiaryContainer,
    )
    state is ConnectionState.Reconnecting -> BannerContent(
        visible = true,
        text = "Reconnecting (attempt ${state.attempt})…",
        background = MaterialTheme.colorScheme.tertiaryContainer,
        onBackground = MaterialTheme.colorScheme.onTertiaryContainer,
    )
    state is ConnectionState.Disconnected -> BannerContent(
        visible = true,
        text = "Disconnected",
        background = MaterialTheme.colorScheme.errorContainer,
        onBackground = MaterialTheme.colorScheme.onErrorContainer,
    )
    state is ConnectionState.SessionEnded -> BannerContent(
        visible = true,
        text = "This session was ended on the server",
        background = MaterialTheme.colorScheme.errorContainer,
        onBackground = MaterialTheme.colorScheme.onErrorContainer,
    )
    state is ConnectionState.Failed -> BannerContent(
        visible = true,
        text = "Connection failed: ${state.reason}",
        background = MaterialTheme.colorScheme.errorContainer,
        onBackground = MaterialTheme.colorScheme.onErrorContainer,
    )
    else -> BannerContent(visible = false, text = "", background = Color.Unspecified, onBackground = Color.Unspecified)
}
