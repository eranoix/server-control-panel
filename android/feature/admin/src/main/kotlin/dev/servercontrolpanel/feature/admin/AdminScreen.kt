package dev.servercontrolpanel.feature.admin

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
import dev.servercontrolpanel.sdui.SduiScreen
import dev.servercontrolpanel.sdui.registry.LocalActionRunner
import dev.servercontrolpanel.sdui.registry.LocalScreenState

@Composable
fun AdminScreen(
    sectionId: String,
    modifier: Modifier = Modifier,
    catalogViewModel: AdminCatalogViewModel = adminCatalogViewModel(sectionId),
) {
    val catalog by catalogViewModel.uiState.collectAsStateWithLifecycle()
    var paletteOpen by remember { mutableStateOf(false) }
    val focus = remember { FocusRequester() }

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

            is AdminCatalogState.Error ->
                ErrorState(message = state.message, onRetry = state.retry)

            is AdminCatalogState.Ready -> {
                val chosen = state.selectedId
                when {
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

@Composable
internal fun AdminSectionContent(
    sectionId: String,
    modifier: Modifier = Modifier,
    viewModel: AdminViewModel = viewModel(
        key = sectionId,
        factory = viewModelFactory { initializer { AdminViewModel(sectionId = sectionId) } },
    ),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    when (val current = state) {
        is AdminUiState.Loading -> LoadingState(modifier)

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
