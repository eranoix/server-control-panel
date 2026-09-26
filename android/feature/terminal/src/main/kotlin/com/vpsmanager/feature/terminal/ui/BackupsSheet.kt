package com.vpsmanager.feature.terminal.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.vpsmanager.data.terminal.SessionBackup
import com.vpsmanager.data.terminal.groupBySession
import com.vpsmanager.data.terminal.BackupVersion
import com.vpsmanager.data.terminal.BackupGroup
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * The session backups sheet.
 *
 * ## Why a sheet, and not a screen
 * A backup is an operation ON the session list, not a place you go to. The
 * sheet keeps the list visible behind it, which is the context for "restore
 * what, and where to" — and leaving it is a gesture, not a navigation with a
 * back stack.
 *
 * ## What each card shows
 * ```
 * ┌───────────────────────────────────────────────┐
 * │ 07/09 20:14 · automatic      1.2 MB   ↺   🗑  │
 * │ [Servidor] [Aplicativo] [tt]                   │
 * │ Servidor — build passed, pushing the deploy    │
 * └───────────────────────────────────────────────┘
 * ```
 * Date, origin, size, which sessions are inside, and one line saying what the
 * first of them was about. The summary is what tells two backups from the same
 * afternoon apart — without it, choosing which one to restore means choosing
 * by timestamp.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BackupsSheet(viewModel: SessionListViewModel, onClose: () -> Unit) {
    val state by viewModel.backups.collectAsStateWithLifecycle()
    val busySession by viewModel.busySession.collectAsStateWithLifecycle()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var confirmingDelete by remember { mutableStateOf<VersionToDelete?>(null) }
    // Which groups are open. All closed by default: the sheet opens showing
    // the SESSION LIST, which is the question ("which session?"), not a wall
    // of dates. Opening everything up front would hand the problem back.
    val expanded = remember { mutableStateMapOf<String, Boolean>() }

    // Load on opening, not in the ViewModel's `init`: listing backups reads
    // every one of the user's files, and that read must not happen every time
    // the sessions screen appears.
    LaunchedEffect(Unit) { viewModel.loadBackups() }

    ModalBottomSheet(onDismissRequest = onClose, sheetState = sheetState) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = 16.dp, end = 16.dp, bottom = 24.dp)
                .heightIn(max = 560.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(text = "Session backups", style = MaterialTheme.typography.titleMedium)
                Spacer(modifier = Modifier.weight(1f))
                TextButton(onClick = { viewModel.saveBackup(null) }) { Text(text = "Save now") }
            }
            Text(
                text = "The server also saves on its own every 6 h and keeps the 10 most recent.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            when (val current = state) {
                is BackupsUiState.Idle, is BackupsUiState.Loading -> CenterBox {
                    CircularProgressIndicator(modifier = Modifier.size(28.dp))
                }
                is BackupsUiState.Empty -> CenterBox {
                    Text(
                        text = "No backups yet.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                is BackupsUiState.Error -> CenterBox {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(text = current.message, style = MaterialTheme.typography.bodyMedium)
                        TextButton(onClick = viewModel::loadBackups) { Text(text = "Try again") }
                    }
                }
                is BackupsUiState.Ready -> {
                    val groups = remember(current.backups) { groupBySession(current.backups) }
                    LazyColumn(
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                        modifier = Modifier.testTag(BACKUP_LIST_TAG),
                    ) {
                        items(items = groups, key = { it.session }) { group ->
                            SessionGroup(
                                group = group,
                                isOpen = expanded[group.session] == true,
                                busy = busySession != null,
                                onToggle = {
                                    expanded[group.session] = expanded[group.session] != true
                                },
                                onRestoreVersion = { version ->
                                    viewModel.restore(version.id, group.session)
                                },
                                onRestoreWholeSnapshot = { version ->
                                    viewModel.restore(version.id)
                                },
                                onDeleteVersion = { version ->
                                    confirmingDelete = VersionToDelete(group.session, version)
                                },
                            )
                        }
                    }
                }
            }
        }
    }

    confirmingDelete?.let { target ->
        AlertDialog(
            onDismissRequest = { confirmingDelete = null },
            title = { Text(text = "Delete this version?") },
            text = {
                // The wording names the SESSION and the DATE, not "this
                // backup": what is deleted here is one version of one
                // session. When the snapshot holds other sessions, they stay
                // — and not saying so would make a person think they are
                // deleting the whole backup.
                Text(
                    text = buildString {
                        append("\"${target.session}\" from ${readableDate(target.version.createdAt)}.")
                        if (target.version.sessionsInBackup > 1) {
                            append(
                                " The other ${target.version.sessionsInBackup - 1} session(s) " +
                                    "in this same backup are kept.",
                            )
                        }
                        append(" This cannot be undone.")
                    },
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    val id = target.version.id
                    val session = target.session
                    confirmingDelete = null
                    viewModel.deleteBackup(id, session)
                }) { Text(text = "Delete") }
            },
            dismissButton = {
                TextButton(onClick = { confirmingDelete = null }) { Text(text = "Cancel") }
            },
        )
    }
}

