package dev.servercontrolpanel.feature.auth

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.TextButton
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalContext
import dev.servercontrolpanel.feature.auth.dashboard.DashboardTile
import dev.servercontrolpanel.feature.auth.dashboard.ChosenTiles
import dev.servercontrolpanel.feature.auth.dashboard.ScrollToTopRequest
import dev.servercontrolpanel.feature.auth.dashboard.TileCatalog
import dev.servercontrolpanel.feature.auth.dashboard.TileGrid
import dev.servercontrolpanel.feature.auth.dashboard.visibleTiles
import dev.servercontrolpanel.feature.auth.dashboard.tileCatalog
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.DashboardTarget
import dev.servercontrolpanel.data.widget.StoredSummary
import dev.servercontrolpanel.data.widget.summaryOf
import dev.servercontrolpanel.designsystem.panelStatusColors
import kotlinx.coroutines.delay
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import kotlin.math.abs

@Composable
fun HomeScreen(
    onOpenSecurity: () -> Unit = {},
    onOpenDiagnostics: () -> Unit = {},
    modifier: Modifier = Modifier,
    viewModel: HomeViewModel = viewModel(),
    onOpenSection: (String) -> Unit = {},
    onOpenTerminal: () -> Unit = {},
    autoRefreshMillis: Long = HOME_AUTO_REFRESH_MILLIS,
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()

    val context = LocalContext.current
    LaunchedEffect(context) {
        viewModel.publishSummaryWith { snapshot ->
            StoredSummary.persist(context, summaryOf(snapshot))
        }
    }

    AutoRefreshWhileResumed(intervalMillis = autoRefreshMillis, onTick = viewModel::autoRefresh)

    HomeDashboard(
        state = state,
        onRetry = viewModel::load,
        onRefresh = viewModel::refresh,
        onTarget = { target ->
            val sectionId = target.sectionId
            if (sectionId != null) onOpenSection(sectionId) else onOpenTerminal()
        },
        modifier = modifier,
        onOpenSecurity = onOpenSecurity,
        onOpenDiagnostics = onOpenDiagnostics,
    )
}

