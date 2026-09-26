package com.vpsmanager.feature.admin

import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import com.vpsmanager.sdui.SduiScreen
import com.vpsmanager.sdui.registry.LocalActionRunner
import com.vpsmanager.sdui.registry.LocalScreenState

/**
 * Administration's host: the LAUNCHER when no section is open, and the chosen
 * section — rendered by `:sdui` — when there is one.
 *
 * Three routes to a section, serving different situations:
 *
 * | route | for when |
 * |---|---|
 * | [AdminLauncher] (the grid) | you recognise it by shape, without reading |
 * | the search inside it | you know the name, not where it is |
 * | [CommandPalette] | you are INSIDE a section and want another |
 *
 * All three read the same catalogue and use the same `filterSections`, so they
 * never disagree about what a term finds.
 *
 * THE SECTION LIST COMES FROM THE SERVER (`GET /screens`), never from a
 * constant in here — that is what lets a new screen registered on the server
 * show up on the phone with no release. Before this version the drawer's
 * "Admin" destination opened a hard-coded `scheduler.jobs`, and the other 24
 * screens the server was already serving were unreachable from the phone.
 *
 * [sectionId] is the `admin/{sectionId}` route argument and is still honoured
 * (a notification deep link points at a concrete section);
 * [ADMIN_SECTION_AUTO] means "nobody named a section" and resolves to NULL,
 * which is the LAUNCHER — not "the first section the server offers". The
 * difference matters: opening an arbitrary section was exactly the defect that
 * hid the catalogue's other 24.
 *
 * This composable still does not inspect WHICH components a screen contains
 * and does not branch on anything section-specific — a section id remains
 * opaque data, and a new section remains a server-only change.
 */
@Composable
fun AdminScreen(
    sectionId: String,
    modifier: Modifier = Modifier,
    catalogViewModel: AdminCatalogViewModel = adminCatalogViewModel(sectionId),
) {
    val catalog by catalogViewModel.uiState.collectAsStateWithLifecycle()
    var paletteOpen by remember { mutableStateOf(false) }
    val focus = remember { FocusRequester() }

    // The shortcut exists because this app is already operated with a
    // Bluetooth keyboard — it is the same hardware path the terminal uses. It
    // is NEVER the only door: the bar's button is still there, and a palette
    // that only opens by shortcut is a feature only those who already know it
    // will use.
    //
    // onPreviewKeyEvent, not onKeyEvent: the palette has to win against any
    // text field that holds focus inside the rendered section.
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }

    Column(
        modifier = modifier
            .fillMaxSize()
            .focusRequester(focus)
            .focusable()
            .onPreviewKeyEvent { event ->
                val shortcut = event.type == KeyEventType.KeyDown &&
                    event.isCtrlPressed &&
                    event.key == Key.K
                if (shortcut) paletteOpen = true
                shortcut
            },
    ) {
        when (val state = catalog) {
            is AdminCatalogState.Loading -> LoadingState()

            // A failure to list the sections does NOT take the whole screen
            // down in silence: it is stated, with a button to try again.
            is AdminCatalogState.Error ->
                ErrorState(message = state.message, onRetry = state.retry)

            is AdminCatalogState.Ready -> {
                val chosen = state.selectedId
                when {
                    // An empty catalogue is not the same as the launcher: the
                    // server offered this user no section at all, and an empty
                    // grid would look like a defect.
                    state.sections.isEmpty() -> AdminCatalogEmpty()

                    chosen == null -> AdminLauncher(
                        sections = state.sections,
                        query = state.query,
                        recents = state.recents,
                        onQueryChange = catalogViewModel::search,
                        onSelect = catalogViewModel::select,
                    )

                    else -> {
                        SectionBar(
                            title = state.selected?.label ?: chosen,
                            group = state.selected?.group,
                            onBack = catalogViewModel::backToLauncher,
                            onOpenPalette = { paletteOpen = true },
                        )
                        // key(): each section gets its own AdminSectionViewModel.
                        // Without it, switching section would reuse the previous
                        // one's ViewModel and the new screen would be born
                        // holding the old screen's state.
                        AdminSectionContent(sectionId = chosen)
                    }
                }

                if (paletteOpen) {
                    CommandPalette(
                        sections = state.sections,
                        recents = state.recents,
                        onSelect = { id ->
                            paletteOpen = false
                            catalogViewModel.select(id)
                        },
                        onDismiss = { paletteOpen = false },
                    )
                }
            }
        }
    }
}