/** What the confirmation needs to know: which session, from which version. */
private data class VersionToDelete(val session: String, val version: BackupVersion)

/**
 * One group: the session, and its versions once open.
 *
 * ## Why closed by default
 *
 * Once open, this sheet answers "which session?"; the date only matters after
 * that question has been answered. Eight expanded groups would hand back the
 * wall of repeated dates that motivated the grouping in the first place.
 *
 * ## Why the count sits in the header
 *
 * "3 versions · most recent 09/09 08:40" answers, without expanding, the two
 * things that decide whether expanding is worth it: whether there is anything
 * to choose from, and whether the latest one is recent enough.
 */
@Composable
private fun SessionGroup(
    group: BackupGroup,
    isOpen: Boolean,
    busy: Boolean,
    onToggle: () -> Unit,
    onRestoreVersion: (BackupVersion) -> Unit,
    onRestoreWholeSnapshot: (BackupVersion) -> Unit,
    onDeleteVersion: (BackupVersion) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.fillMaxWidth()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable(onClick = onToggle)
                    .padding(horizontal = 12.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = group.session,
                        style = MaterialTheme.typography.titleSmall,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        text = "${group.versions.size} version(s) · latest " +
                            readableDate(group.versions.first().createdAt),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = if (isOpen) "▲" else "▼",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            if (!isOpen && group.summary.isNotBlank()) {
                // The collapsed summary is the clue to WHICH session this is
                // when the name does not say (`tt`, `proxy`). It goes away on
                // expanding: there the versions already have the attention.
                Text(
                    text = group.summary,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(start = 12.dp, end = 12.dp, bottom = 10.dp),
                )
            }

            if (isOpen) {
                group.versions.forEach { version ->
                    HorizontalDivider()
                    VersionRow(
                        version = version,
                        busy = busy,
                        onRestore = { onRestoreVersion(version) },
                        onRestoreAll = { onRestoreWholeSnapshot(version) },
                        onDelete = { onDeleteVersion(version) },
                    )
                }
            }
        }
    }
}

