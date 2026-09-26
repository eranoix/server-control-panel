package com.vpsmanager.feature.terminal.ui

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
import com.vpsmanager.data.terminal.TerminalSession

/** Test tag for the sessions sheet. */
const val SESSIONS_SHEET_TAG = "sessoes-sheet"

/** Test tag for the sheet's filter field. */
const val SESSIONS_FILTER_TAG = "sessoes-filtro"

/** Test tag for the new-session name field. */
const val SESSIONS_NEW_TAG = "sessoes-nova"

/** Prefix of the test tag on each session row. */
fun sessionTag(name: String): String = "sessao-$name"

/**
 * The session-switching sheet, opened by the "Session" button in the top bar.
 *
 * ## Why a bottom sheet, and not a drawer, tabs or a menu
 *
 * The app's owner has 20+ sessions (`Aplicativo`, `Main`, `Servidor`, `Vpsm`,
 * `proxy`, `tt`…), and the decision was taken by looking at what mobile
 * terminal apps chose:
 *
 * - **A tab strip is out.** Blink Shell, which is the app with the model
 *   closest to tabs (`UIPageViewController`, one tab per shell), draws NO tab
 *   strip at all on the phone: it exposes switching by side swipe and by
 *   `Cmd+number`. Twenty tabs on a 6" screen show four at a time, and every
 *   switch becomes a horizontal hunt.
 * - **A dropdown menu is out.** A Material 3 menu is for a short list of
 *   options; 20 items anchored to the top bar become a column with no room
 *   for each session's state (attached / not attached), which is precisely
 *   what Termux proves to be necessary (it strikes through the row of a dead
 *   session and paints in red the one that exited with an error).
 * - **A side drawer is what Termux does** (a 240 dp `DrawerLayout`, opened by
 *   swiping from the left edge). It works there, but here the edge swipe
 *   competes with Android's predictive back and with the grid's own gestures
 *   — and 240 dp give each row less width than a full-width sheet.
 * - **A bottom sheet wins in this app**: it is anchored to an explicit button
 *   (no gesture to be discovered), it sits within thumb reach on a tall
 *   phone, it scrolls naturally, it gives full width to `[n] name + state`
 *   and it accommodates an action pinned to the footer.
 *
 * ## Details copied from those who solved it already
 *
 * - **The `[n]` index at the start of the row**, as in Termux
 *   (`TermuxSessionsListViewController.getView`): it is what sustains muscle
 *   memory and, later on, a numeric shortcut.
 * - **A filter field at the top.** Termux never needed one (it caps at
 *   `MAX_SESSIONS = 8`); with 20+ sessions, a list with no filter is a list
 *   you scroll through with your thumb.
 * - **"New session" PINNED TO THE FOOTER**, not as the first row of the list.
 *   It is literally what `activity_termux.xml` does: the `ListView` takes
 *   `layout_weight="1"` and the button sits in a `buttonBarStyle` BELOW it.
 *   With 20+ sessions, a create action placed as the first row scrolls off
 *   the screen and sits exactly where the thumb lands when reaching for
 *   session number 1.
 * - **Instant switching, with no animation on the grid.** Termux switches in
 *   `setCurrentSession` → `attachSession`, with no transition, and announces
 *   by toast which session came in. Blink animates because ITS switch is
 *   literally a page turn. Here the grid is a blit of cells: cross-fading
 *   between two buffers would fight the rasteriser and read as slowness. It
 *   is the SHEET's closing that is animated, never the grid.
 *
 * ## What this sheet does NOT do, and why
 *
 * **Rename and kill were left out.** Not by design: the mobile BFF today
 * exposes only `GET /terminal/sessions`
 * (`internal/mobilebff/handlers_terminal.go`) — there is no kill or rename
 * route. Putting them in the sheet now would mean drawing buttons that answer
 * 404 on the device. Killing is destructive and cannot be a button that
 * "sometimes works".
 *
 * **Detach is here** because it needs no route at all: the session lives on
 * the server, under `dtach`; leaving the screen ALREADY is detaching. The item
 * exists to say so in words — the doubt "if I leave, do I lose what is
 * running?" is the reason someone never leaves.
 */
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

/**
 * The sheet's content, split from the wrapper for the same reason as in
 * [TerminalOptionsSheet]: the wrapper is a system window, the content is what
 * has rules and can be exercised in a JVM test without waiting on a window
 * animation.
 */
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
                // The filter only appears once there is list enough to
                // justify it: on a handful of sessions it is one more field
                // between the thumb and the target.
                if (state.sessions.size > FILTER_THRESHOLD) {
                    OutlinedTextField(
                        value = filter,
                        onValueChange = { filter = it },
                        label = { Text(text = "Filter") },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth().testTag(SESSIONS_FILTER_TAG),
                    )
                }
                // `heightIn` with a cap: the list scrolls INSIDE the sheet,
                // and the create footer never leaves the screen — which is
                // the whole property of a pinned footer.
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

        // Detach says what the back arrow already does, spelled out in full:
        // the session stays alive on the server. It is the answer to the doubt
        // that makes someone never leave the screen.
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

/** Above this the list stops being thumb-scrollable and gains a filter. */
private const val FILTER_THRESHOLD = 8

private val MAX_LIST_HEIGHT = 340.dp
