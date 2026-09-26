package com.vpsmanager.feature.admin.ops

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import com.vpsmanager.data.events.MobileEventsClient
import com.vpsmanager.data.ops.OpsAlert
import com.vpsmanager.data.ops.OpsSnapshot

/**
 * Entry point for the operations dashboard. [mobileEventsClient] is the app-scoped singleton
 * constructed in `VpsManagerApplication` — this module never casts it out of a `Context`, it is
 * threaded in explicitly by whatever screen hosts this route.
 */
@Composable
fun OpsDashboardRoute(
    mobileEventsClient: MobileEventsClient,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val viewModel: OpsDashboardViewModel = viewModel(
        factory = viewModelFactory {
            initializer { OpsDashboardViewModel(eventsClient = mobileEventsClient) }
        },
    )
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()
    OpsDashboardScreen(
        uiState = uiState,
        onBack = onBack,
        onRetry = viewModel::refresh,
        modifier = modifier,
    )
}

/** Stateless — every dependency is a parameter, so this renders/tests without a real ViewModel. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun OpsDashboardScreen(
    uiState: OpsDashboardUiState,
    onBack: () -> Unit,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text(text = "Operations dashboard") },
                navigationIcon = { TextButton(onClick = onBack) { Text(text = "Back") } },
            )
        },
    ) { innerPadding ->
        Box(modifier = Modifier.fillMaxSize().padding(innerPadding)) {
            when (uiState) {
                is OpsDashboardUiState.Loading -> {
                    CircularProgressIndicator(modifier = Modifier.padding(24.dp))
                }
                is OpsDashboardUiState.LoadError -> {
                    Column(modifier = Modifier.fillMaxWidth().padding(16.dp)) {
                        Text(text = uiState.message)
                        TextButton(onClick = onRetry) { Text(text = "Try again") }
                    }
                }
                is OpsDashboardUiState.Success -> {
                    OpsSnapshotSections(snapshot = uiState.snapshot)
                }
            }
        }
    }
}

@Composable
private fun OpsSnapshotSections(snapshot: OpsSnapshot) {
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        item {
            ExpandableSection(title = "Health checks (${if (snapshot.healthOk) "all ok" else "failures"})") {
                Column {
                    snapshot.health.forEach { (name, status) ->
                        Row(
                            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
                            horizontalArrangement = Arrangement.SpaceBetween,
                        ) {
                            Text(text = name, modifier = Modifier.weight(1f))
                            Text(text = status)
                        }
                        HorizontalDivider()
                    }
                }
            }
        }
        item {
            ExpandableSection(title = "Queue (${snapshot.queueRunning} running, ${snapshot.queueQueued} waiting)") {
                Row(modifier = Modifier.fillMaxWidth().padding(16.dp)) {
                    Text(text = "Running jobs: ${snapshot.queueRunning}")
                }
                Row(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)) {
                    Text(text = "Queued jobs: ${snapshot.queueQueued}")
                }
            }
        }
        item {
            ExpandableSection(title = "Active alerts (${snapshot.alerts.size})", initiallyExpanded = snapshot.alerts.isNotEmpty()) {
                if (snapshot.alerts.isEmpty()) {
                    Text(text = "No alerts firing right now.", modifier = Modifier.fillMaxWidth().padding(16.dp))
                } else {
                    Column {
                        snapshot.alerts.forEach { alert ->
                            AlertRow(alert = alert)
                            HorizontalDivider()
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun AlertRow(alert: OpsAlert) {
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
        Text(text = "${alert.name} — ${alert.severity} (${alert.state})")
        val unit = alert.unit.orEmpty()
        Text(text = "Current value: ${alert.currentValue}$unit · threshold: ${alert.threshold}$unit")
    }
}

/**
 * Local grouped/expandable section — no equivalent exists yet in `:design-system`, so this stays
 * private to the ops dashboard until a shared component is extracted.
 */
@Composable
private fun ExpandableSection(
    title: String,
    initiallyExpanded: Boolean = true,
    content: @Composable () -> Unit,
) {
    var expanded by remember { mutableStateOf(initiallyExpanded) }
    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable { expanded = !expanded }
                .padding(16.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(text = title, modifier = Modifier.weight(1f))
            Text(text = if (expanded) "▲" else "▼")
        }
        AnimatedVisibility(visible = expanded) {
            Column { content() }
        }
        HorizontalDivider()
    }
}