/** One version: date, origin, and what can be done with it. */
@Composable
private fun VersionRow(
    version: BackupVersion,
    busy: Boolean,
    onRestore: () -> Unit,
    onRestoreAll: () -> Unit,
    onDelete: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp)) {
        Text(text = readableDate(version.createdAt), style = MaterialTheme.typography.bodyMedium)
        Text(
            // THE SIZE IS THAT OF THE WHOLE FILE, and the wording says so
            // when it holds more than one session. "89 kB" beside the name of
            // ONE session implies that this session takes 89 kB, which is
            // false in a backup of eight.
            text = buildString {
                append(readableOrigin(version.origin))
                append(" · ")
                if (version.sessionsInBackup > 1) {
                    append("${readableSize(version.bytes)} in a backup of ${version.sessionsInBackup} sessions")
                } else {
                    append(readableSize(version.bytes))
                }
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (version.summary.isNotBlank()) {
            Text(
                text = version.summary,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 2.dp),
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            TextButton(onClick = onRestore, enabled = !busy) { Text(text = "Restore") }
            // "Restore the snapshot" only appears when there is a snapshot
            // to restore: in a backup of a single session it would do exactly
            // the same as the button next to it, under another name.
            if (version.sessionsInBackup > 1) {
                TextButton(onClick = onRestoreAll, enabled = !busy) {
                    Text(text = "All ${version.sessionsInBackup}")
                }
            }
            Spacer(modifier = Modifier.weight(1f))
            TextButton(onClick = onDelete, enabled = !busy) { Text(text = "Delete") }
        }
    }
}

@Composable
private fun CenterBox(content: @Composable () -> Unit) {
    Box(
        modifier = Modifier.fillMaxWidth().height(160.dp),
        contentAlignment = Alignment.Center,
    ) { content() }
}

@Composable
private fun BackupCard(
    backup: SessionBackup,
    busy: Boolean,
    onRestoreAll: () -> Unit,
    onRestoreSession: (String) -> Unit,
    onDelete: () -> Unit,
) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
    ) {
        Column(
            modifier = Modifier.padding(start = 12.dp, end = 4.dp, top = 10.dp, bottom = 10.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = readableDate(backup.createdAt),
                        style = MaterialTheme.typography.titleSmall,
                    )
                    Text(
                        text = listOfNotNull(
                            readableOrigin(backup.origin),
                            readableSize(backup.bytes),
                            "${backup.sessions.size} session(s)",
                        ).joinToString(" · "),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                IconButton(onClick = onRestoreAll, enabled = !busy) {
                    Icon(
                        imageVector = Icons.Filled.Refresh,
                        contentDescription = "Restore all sessions in this backup",
                    )
                }
                IconButton(onClick = onDelete, enabled = !busy) {
                    Icon(
                        imageVector = Icons.Filled.Delete,
                        contentDescription = "Delete this backup",
                        tint = MaterialTheme.colorScheme.error,
                    )
                }
            }

            // Tapping a chip restores ONLY that session. That is the truly
            // common case: one almost never wants the whole bundle back, one
            // wants the session that was lost.
            SessionChips(
                names = backup.sessions.map { it.name },
                onTap = { if (!busy) onRestoreSession(it) },
            )

            backup.sessions.firstOrNull { it.summary.isNotBlank() }?.let { s ->
                Surface(
                    color = MaterialTheme.colorScheme.surface,
                    shape = RoundedCornerShape(8.dp),
                    modifier = Modifier.fillMaxWidth().padding(end = 8.dp),
                ) {
                    Text(
                        text = s.summary,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(horizontal = 8.dp, vertical = 6.dp),
                    )
                }
            }
        }
    }
}

/**
 * Short date and time. No year: a terminal session backup lives for days, not
 * years — pruning keeps ten — and the year would spend the width that the
 * session names need.
 */
private fun readableDate(seconds: Long): String {
    if (seconds <= 0L) return "—"
    return SimpleDateFormat("dd/MM HH:mm", Locale.getDefault()).format(Date(seconds * 1000))
}

/**
 * The origin, spelled out in Portuguese. It matters because it changes the
 * expectation: an automatic one disappears by itself on pruning, a manual one
 * belongs to the user, a scheduled one has a retention of its own.
 */
private fun readableOrigin(origin: String?): String = when (origin) {
    "manual" -> "manual"
    "auto" -> "automatic"
    "scheduled" -> "scheduled"
    else -> "legacy"
}

private fun readableSize(bytes: Long): String = when {
    bytes <= 0 -> "—"
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> String.format(Locale.getDefault(), "%.0f kB", bytes / 1024.0)
    else -> String.format(Locale.getDefault(), "%.1f MB", bytes / (1024.0 * 1024.0))
}

internal const val BACKUP_LIST_TAG = "backups-lista"
