package com.vpsmanager.feature.terminal.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.vpsmanager.data.terminal.TARGET_ALL
import com.vpsmanager.data.terminal.TerminalSession
import com.vpsmanager.designsystem.VpsmIcons
import com.vpsmanager.designsystem.vpsmStatusColors

/**
 * The terminal sessions screen.
 *
 * ## What it shows, and why it shows it this way
 *
 * The previous version was one `ListItem` per session with the name on top and
 * the words "Not attached" underneath. Seven sessions filled the screen
 * repeating the same phrase six times, with no icon, no state visible at a
 * glance, not a single action. The operator called it threadbare, and the
 * diagnosis is information density: ~110 px of row to say one name.
 *
 * The new design puts, in the SAME height as before:
 *
 * ```
 * ┌──────────────────────────────────────────────┐
 * │ ▣  Server                     ● active    ⋮  │
 * │    3 h ago · 2 backups                       │
 * └──────────────────────────────────────────────┘
 * ```
 *
 * — icon (category at a glance), name in monospace (it is a session name, not
 * a sentence), state badge on the scanning axis, meta on a single line, and a
 * menu with what can be done to that session. The badge sits on the right
 * because the question you ask while scanning the list is "which one is
 * active?", and the answer has to be in a column, not in the middle of the
 * text.
 *
 * ## Backups
 *
 * They have always existed on the server — an automatic collector every 6 h,
 * plus the button in the web panel — and the app could not see them. Now the
 * summary bar leads to the sheet ([BackupsSheet]) and each session can be saved
 * on its own from its menu.
 */
@Composable
fun SessionListScreen(
    onSessionSelected: (String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: SessionListViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val notice by viewModel.notice.collectAsStateWithLifecycle()
    val busySession by viewModel.busySession.collectAsStateWithLifecycle()
    val previews by viewModel.previews.collectAsStateWithLifecycle()
    val targets by viewModel.targets.collectAsStateWithLifecycle()
    var backupsOpen by rememberSaveable { mutableStateOf(false) }
    var renaming by remember { mutableStateOf<String?>(null) }
    var ending by remember { mutableStateOf<String?>(null) }
    var assigning by remember { mutableStateOf<String?>(null) }

    // The assignment targets are requested when the screen appears, not in the
    // ViewModel's constructor — whoever constructs it controls what starts.
    LaunchedEffect(Unit) { viewModel.loadTargets() }

    Column(modifier = modifier.fillMaxSize()) {
        // The message takes up height only when there is one: a permanent empty
        // strip would cost 40 dp on the screen where the list needs them.
        notice?.let { text ->
            OperationNotice(text = text, onClose = viewModel::clearNotice)
        }

        when (val current = state) {
            is SessionListUiState.Loading -> LoadingContent()
            is SessionListUiState.Error -> ErrorContent(
                message = current.message,
                onRetry = viewModel::refresh,
            )
            is SessionListUiState.Empty -> EmptyContent(
                onAttach = onSessionSelected,
                onOpenBackups = { backupsOpen = true },
            )
            is SessionListUiState.Success -> SessionList(
                sessions = current.sessions,
                busySession = busySession,
                onSessionClick = { onSessionSelected(it.name) },
                onAttach = onSessionSelected,
                onSaveAll = { viewModel.saveBackup(null) },
                onOpenBackups = { backupsOpen = true },
                onSaveSession = { viewModel.saveBackup(it) },
                onRename = { renaming = it },
                previews = previews,
                // The list comes back empty for anyone who is not an admin (the
                // route returns 404), so the option does not even appear —
                // rather than appearing and failing.
                canAssign = !targets.isNullOrEmpty(),
                onPeek = { viewModel.togglePreview(it) },
                onAssign = { assigning = it },
                onEnd = { ending = it },
            )
        }
    }

    if (backupsOpen) {
        BackupsSheet(
            viewModel = viewModel,
            onClose = { backupsOpen = false },
        )
    }

    ending?.let { name ->
        EndDialog(
            name = name,
            onConfirm = {
                ending = null
                viewModel.kill(name)
            },
            onCancel = { ending = null },
        )
    }

    assigning?.let { name ->
        AssignSheet(
            name = name,
            targets = targets.orEmpty(),
            onChoose = { target ->
                assigning = null
                viewModel.assign(name, target)
            },
            onClose = { assigning = null },
        )
    }

    renaming?.let { currentName ->
        RenameDialog(
            currentName = currentName,
            onConfirm = { next ->
                renaming = null
                viewModel.rename(currentName, next)
            },
            onCancel = { renaming = null },
        )
    }
}

/**
 * The sentence reporting back on the last operation.
 *
 * Deliberately not a `Snackbar`: a snackbar vanishes on its own after 4 s, and
 * the answer that matters here ("3 restored, 2 skipped as already present") is
 * precisely the one you want to read twice. This strip stays until it is
 * dismissed.
 */
@Composable
private fun OperationNotice(text: String, onClose: () -> Unit) {
    Surface(
        color = MaterialTheme.colorScheme.secondaryContainer,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier.padding(start = 16.dp, end = 4.dp, top = 8.dp, bottom = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = text,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
                modifier = Modifier.weight(1f),
            )
            TextButton(onClick = onClose) { Text(text = "OK") }
        }
    }
}