/**
 * Builds the [AdminCatalogViewModel] already wired to this device's recents.
 *
 * Outside [AdminScreen] because it is the only line in the file that touches a
 * `Context`: the Compose test builds the ViewModel with doubles and never
 * comes through here.
 */
@Composable
private fun adminCatalogViewModel(sectionId: String): AdminCatalogViewModel {
    val context = LocalContext.current.applicationContext
    return viewModel(
        factory = viewModelFactory {
            initializer {
                AdminCatalogViewModel(
                    initialRoute = sectionId,
                    readRecents = { AdminRecents.read(context) },
                    writeRecent = { id -> AdminRecents.registrar(context, id) },
                )
            }
        },
    )
}

/**
 * The open section's bar: where to go back to, where you are, and the shortcut
 * to another section without going through the launcher.
 *
 * The palette button lives here, and not on the launcher, because here is
 * where it solves something: on the launcher the search is already on screen,
 * and two search fields on one screen would be the same question asked twice.
 */
@Composable
private fun SectionBar(
    title: String,
    group: String?,
    onBack: () -> Unit,
    onOpenPalette: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 4.dp, end = 4.dp, top = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onBack) {
            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back to sections")
        }
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (group != null) {
                Text(
                    text = group,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        IconButton(onClick = onOpenPalette) {
            Icon(Icons.Filled.Search, contentDescription = PALETTE_OPEN_DESCRIPTION)
        }
    }
}

/**
 * A rendered section — the thin host that existed before there was a picker.
 * It still knows nothing about the section it is drawing.
 *
 * `internal`, with an injectable [viewModel], because it is THIS composable
 * and not the whole [AdminScreen] that maps [AdminUiState] to pixels: a test
 * that wants to exercise "a 404 becomes a quiet notice" should not have to
 * stand up a fake catalogue as well.
 */
@Composable
internal fun AdminSectionContent(
    sectionId: String,
    modifier: Modifier = Modifier,
    viewModel: AdminViewModel = viewModel(
        // The key is what gives one ViewModel per section inside the same host.
        key = sectionId,
        factory = viewModelFactory { initializer { AdminViewModel(sectionId = sectionId) } },
    ),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    when (val current = state) {
        is AdminUiState.Loading -> LoadingState(modifier)

        // An unavailable section is a NORMAL state (the permission may have
        // changed between the listing and the tap), not a frightening error —
        // which is why the notice is quiet and the picker stays above it,
        // ready for another choice. See AdminUiState.Unavailable.
        is AdminUiState.Unavailable ->
            UnavailableState(message = current.message, onRetry = current.retry, modifier = modifier)

        is AdminUiState.Error -> ErrorState(message = current.message, onRetry = current.retry, modifier = modifier)

        is AdminUiState.Ready -> CompositionLocalProvider(
            LocalActionRunner provides current.actionRunner,
            LocalScreenState provides current.screenState,
        ) {
            SduiScreen(
                envelope = current.envelope,
                modifier = modifier,
                onOutcome = viewModel::handleOutcome,
                refreshKey = current.refreshTick,
            )
        }
    }
}

@Composable
private fun LoadingState(modifier: Modifier = Modifier) {
    Column(
        modifier = modifier.fillMaxSize().padding(16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ErrorState(message: String, onRetry: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier = modifier.fillMaxSize().padding(16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    ) {
        Text(text = message, style = MaterialTheme.typography.bodyLarge)
        Button(onClick = onRetry) { Text("Try again") }
    }
}

/**
 * A section's 404, stated without drama.
 *
 * A `GET /screens/{id}` that returns 404 almost always means "this account
 * does not have (or no longer has) access to this section" — the server
 * answers 404 rather than 403 on purpose, so as not to confirm the screen
 * exists to someone who may not see it. From the user's side that is not a
 * failure: it is a section that is not theirs. Treating it as a red error
 * would teach them to ignore real ones.
 */
@Composable
private fun UnavailableState(message: String, onRetry: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier = modifier.fillMaxWidth().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(text = "Section unavailable", style = MaterialTheme.typography.titleMedium)
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        TextButton(onClick = onRetry, modifier = Modifier.padding(top = 4.dp)) {
            Text("Reload")
        }
    }
}
