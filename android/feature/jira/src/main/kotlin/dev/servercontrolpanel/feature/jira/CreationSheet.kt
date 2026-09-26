package dev.servercontrolpanel.feature.jira

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.data.jira.NewIssue

/** Test tag for the creation sheet. */
internal const val TAG_CREATION_SHEET = "jira-folha-criacao"

/**
 * Issue creation sheet for quick capture.
 *
 * Type, priority and assignee are picked from server-provided lists, never typed, so invalid
 * values cannot be entered. Refinement fields (epic, components, versions, points) are
 * intentionally left to the web panel.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun CreationSheet(
    state: CreationState,
    project: String,
    onClose: () -> Unit,
    onCreate: (NewIssue) -> Unit,
) {
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var summary by remember { mutableStateOf("") }
    var description by remember { mutableStateOf("") }
    var type by remember { mutableStateOf("") }
    var priority by remember { mutableStateOf("") }
    var assignee by remember { mutableStateOf<dev.servercontrolpanel.data.jira.JiraPerson?>(null) }
    var peopleMenu by remember { mutableStateOf(false) }

    // Pre-select the first type so the required field is never empty on open.
    if (type.isBlank() && state.meta.types.isNotEmpty()) {
        type = state.meta.types.first()
    }

    ModalBottomSheet(
        onDismissRequest = onClose,
        sheetState = sheet,
        modifier = Modifier.testTag(TAG_CREATION_SHEET),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(max = 620.dp)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp)
                .padding(bottom = 24.dp),
        ) {
            Text(
                text = if (project.isBlank()) "New issue" else "New issue in $project",
                style = MaterialTheme.typography.titleLarge,
            )

            if (project.isBlank()) {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = "Pick a project on the board before creating — without a project, Jira does not know where the issue belongs.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(16.dp))
                TextButton(onClick = onClose) { Text("Close") }
                return@Column
            }

            Spacer(Modifier.height(16.dp))
            OutlinedTextField(
                value = summary,
                onValueChange = { summary = it },
                label = { Text("Summary") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )

            Spacer(Modifier.height(12.dp))
            OutlinedTextField(
                value = description,
                onValueChange = { description = it },
                label = { Text("Description (optional)") },
                modifier = Modifier.fillMaxWidth(),
            )

            if (state.loadingMeta) {
                Spacer(Modifier.height(16.dp))
                Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            }

            if (state.meta.types.isNotEmpty()) {
                Spacer(Modifier.height(16.dp))
                Text("Type", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                ChoiceRow(
                    options = state.meta.types,
                    chosen = type,
                    onChoose = { type = it },
                )
            }

            if (state.meta.priorities.isNotEmpty()) {
                Spacer(Modifier.height(12.dp))
                Text("Priority (optional)", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                ChoiceRow(
                    options = state.meta.priorities,
                    chosen = priority,
                    onChoose = { priority = if (priority == it) "" else it },
                )
            }

            if (state.people.isNotEmpty()) {
                Spacer(Modifier.height(12.dp))
                Text("Assignee (optional)", style = MaterialTheme.typography.labelLarge)
                Box {
                    TextButton(onClick = { peopleMenu = true }) {
                        Text(assignee?.name ?: "Nobody")
                    }
                    DropdownMenu(expanded = peopleMenu, onDismissRequest = { peopleMenu = false }) {
                        DropdownMenuItem(
                            text = { Text("Nobody") },
                            onClick = {
                                assignee = null
                                peopleMenu = false
                            },
                        )
                        state.people.forEach { p ->
                            DropdownMenuItem(
                                text = { Text(p.name) },
                                onClick = {
                                    assignee = p
                                    peopleMenu = false
                                },
                            )
                        }
                    }
                }
            }

            Spacer(Modifier.height(20.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(
                    onClick = {
                        onCreate(
                            NewIssue(
                                project = project,
                                type = type,
                                summary = summary.trim(),
                                description = description.trim().takeIf { it.isNotBlank() },
                                priority = priority.takeIf { it.isNotBlank() },
                                assigneeId = assignee?.accountId,
                            ),
                        )
                    },
                    enabled = summary.isNotBlank() && type.isNotBlank() && !state.sending,
                ) {
                    Text(if (state.sending) "Creating…" else "Create")
                }
                TextButton(onClick = onClose, enabled = !state.sending) { Text("Cancel") }
            }
        }
    }
}

/** A row of exclusive choices, wrapping onto the next line. */
@Composable
private fun ChoiceRow(
    options: List<String>,
    chosen: String,
    onChoose: (String) -> Unit,
) {
    androidx.compose.foundation.layout.FlowRow(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        options.forEach { option ->
            FilterChip(
                selected = chosen == option,
                onClick = { onChoose(option) },
                label = { Text(option, maxLines = 1) },
            )
        }
    }
}