@Composable
private fun LoadingContent() {
    Column(
        modifier = Modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        CircularProgressIndicator()
        Text(text = "Loading sessions…", modifier = Modifier.padding(top = 16.dp))
    }
}

@Composable
private fun ErrorContent(message: String, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(16.dp), contentAlignment = Alignment.Center) {
        Card {
            Column(
                modifier = Modifier.padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(text = "Could not load", style = MaterialTheme.typography.titleMedium)
                Text(text = message, style = MaterialTheme.typography.bodyMedium)
                Button(onClick = onRetry) { Text(text = "Try again") }
            }
        }
    }
}

/**
 * No sessions, but possibly WITH backups — and that is the case the previous
 * version had no way of telling. After the machine restarts the list is empty
 * and the whole history is stored away; offering only "type a name" would send
 * the person back to square one on top of a backup they do not even know is
 * there.
 */
@Composable
private fun EmptyContent(onAttach: (String) -> Unit, onOpenBackups: () -> Unit) {
    Column(
        modifier = Modifier.fillMaxSize().padding(16.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(
            imageVector = VpsmIcons.Terminal,
            contentDescription = null,
            modifier = Modifier.size(48.dp),
            tint = MaterialTheme.colorScheme.outline,
        )
        Spacer(modifier = Modifier.height(12.dp))
        Text(text = "No open sessions", style = MaterialTheme.typography.titleMedium)
        Spacer(modifier = Modifier.height(4.dp))
        Text(
            text = "Type a name to open one, or restore from a backup.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(modifier = Modifier.height(20.dp))
        NewSessionField(onAttach = onAttach)
        Spacer(modifier = Modifier.height(8.dp))
        TextButton(onClick = onOpenBackups) { Text(text = "View backups") }
    }
}

@Composable
private fun SessionList(
    sessions: List<TerminalSession>,
    busySession: String?,
    onSessionClick: (TerminalSession) -> Unit,
    onAttach: (String) -> Unit,
    onSaveAll: () -> Unit,
    onOpenBackups: () -> Unit,
    onSaveSession: (String) -> Unit,
    onRename: (String) -> Unit,
    previews: Map<String, PreviewUiState>,
    canAssign: Boolean,
    onPeek: (String) -> Unit,
    onAssign: (String) -> Unit,
    onEnd: (String) -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize()) {
        SummaryBar(
            total = sessions.size,
            active = sessions.count { it.attached },
            savingAll = busySession == SessionListViewModel.ALL,
            onSaveAll = onSaveAll,
            onOpenBackups = onOpenBackups,
        )
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(
                start = 12.dp, end = 12.dp, top = 8.dp, bottom = 16.dp,
            ),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            items(items = sessions, key = { it.name }) { session ->
                SessionRow(
                    session = session,
                    busySession = busySession == session.name,
                    preview = previews[session.name],
                    canAssign = canAssign,
                    onClick = { onSessionClick(session) },
                    onSave = { onSaveSession(session.name) },
                    onRename = { onRename(session.name) },
                    onPeek = { onPeek(session.name) },
                    onAssign = { onAssign(session.name) },
                    onEnd = { onEnd(session.name) },
                )
            }
            item {
                NewSessionCard(onAttach = onAttach)
            }
        }
    }
}

