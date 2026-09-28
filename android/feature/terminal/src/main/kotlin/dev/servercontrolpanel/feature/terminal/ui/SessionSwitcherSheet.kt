package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.data.terminal.TerminalSession

const val SESSIONS_SHEET_TAG = "sessions-sheet"

const val SESSIONS_FILTER_TAG = "sessions-filter"

const val SESSIONS_NEW_TAG = "sessions-nova"

fun sessionTag(name: String): String = "session-$name"

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionSwitcherSheet(
    currentSession: String,
    state: SessionListUiState,
    onSwitchSession: (String) -> Unit,
    onCreateSession: (String) -> Unit,
    onDetach: () -> Unit,
    onDismissRequest: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onDismissRequest,
        sheetState = sheetState,
        modifier = Modifier.testTag(SESSIONS_SHEET_TAG),
    ) {
        SessionSwitcherContent(
            currentSession = currentSession,
            state = state,
            onSwitchSession = onSwitchSession,
            onCreateSession = onCreateSession,
            onDetach = onDetach,
        )
    }
}

@Composable
internal fun SessionSwitcherContent(
    currentSession: String,
    state: SessionListUiState,
    onSwitchSession: (String) -> Unit,
    onCreateSession: (String) -> Unit,
    onDetach: () -> Unit,
) {
    var filter by remember { mutableStateOf("") }
    var newName by remember { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 20.dp)
            .padding(bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(text = "Sessions", style = MaterialTheme.typography.titleMedium)

        when (state) {
            is SessionListUiState.Loading -> CircularProgressIndicator()

            is SessionListUiState.Error -> Text(
                text = state.message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )

            is SessionListUiState.Empty -> Text(
                text = "No sessions besides this one. Create one below.",
                style = MaterialTheme.typography.bodySmall,
            )

            is SessionListUiState.Success -> {
                val visible = state.sessions.filter {
                    filter.isBlank() || it.name.contains(filter, ignoreCase = true)
                }
                if (state.sessions.size > FILTER_THRESHOLD) {
                    OutlinedTextField(
                        value = filter,
                        onValueChange = { filter = it },
                        label = { Text(text = "Filter") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().testTag(SESSIONS_FILTER_TAG),
                    )
                }
                LazyColumn(
                    modifier = Modifier.fillMaxWidth().heightIn(max = MAX_LIST_HEIGHT),
                    verticalArrangement = Arrangement.spacedBy(2.dp),
                ) {
                    items(visible, key = { it.name }) { session ->
                        SessionRow(
                            index = state.sessions.indexOf(session) + 1,
                            session = session,
                            current = session.name == currentSession,
                            onClick = { onSwitchSession(session.name) },
                        )
                    }
                }
                if (visible.isEmpty()) {
                    Text(
                        text = "No session with that name.",
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        }

        HorizontalDivider()

        Row(verticalAlignment = Alignment.CenterVertically) {
            OutlinedTextField(
                value = newName,
                onValueChange = { newName = it },
                label = { Text(text = "New session") },
                singleLine = true,
                modifier = Modifier.weight(1f).testTag(SESSIONS_NEW_TAG),
            )
            Button(
                onClick = { onCreateSession(newName.trim()) },
                enabled = newName.isNotBlank(),
                modifier = Modifier.padding(start = 8.dp),
            ) { Text(text = "Create") }
        }

        OutlinedButton(onClick = onDetach, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Detach (the session keeps running on the server)")
        }
    }
}

@Composable
private fun SessionRow(
    index: Int,
    session: TerminalSession,
    current: Boolean,
    onClick: () -> Unit,
) {
    TextButton(
        onClick = onClick,
        enabled = !current,
        modifier = Modifier.fillMaxWidth().testTag(sessionTag(session.name)),
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = "[$index] ${session.name}",
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = if (current) FontWeight.Bold else FontWeight.Normal,
            )
            Text(
                text = when {
                    current -> "on this screen now"
                    session.attached -> "open in another client"
                    else -> "not attached"
                },
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
}

private const val FILTER_THRESHOLD = 8

private val MAX_LIST_HEIGHT = 340.dp
