package dev.servercontrolpanel.app.offline

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.servercontrolpanel.data.offline.NetworkState
import dev.servercontrolpanel.data.offline.Outbox
import dev.servercontrolpanel.data.offline.DataAge
import dev.servercontrolpanel.data.offline.DeviceNetwork
import dev.servercontrolpanel.designsystem.panelStatusColors
import java.util.Locale

internal const val OFFLINE_NO_CONTACT = "Offline — nothing has loaded yet"

@Composable
fun OfflineBanner(modifier: Modifier = Modifier) {
    val state by DeviceNetwork.state.collectAsStateWithLifecycle()
    val last by DataAge.lastNetworkResponse.collectAsStateWithLifecycle()
    val pending by Outbox.pending.collectAsStateWithLifecycle()
    val rejected by Outbox.rejected.collectAsStateWithLifecycle()
    val warning = panelStatusColors.warning

    val visible = state == NetworkState.OFFLINE || pending.isNotEmpty() || rejected.isNotEmpty()

    AnimatedVisibility(visible = visible, modifier = modifier) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(warning.container)
                .padding(horizontal = 16.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = queueText(pending.size, rejected.map { it.description })
                    ?: bannerText(last, System.currentTimeMillis()),
                color = warning.content,
                style = MaterialTheme.typography.bodySmall,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

internal fun bannerText(lastMs: Long?, nowMs: Long): String {
    if (lastMs == null) return OFFLINE_NO_CONTACT
    val minutes = ((nowMs - lastMs) / 60_000L).coerceAtLeast(0)
    val whenText = when {
        minutes < 1 -> "just now"
        minutes < 60 -> "$minutes min ago"
        minutes < 60 * 24 -> String.format(Locale.US, "%d h ago", minutes / 60)
        else -> "over a day ago"
    }
    return "Offline — last server response $whenText"
}

internal fun queueText(pending: Int, rejected: List<String>): String? = when {
    rejected.isNotEmpty() -> {
        val which = rejected.take(2).joinToString("; ")
        val rest = rejected.size - 2
        val tail = if (rest > 0) " (and $rest more)" else ""
        "The server refused: $which$tail — redo it if you still want it."
    }
    pending == 1 -> "1 action waiting for the internet"
    pending > 1 -> "$pending actions waiting for the internet"
    else -> null
}
