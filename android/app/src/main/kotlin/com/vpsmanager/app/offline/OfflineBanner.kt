package com.vpsmanager.app.offline

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
import com.vpsmanager.data.offline.NetworkState
import com.vpsmanager.data.offline.Outbox
import com.vpsmanager.data.offline.DataAge
import com.vpsmanager.data.offline.DeviceNetwork
import com.vpsmanager.designsystem.vpsmStatusColors
import java.util.Locale

/** Text of the banner when there has been no contact at all in this run. */
internal const val OFFLINE_NO_CONTACT = "Offline — nothing has loaded yet"

/**
 * The banner that stops the cache from lying.
 *
 * ## Why it is mandatory, and not decoration
 *
 * The read cache keeps the app opening without internet. On its own, it
 * creates a new problem worse than the one it solves: the screen starts
 * claiming, with its usual face, that the disk is at 78% — when that
 * number may be six hours old and the disk may have filled up since.
 * **Stale data with no label is worse than an error screen**, because an
 * error screen never stops anyone from acting.
 *
 * This banner is the label. It appears **only** when there is no validated
 * network, says how long ago the server's last response was, and
 * disappears on its own when the connection comes back — requiring no tap,
 * because there is nothing to decide.
 *
 * ## Why "the server's last response", and not "this data is from"
 *
 * The timestamp belongs to the whole app, not to this screen (see
 * [DataAge]). Saying "this data is from 11:45" would be more precise
 * than what is actually known: another screen may have talked to the
 * server since. The sentence chosen is true in every case.
 *
 * ## Why yellow, and not red
 *
 * There is no failure: the server is fine, the app is working and showing
 * what it has. Red would teach people to ignore red.
 */
@Composable
fun OfflineBanner(modifier: Modifier = Modifier) {
    val state by DeviceNetwork.state.collectAsStateWithLifecycle()
    val last by DataAge.lastNetworkResponse.collectAsStateWithLifecycle()
    val pending by Outbox.pending.collectAsStateWithLifecycle()
    val rejected by Outbox.rejected.collectAsStateWithLifecycle()
    val warning = vpsmStatusColors.warning

    // The banner also shows WITH network when there is a queued or refused
    // action: the queue exists to carry across the return of the connection,
    // and hiding it the instant Wi-Fi comes back would hide exactly the moment
    // it does its work.
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

/**
 * The sentence, split from the drawing so it can be pinned by an ordinary
 * test.
 *
 * The unit follows the age: seconds interest nobody, and "320 minutes ago"
 * forces you to do arithmetic. Past a day the number stops being useful
 * and what matters is "this is old".
 */
internal fun bannerText(lastMs: Long?, nowMs: Long): String {
    if (lastMs == null) return OFFLINE_NO_CONTACT
    val minutes = ((nowMs - lastMs) / 60_000L).coerceAtLeast(0)
    val whenText = when {
        minutes < 1 -> "just now"
        minutes < 60 -> "$minutes min ago"
        minutes < 60 * 24 -> String.format(Locale.forLanguageTag("pt-BR"), "%d h ago", minutes / 60)
        else -> "over a day ago"
    }
    return "Offline — last server response $whenText"
}

/**
 * The sentence about the QUEUE, when there is something to say about it —
 * null when there is not, and then the banner goes back to talking only
 * about the connection.
 *
 * The refusal comes first and alone because it changes what the person
 * does: a "waiting" action is going to happen, a refused one is NOT, and
 * whoever needs to redo something is whoever got refused. Before this the
 * discard was silent — the action left the queue after a 4xx and the
 * person went on believing it would happen.
 */
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
