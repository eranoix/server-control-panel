package dev.servercontrolpanel.feature.terminal.attach

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import java.util.UUID

/** Test tag of the attachments bar. */
const val ATTACHMENT_BAR_TAG = "barra-anexos-terminal"

/** Label of the button that puts the path of ONE ready attachment on the command line. */
const val INSERT_LABEL = "Insert"

/** Label of the button that inserts every ready attachment at once. */
const val INSERT_ALL_LABEL = "Insert all"

/**
 * The attachments strip, flush above the key bar where the thumb and eye already
 * are. With no attachment it emits no node, so it costs no height (the rule
 * `TerminalRoute` applies to all chrome). Each row states its status in words,
 * not just colour, for screen readers and bright sunlight.
 */
@Composable
fun AttachmentBar(
    attachments: List<ScreenAttachment>,
    onInsert: (List<UUID>) -> Unit,
    onCancel: (UUID) -> Unit,
    onDiscard: (UUID) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (attachments.isEmpty()) return

    val readyIds = attachments.filter { it.state is AttachmentState.Ready }.map { it.id }

    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(horizontal = 12.dp, vertical = 4.dp)
            .testTag(ATTACHMENT_BAR_TAG),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        attachments.forEach { attachment ->
            AttachmentRow(
                attachment = attachment,
                onInsert = { onInsert(listOf(attachment.id)) },
                onCancel = { onCancel(attachment.id) },
                onDiscard = { onDiscard(attachment.id) },
            )
        }
        // "All" only when there is more than one; otherwise the row button suffices.
        if (readyIds.size > 1) {
            TextButton(onClick = { onInsert(readyIds) }) { Text(text = "$INSERT_ALL_LABEL (${readyIds.size})") }
        }
    }
}

@Composable
private fun AttachmentRow(
    attachment: ScreenAttachment,
    onInsert: () -> Unit,
    onCancel: () -> Unit,
    onDiscard: () -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = attachment.name,
                style = MaterialTheme.typography.labelLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = stateDescription(attachment.state),
                style = MaterialTheme.typography.bodySmall,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            val state = attachment.state
            if (state is AttachmentState.Uploading && state.percent != UNKNOWN_PERCENT) {
                LinearProgressIndicator(
                    progress = { state.percent / 100f },
                    modifier = Modifier.fillMaxWidth().widthIn(min = 0.dp),
                )
            }
        }
        when (attachment.state) {
            is AttachmentState.Uploading -> TextButton(onClick = onCancel) { Text(text = "Cancel") }
            is AttachmentState.Ready -> {
                TextButton(onClick = onInsert) { Text(text = INSERT_LABEL) }
                TextButton(onClick = onDiscard) { Text(text = "Dismiss") }
            }
            is AttachmentState.Failed, is AttachmentState.Cancelled ->
                TextButton(onClick = onDiscard) { Text(text = "Dismiss") }
        }
    }
}

/**
 * The status in words. Errors show the server's reason (made actionable by
 * `transferErrorFor` in `:data`), never a generic "failed": "no disk space" and
 * "file too large" call for opposite actions.
 */
internal fun stateDescription(state: AttachmentState): String = when (state) {
    is AttachmentState.Uploading ->
        if (state.percent == UNKNOWN_PERCENT) "Uploading…" else "Uploading ${state.percent}%"
    is AttachmentState.Ready -> state.path
    is AttachmentState.Failed -> state.reason
    is AttachmentState.Cancelled -> "Upload canceled."
}