/**
 * The top row: how many sessions, how many active, and the two actions that
 * apply to the set. It lives outside the `LazyColumn` because it is an anchor,
 * not content — scrolling the list must not carry away access to the backups.
 */
@Composable
private fun SummaryBar(
    total: Int,
    active: Int,
    savingAll: Boolean,
    onSaveAll: () -> Unit,
    onOpenBackups: () -> Unit,
) {
    Surface(color = MaterialTheme.colorScheme.surfaceVariant, modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.padding(start = 16.dp, end = 8.dp, top = 6.dp, bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = buildString {
                    append(if (total == 1) "1 session" else "$total sessions")
                    if (active > 0) append(" · $active active" + if (active > 1) "" else "")
                },
                style = MaterialTheme.typography.labelLarge,
                modifier = Modifier.weight(1f),
            )
            if (savingAll) {
                CircularProgressIndicator(modifier = Modifier.size(16.dp), strokeWidth = 2.dp)
                Spacer(modifier = Modifier.width(12.dp))
            } else {
                TextButton(onClick = onSaveAll, modifier = Modifier.testTag(SAVE_ALL_TAG)) {
                    Text(text = "Save all")
                }
            }
            TextButton(onClick = onOpenBackups, modifier = Modifier.testTag(BACKUPS_TAG)) {
                Text(text = "Backups")
            }
        }
    }
}

@Composable
private fun SessionRow(
    session: TerminalSession,
    busySession: Boolean,
    preview: PreviewUiState?,
    canAssign: Boolean,
    onClick: () -> Unit,
    onSave: () -> Unit,
    onRename: () -> Unit,
    onPeek: () -> Unit,
    onAssign: () -> Unit,
    onEnd: () -> Unit,
) {
    val previewOpen = preview != null
    var menuOpen by remember { mutableStateOf(false) }
    val statusColors = vpsmStatusColors

    Card(
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick),
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp),
    ) {
        Row(
            modifier = Modifier.padding(start = 12.dp, end = 4.dp, top = 10.dp, bottom = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                imageVector = VpsmIcons.Terminal,
                contentDescription = null,
                modifier = Modifier.size(20.dp),
                tint = MaterialTheme.colorScheme.primary,
            )
            Spacer(modifier = Modifier.width(12.dp))
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    text = session.name,
                    style = MaterialTheme.typography.titleSmall,
                    // Monospace: this is a terminal session name, and reading
                    // `web-2` without mistaking the `2` for a `z` matters more
                    // here than the elegance of a proportional face.
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = sessionMeta(session),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (session.attached) {
                // Only the ACTIVE state earns a badge. Marking the inactive one
                // too would make seven badges in a list of seven, and seven
                // badges distinguish nothing — that was exactly the repeated
                // "Not attached" of the previous version.
                ActiveBadge(color = statusColors.ok.accent)
                Spacer(modifier = Modifier.width(4.dp))
            }
            if (busySession) {
                CircularProgressIndicator(
                    modifier = Modifier.size(18.dp).padding(end = 2.dp),
                    strokeWidth = 2.dp,
                )
                Spacer(modifier = Modifier.width(10.dp))
            } else {
                Box {
                    IconButton(onClick = { menuOpen = true }) {
                        Icon(
                            imageVector = Icons.Filled.MoreVert,
                            contentDescription = "Actions for session ${session.name}",
                        )
                    }
                    DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                        DropdownMenuItem(
                            text = { Text("Open") },
                            onClick = { menuOpen = false; onClick() },
                        )
                        DropdownMenuItem(
                            text = { Text("Save backup") },
                            onClick = { menuOpen = false; onSave() },
                        )
                        DropdownMenuItem(
                            text = { Text("Rename…") },
                            onClick = { menuOpen = false; onRename() },
                        )
                        DropdownMenuItem(
                            // The label follows the STATE, not the action:
                            // whoever has the preview open reads "Close preview".
                            text = { Text(if (previewOpen) "Close preview" else "Peek") },
                            onClick = { menuOpen = false; onPeek() },
                        )
                        if (canAssign) {
                            DropdownMenuItem(
                                text = { Text("Visible to…") },
                                onClick = { menuOpen = false; onAssign() },
                            )
                        }
                        HorizontalDivider()
                        DropdownMenuItem(
                            // Set apart by a divider and painted in the error
                            // colour: it is the only one here with no way back.
                            text = {
                                Text(
                                    text = "End…",
                                    color = MaterialTheme.colorScheme.error,
                                )
                            },
                            onClick = { menuOpen = false; onEnd() },
                        )
                    }
                }
            }
        }
        if (preview != null) {
            PreviewCard(preview = preview)
        }
    }
}