@Composable
private fun AutoRefreshWhileResumed(intervalMillis: Long, onTick: () -> Unit) {
    if (intervalMillis <= 0) return
    val tick by rememberUpdatedState(onTick)
    val lifecycleOwner = LocalLifecycleOwner.current
    var resumed by remember { mutableStateOf(false) }

    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> resumed = true
                Lifecycle.Event.ON_PAUSE -> resumed = false
                else -> Unit
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    LaunchedEffect(resumed, intervalMillis) {
        if (!resumed) return@LaunchedEffect
        while (true) {
            delay(intervalMillis)
            tick()
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun HomeDashboard(
    state: HomeUiState,
    onRetry: () -> Unit,
    onRefresh: () -> Unit,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
    onOpenSecurity: () -> Unit = {},
    onOpenDiagnostics: () -> Unit = {},
) {
    PullToRefreshBox(
        isRefreshing = (state as? HomeUiState.Success)?.refreshing == true,
        onRefresh = onRefresh,
        modifier = modifier.fillMaxSize(),
    ) {
        when (state) {
            is HomeUiState.Loading -> HomeLoading()
            is HomeUiState.Error -> HomeError(message = state.message, onRetry = onRetry)
            is HomeUiState.Success -> DashboardContent(
                snapshot = state.snapshot,
                staleError = state.staleError,
                onRetry = onRetry,
                onTarget = onTarget,
                onOpenSecurity = onOpenSecurity,
                onOpenDiagnostics = onOpenDiagnostics,
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DashboardContent(
    snapshot: DashboardSnapshot,
    staleError: String?,
    onRetry: () -> Unit,
    onTarget: (DashboardTarget) -> Unit,
    onOpenSecurity: () -> Unit = {},
    onOpenDiagnostics: () -> Unit = {},
) {
    val warning = snapshot.attention
    val context = LocalContext.current

    val scroll = rememberLazyListState()
    val scrollToTopRequest by ScrollToTopRequest.counter.collectAsStateWithLifecycle()
    LaunchedEffect(scrollToTopRequest) {
        if (scrollToTopRequest > 0) scroll.animateScrollToItem(0)
    }
    val catalog = remember(snapshot) { tileCatalog(snapshot) }
    var chosen by remember { mutableStateOf(ChosenTiles.read(context)) }
    var editing by rememberSaveable { mutableStateOf(false) }
    var catalogOpen by rememberSaveable { mutableStateOf(false) }
    val visible = remember(catalog, chosen) { visibleTiles(catalog, chosen) }

    if (catalogOpen) {
        TileCatalog(
            catalog = catalog,
            chosen = chosen,
            atCap = chosen.size >= ChosenTiles.MAX,
            onChoose = { tile ->
                chosen = ChosenTiles.append(context, tile.id)
            },
            onClose = { catalogOpen = false },
        )
    }

    LazyColumn(
        state = scroll,
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item("stamp") {
            FreshnessLine(
                fetchedAtEpochMs = snapshot.fetchedAtEpochMs,
                staleError = staleError,
                onRetry = onRetry,
            )
        }
        item("panel") {
            TileDashboard(
                tiles = visible,
                editing = editing,
                hasChoice = chosen.isNotEmpty(),
                onToggleEditing = { editing = !editing },
                onTap = { onTarget(it.target) },
                onRemove = { chosen = ChosenTiles.remove(context, it.id) },
                onRequestCatalog = { catalogOpen = true },
            )
        }
        if (warning.isNotEmpty()) {
            item("warning") { AttentionCard(signals = warning, onTarget = onTarget) }
        }
        if (snapshot.resourceSignals.isEmpty()) {
            item("resources-missing") { ResourcesUnavailableCard() }
        }
        item("actions") { QuickActionsCard(onTarget = onTarget) }
        item("session") {
            SessionCard(
                snapshot = snapshot,
                driftText = clockDriftText(snapshot),
                onOpenSecurity = onOpenSecurity,
                onOpenDiagnostics = onOpenDiagnostics,
            )
        }
    }
}

@Composable
private fun FreshnessLine(fetchedAtEpochMs: Long, staleError: String?, onRetry: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "updated at ${timeOf(fetchedAtEpochMs)}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = "pull to refresh",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (staleError != null) {
            val warning = panelStatusColors.warning
            Card(
                colors = CardDefaults.cardColors(containerColor = warning.container),
                elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
            ) {
                Row(
                    modifier = Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        imageVector = Icons.Filled.Warning,
                        contentDescription = null,
                        tint = warning.accent,
                        modifier = Modifier.size(18.dp),
                    )
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            text = "The numbers below are from ${timeOf(fetchedAtEpochMs)}",
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.SemiBold,
                            color = warning.content,
                        )
                        Text(
                            text = staleError,
                            style = MaterialTheme.typography.bodySmall,
                            color = warning.content,
                        )
                    }
                    Button(onClick = onRetry) { Text("Reload") }
                }
            }
        }
    }
}

@Composable
private fun ResourcesUnavailableCard() {
    DashboardCard(title = "Resources", subtitle = "unavailable") {
        Text(
            text = "This server does not expose CPU, memory and disk in /ops/status. " +
                "Update Server Control Panel to see resources here.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun HomeLoading() {
    LazyColumn(
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                CircularProgressIndicator(modifier = Modifier.size(18.dp))
                Text(
                    text = "Loading the dashboard…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        items(listOf("Quick actions", "Session")) { title ->
            Card(
                modifier = Modifier.fillMaxWidth(),
                colors = CardDefaults.cardColors(
                    containerColor = MaterialTheme.colorScheme.surfaceContainer,
                ),
                elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = title,
                        style = MaterialTheme.typography.titleMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Text(
                        text = "…",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun HomeError(message: String, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Card(
            colors = CardDefaults.cardColors(
                containerColor = MaterialTheme.colorScheme.surfaceContainer,
            ),
            elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
        ) {
            Column(
                modifier = Modifier.padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(
                    text = "Can't reach the server",
                    style = MaterialTheme.typography.titleLarge,
                )
                Text(text = message, style = MaterialTheme.typography.bodyMedium)
                Text(
                    text = "Check the device's network and the server address in Settings. " +
                        "The dashboard comes back on its own as soon as the connection responds.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Button(onClick = onRetry) {
                    Icon(
                        imageVector = Icons.Filled.Refresh,
                        contentDescription = null,
                        modifier = Modifier.size(18.dp),
                    )
                    Text(text = "  Try again")
                }
            }
        }
    }
}

internal fun timeOf(epochMillis: Long): String =
    Instant.ofEpochMilli(epochMillis)
        .atZone(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofPattern("HH:mm:ss"))

private const val CLOCK_DRIFT_SECONDS = 60L

internal fun clockDriftText(snapshot: DashboardSnapshot): String? {
    val system = snapshot.ops.system ?: return null
    val deviceSeconds = snapshot.fetchedAtEpochMs / 1_000
    val drift = deviceSeconds - system.serverTimeEpoch
    if (abs(drift) < CLOCK_DRIFT_SECONDS) return null
    val direction = if (drift > 0) "behind the" else "ahead of the"
    return "Server clock ${abs(drift)} s $direction device"
}


@Composable
private fun TileDashboard(
    tiles: List<DashboardTile>,
    editing: Boolean,
    hasChoice: Boolean,
    onToggleEditing: () -> Unit,
    onTap: (DashboardTile) -> Unit,
    onRemove: (DashboardTile) -> Unit,
    onRequestCatalog: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = if (editing) "Building the dashboard" else "Dashboard",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
            )
            TextButton(onClick = onToggleEditing) {
                Text(text = if (editing) "Done" else "Customize")
            }
        }
        if (tiles.isEmpty() && !editing) {
            OutlinedCard(
                modifier = Modifier.fillMaxWidth().clickable(onClick = onRequestCatalog),
            ) {
                Column(
                    modifier = Modifier.fillMaxWidth().padding(20.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Text(text = "+  Pick the first tile", style = MaterialTheme.typography.titleSmall)
                    Text(
                        text = if (hasChoice) {
                            "None of the chosen tiles is available right now."
                        } else {
                            "The dashboard starts empty on purpose: you choose what you look at every day."
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        textAlign = androidx.compose.ui.text.style.TextAlign.Center,
                    )
                }
            }
        } else {
            TileGrid(
                tiles = tiles,
                editing = editing,
                onTap = onTap,
                onRemove = onRemove,
                onRequestCatalog = onRequestCatalog,
            )
        }
    }
}
