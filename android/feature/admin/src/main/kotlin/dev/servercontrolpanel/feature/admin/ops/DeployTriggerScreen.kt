package dev.servercontrolpanel.feature.admin.ops

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.platform.LocalContext
import dev.servercontrolpanel.data.ops.progress.DeployWatchWorker
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import dev.servercontrolpanel.data.events.MobileEventsClient

@Composable
fun DeployTriggerRoute(
    mobileEventsClient: MobileEventsClient,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val viewModel: DeployTriggerViewModel = viewModel(
        factory = viewModelFactory {
            initializer {
                DeployTriggerViewModel(
                    eventsClient = mobileEventsClient,
                    trackOffScreen = { jobId ->
                        DeployWatchWorker.track(context, jobId, app = "")
                    },
                )
            }
        },
    )
    val uiState by viewModel.uiState.collectAsStateWithLifecycle()
    val isAdmin by viewModel.isAdmin.collectAsStateWithLifecycle()
    DeployTriggerScreen(
        uiState = uiState,
        isAdmin = isAdmin,
        onBack = onBack,
        onRequestConfirmation = viewModel::requestConfirmation,
        onDismissConfirmation = viewModel::dismissConfirmation,
        onConfirmDeploy = viewModel::confirmDeploy,
        modifier = modifier,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeployTriggerScreen(
    uiState: DeployTriggerUiState,
    isAdmin: Boolean?,
    onBack: () -> Unit,
    onRequestConfirmation: () -> Unit,
    onDismissConfirmation: () -> Unit,
    onConfirmDeploy: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text(text = "Trigger deploy") },
                navigationIcon = { TextButton(onClick = onBack) { Text(text = "Back") } },
            )
        },
    ) { innerPadding ->
        Box(modifier = Modifier.fillMaxSize().padding(innerPadding)) {
            when (isAdmin) {
                null -> CircularProgressIndicator(modifier = Modifier.padding(24.dp))
                false -> Text(
                    text = "This action is restricted to administrators.",
                    modifier = Modifier.fillMaxWidth().padding(16.dp),
                )
                true -> DeployTriggerContent(
                    uiState = uiState,
                    onRequestConfirmation = onRequestConfirmation,
                    onDismissConfirmation = onDismissConfirmation,
                    onConfirmDeploy = onConfirmDeploy,
                )
            }
        }
    }
}

@Composable
private fun DeployTriggerContent(
    uiState: DeployTriggerUiState,
    onRequestConfirmation: () -> Unit,
    onDismissConfirmation: () -> Unit,
    onConfirmDeploy: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        when (uiState) {
            is DeployTriggerUiState.Idle -> {
                Button(onClick = onRequestConfirmation) { Text(text = "Trigger deploy") }
            }
            is DeployTriggerUiState.AwaitingConfirmation -> {
                Button(onClick = onRequestConfirmation, enabled = false) { Text(text = "Trigger deploy") }
                DeployConfirmationDialog(onConfirm = onConfirmDeploy, onDismiss = onDismissConfirmation)
            }
            is DeployTriggerUiState.TriggerFailed -> {
                Text(text = uiState.message, modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp))
                Button(onClick = onRequestConfirmation) { Text(text = "Try again") }
            }
            is DeployTriggerUiState.InProgress -> {
                DeployInProgressContent(uiState)
            }
            is DeployTriggerUiState.Outcome -> {
                DeployOutcomeContent(uiState, onRequestConfirmation)
            }
        }
    }
}

@Composable
private fun DeployInProgressContent(state: DeployTriggerUiState.InProgress) {
    Column(modifier = Modifier.fillMaxWidth()) {
        if (state.phase == "queued") {
            Text(text = "Queued — this can take up to 10 minutes if another deploy is in progress.")
        } else {
            Text(text = "Current phase: ${state.phase}")
        }
        state.step?.let { step -> Text(text = "Step: $step") }
        LinearProgressIndicator(
            progress = { state.progress / 100f },
            modifier = Modifier.fillMaxWidth().padding(vertical = 12.dp),
        )
        Text(text = "Log", modifier = Modifier.padding(top = 8.dp))

        val scroll = rememberLazyListState()
        LaunchedEffect(state.logLines.size) {
            if (state.logLines.isNotEmpty()) {
                scroll.animateScrollToItem(state.logLines.lastIndex)
            }
        }
        LazyColumn(state = scroll, modifier = Modifier.fillMaxWidth()) {
            itemsIndexed(state.logLines, key = { index, _ -> index }) { _, line ->
                Text(text = line)
            }
        }
    }
}

@Composable
private fun DeployOutcomeContent(state: DeployTriggerUiState.Outcome, onRequestConfirmation: () -> Unit) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Text(text = "Result: ${state.status}")
        state.error?.let { error -> Text(text = error, modifier = Modifier.padding(top = 8.dp)) }
        Button(onClick = onRequestConfirmation, modifier = Modifier.padding(top = 16.dp)) {
            Text(text = "Trigger a new deploy")
        }
    }
}

@Composable
private fun DeployConfirmationDialog(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(text = "Trigger deploy?") },
        text = {
            Text(
                text = "The server will build the current version, apply a deploy with a health " +
                    "check (health-gated) and roll back automatically if the check fails. " +
                    "This can take several minutes, including waiting if another deploy is already in progress.",
            )
        },
        confirmButton = { TextButton(onClick = onConfirm) { Text(text = "Confirm") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(text = "Cancel") } },
    )
}