/**
 * The session's portrait, inside the row's own card.
 *
 * It stays ANCHORED to the session instead of opening a sheet over the top: the
 * question the preview answers is "which of these do I want to open?", and a
 * modal sheet hides precisely the other options being compared.
 *
 * Monospace and horizontally scrollable because it is terminal output —
 * wrapping lines here would throw off tables, progress bars and directory
 * trees, which are half of what you are trying to recognise at a glance.
 */
@Composable
private fun PreviewCard(preview: PreviewUiState) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceVariant,
        modifier = Modifier.fillMaxWidth().padding(start = 12.dp, end = 12.dp, bottom = 10.dp),
        shape = RoundedCornerShape(8.dp),
    ) {
        Box(modifier = Modifier.padding(10.dp)) {
            when (preview) {
                is PreviewUiState.Loading -> Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(modifier = Modifier.size(14.dp), strokeWidth = 2.dp)
                    Spacer(modifier = Modifier.width(8.dp))
                    Text(text = "Reading the session…", style = MaterialTheme.typography.bodySmall)
                }

                is PreviewUiState.Empty -> Text(
                    // "Empty" is information, not failure: a freshly created
                    // session has not written anything yet, and saying so keeps it
                    // from looking like an error.
                    text = "The session has not written anything yet.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )

                is PreviewUiState.Error -> Text(
                    text = preview.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )

                is PreviewUiState.Ready -> Text(
                    text = preview.text,
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    modifier = Modifier.horizontalScroll(rememberScrollState()),
                    softWrap = false,
                )
            }
        }
    }
}


/**
 * Confirmation before killing a session.
 *
 * It states the NAME and it states the CONSEQUENCE, because both are what the
 * person needs in order to decide: killing it kills whatever is running inside
 * — a build, a migration, an agent halfway through a task — and none of it
 * comes back.
 *
 * The confirm button carries the verb ("Kill"), not "OK". An "OK" forces you to
 * re-read the question to know what you are confirming.
 */
@Composable
private fun EndDialog(
    name: String,
    onConfirm: () -> Unit,
    onCancel: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text("End $name?") },
        text = {
            Text(
                "Everything running inside it ends too. " +
                    "This cannot be undone.",
            )
        },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(text = "End", color = MaterialTheme.colorScheme.error)
            }
        },
        dismissButton = {
            TextButton(onClick = onCancel) { Text("Cancel") }
        },
    )
}

/**
 * Choice of audience: who the session shows up for.
 *
 * It is a LIST and not a text field — a project rule, and one with teeth here:
 * the server accepts any target, so a name with one character wrong would make
 * the session vanish from everybody's list, with no error at all.
 *
 * "Everyone" comes last and with its explanation attached: it is the option
 * that changes the outcome the most, and "*" says nothing to anyone who does
 * not know the convention.
 */
@Composable
private fun AssignSheet(
    name: String,
    targets: List<String>,
    onChoose: (String) -> Unit,
    onClose: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onClose,
        title = { Text("Who sees $name?") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                targets.forEach { target ->
                    val isAll = target == TARGET_ALL
                    TextButton(
                        onClick = { onChoose(target) },
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text(
                                text = if (isAll) "Everyone" else target,
                                style = MaterialTheme.typography.bodyLarge,
                            )
                            if (isAll) {
                                Text(
                                    text = "Shows up in every account's list",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onClose) { Text("Cancel") }
        },
    )
}

@Composable
private fun ActiveBadge(color: Color) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(modifier = Modifier.size(7.dp).clip(CircleShape).background(color))
        Spacer(modifier = Modifier.width(5.dp))
        Text(
            text = "active",
            style = MaterialTheme.typography.labelSmall,
            color = color,
        )
    }
}

