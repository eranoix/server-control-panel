package com.vpsmanager.feature.jira

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SuggestionChip
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

/** Test tag for the detail sheet. */
internal const val TAG_ISSUE_SHEET = "jira-folha-issue"

/**
 * The open issue, on a sheet that rises over the board.
 *
 * ## Why a sheet, and not a detail screen
 *
 * The board is the context: the person opened that card BECAUSE they were
 * looking at that column. A whole navigation would push a destination, take
 * the board off the screen and force them to go back to carry on where they
 * left off. The sheet keeps the board behind it, and closing is a downward
 * gesture.
 *
 * ## The "move to…" lives here, and it is not redundant with the drag
 *
 * A screen reader does not drag. Without this list, moving an issue would be
 * an inaccessible function — and it is the board's main function. The same
 * list serves whoever has only one hand free, and whoever wants to move to a
 * distant column without dragging a finger across the whole board.
 *
 * The destinations come from the server ALREADY translated into the columns'
 * vocabulary: whoever sees "Em andamento" on the board picks "Em andamento"
 * here, even if Jira calls that state "EM REVISÃO".
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun IssueSheet(
    state: IssueState,
    onClose: () -> Unit,
    onMove: (String, String) -> Unit,
    onComment: (String, String) -> Unit,
    onAssignToMe: (String) -> Unit,
    onUnassign: (String) -> Unit,
) {
    if (state is IssueState.Closed) return
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    ModalBottomSheet(
        onDismissRequest = onClose,
        sheetState = sheet,
        modifier = Modifier.testTag(TAG_ISSUE_SHEET),
    ) {
        when (state) {
            is IssueState.Closed -> Unit

            is IssueState.Loading -> Box(
                modifier = Modifier.fillMaxWidth().height(180.dp),
                contentAlignment = Alignment.Center,
            ) { CircularProgressIndicator() }

            is IssueState.Error -> Column(Modifier.padding(24.dp)) {
                Text(state.key, style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Text(state.message, style = MaterialTheme.typography.bodyMedium)
            }

            is IssueState.Ready -> IssueBody(
                issue = state.issue,
                onMove = onMove,
                onComment = onComment,
                onAssignToMe = onAssignToMe,
                onUnassign = onUnassign,
            )
        }
    }
}

@Composable
private fun IssueBody(
    issue: com.vpsmanager.data.jira.JiraIssue,
    onMove: (String, String) -> Unit,
    onComment: (String, String) -> Unit,
    onAssignToMe: (String) -> Unit,
    onUnassign: (String) -> Unit,
) {
    var comment by remember(issue.key) { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(max = 640.dp)
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 20.dp)
            .padding(bottom = 24.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = issue.key,
                style = MaterialTheme.typography.titleMedium,
                fontFamily = FontFamily.Monospace,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(10.dp))
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(6.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .padding(horizontal = 8.dp, vertical = 3.dp),
            ) {
                Text(
                    text = issue.column ?: issue.status,
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }

        Spacer(Modifier.height(8.dp))
        Text(issue.summary, style = MaterialTheme.typography.titleLarge)

        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = issue.assignee?.name ?: "Unassigned",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.weight(1f))
            TextButton(onClick = { onAssignToMe(issue.key) }) { Text("Assign to me") }
            if (issue.assignee != null) {
                TextButton(onClick = { onUnassign(issue.key) }) { Text("Unassign") }
            }
        }

        // --- move to… ------------------------------------------------------
        if (issue.destinations.isNotEmpty()) {
            HorizontalDivider(Modifier.padding(vertical = 12.dp))
            Text("Move to", style = MaterialTheme.typography.labelLarge)
            Spacer(Modifier.height(6.dp))
            Row(
                modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                // Repeated destinations do happen: two different transitions
                // can land on the same column, and offering the column twice
                // would make the person choose between two identical buttons.
                issue.destinations.distinctBy { it.column }.forEach { destination ->
                    SuggestionChip(
                        onClick = { onMove(issue.key, destination.column) },
                        label = { Text(destination.column, maxLines = 1) },
                    )
                }
            }
        }

        if (!issue.description.isNullOrBlank()) {
            HorizontalDivider(Modifier.padding(vertical = 12.dp))
            Text("Description", style = MaterialTheme.typography.labelLarge)
            Spacer(Modifier.height(6.dp))
            Text(issue.description!!, style = MaterialTheme.typography.bodyMedium)
        }

        if (issue.labels.isNotEmpty() || issue.priority != null || issue.type != null) {
            HorizontalDivider(Modifier.padding(vertical = 12.dp))
            Row(
                modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                issue.type?.let { AssistChip(onClick = {}, label = { Text(it) }) }
                issue.priority?.let { AssistChip(onClick = {}, label = { Text(it) }) }
                issue.labels.forEach { AssistChip(onClick = {}, label = { Text(it) }) }
            }
        }

        if (issue.subtasks.isNotEmpty()) {
            HorizontalDivider(Modifier.padding(vertical = 12.dp))
            Text("Subtasks", style = MaterialTheme.typography.labelLarge)
            issue.subtasks.forEach { sub ->
                Text(
                    text = "${sub.key} — ${sub.summary} (${sub.status})",
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
        }

        if (issue.links.isNotEmpty()) {
            HorizontalDivider(Modifier.padding(vertical = 12.dp))
            Text("Links", style = MaterialTheme.typography.labelLarge)
            issue.links.forEach { v ->
                Text(
                    text = "${v.relation}: ${v.key}${v.summary?.let { " — $it" }.orEmpty()}",
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
        }

        // --- comments ------------------------------------------------------
        HorizontalDivider(Modifier.padding(vertical = 12.dp))
        Text(
            text = if (issue.comments.isEmpty()) "Comments" else "Comments (${issue.comments.size})",
            style = MaterialTheme.typography.labelLarge,
        )
        Spacer(Modifier.height(6.dp))
        if (issue.comments.isEmpty()) {
            Text(
                text = "No comments yet.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        issue.comments.forEach { c ->
            Column(modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp)) {
                Text(
                    text = c.author,
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
                Text(c.text, style = MaterialTheme.typography.bodyMedium)
            }
        }

        Spacer(Modifier.height(8.dp))
        OutlinedTextField(
            value = comment,
            onValueChange = { comment = it },
            label = { Text("Write a comment") },
            modifier = Modifier.fillMaxWidth(),
        )
        Spacer(Modifier.height(8.dp))
        Button(
            onClick = {
                onComment(issue.key, comment)
                comment = ""
            },
            enabled = comment.isNotBlank(),
        ) { Text("Comment") }
    }
}