/**
 * The meta row: the session's age and the originating tab, where there is one.
 *
 * The age is what answers "is this yesterday's or just now?" in a list of
 * similar names, and it is the only temporal information the server sends
 * today — `created` exists in the BFF's session summary, "last activity" does
 * not.
 */
private fun sessionMeta(session: TerminalSession): String {
    val parts = mutableListOf(readableAge(session.created))
    // The tab only goes in when it says something the name does not. On the
    // server it is filled with the session's own name in most cases, and
    // repeating "pouco1 · pouco1" spends the entire meta row to convey nothing —
    // which is exactly how the first version of this screen turned out.
    session.tab
        ?.takeIf { it.isNotBlank() && !it.equals(session.name, ignoreCase = true) }
        ?.let { parts += it }
    return parts.joinToString(" · ")
}

/**
 * "3 h ago", "2 d ago". Seconds become "now": a terminal opened 40 s ago and one
 * opened 55 s ago are the same thing to somebody choosing which to open, and a
 * clock ticking in the list would only draw attention to what does not matter.
 */
internal fun readableAge(createdAtSeconds: Long, nowSeconds: Long = System.currentTimeMillis() / 1000): String {
    val delta = (nowSeconds - createdAtSeconds).coerceAtLeast(0)
    return when {
        createdAtSeconds <= 0L -> "—"
        delta < 60 -> "now"
        delta < 3_600 -> "${delta / 60} min ago"
        delta < 86_400 -> "${delta / 3_600} h ago"
        delta < 2_592_000 -> "${delta / 86_400} d ago"
        else -> "${delta / 2_592_000} mo ago"
    }
}

/** At the end of the list, not at the top: what you do here 9 times out of 10 is open a session that already exists. */
@Composable
private fun NewSessionCard(onAttach: (String) -> Unit) {
    Card(
        modifier = Modifier.fillMaxWidth().padding(top = 6.dp),
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
    ) {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    imageVector = Icons.Filled.Add,
                    contentDescription = null,
                    modifier = Modifier.size(16.dp),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(modifier = Modifier.width(6.dp))
                Text(text = "New session", style = MaterialTheme.typography.labelLarge)
            }
            NewSessionField(onAttach = onAttach)
        }
    }
}

@Composable
private fun NewSessionField(onAttach: (String) -> Unit) {
    var name by rememberSaveable { mutableStateOf("") }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedTextField(
            value = name,
            onValueChange = { name = it },
            label = { Text(text = "Session name") },
            singleLine = true,
            modifier = Modifier.weight(1f),
        )
        TextButton(
            enabled = name.isNotBlank(),
            onClick = { onAttach(name.trim()) },
        ) {
            Text(text = "Attach")
        }
    }
}

@Composable
private fun RenameDialog(currentName: String, onConfirm: (String) -> Unit, onCancel: () -> Unit) {
    var next by remember { mutableStateOf(currentName) }
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text(text = "Rename session") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = next,
                    onValueChange = { next = it },
                    label = { Text(text = "New name") },
                    singleLine = true,
                )
                Text(
                    text = "The session keeps running; only the name changes.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            TextButton(
                enabled = next.isNotBlank() && next != currentName,
                onClick = { onConfirm(next.trim()) },
            ) { Text(text = "Rename") }
        },
        dismissButton = { TextButton(onClick = onCancel) { Text(text = "Cancel") } },
    )
}

/**
 * The session chips inside a backup, used by the sheet. It lives here because
 * it shares the list's visual language.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun SessionChips(names: List<String>, onTap: (String) -> Unit) {
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        names.forEach { name ->
            AssistChip(
                onClick = { onTap(name) },
                label = {
                    Text(
                        text = name,
                        style = MaterialTheme.typography.labelSmall,
                        fontFamily = FontFamily.Monospace,
                        maxLines = 1,
                    )
                },
                colors = AssistChipDefaults.assistChipColors(),
            )
        }
    }
}

internal const val SAVE_ALL_TAG = "sessoes-salvar-tudo"
internal const val BACKUPS_TAG = "sessoes-backups"
