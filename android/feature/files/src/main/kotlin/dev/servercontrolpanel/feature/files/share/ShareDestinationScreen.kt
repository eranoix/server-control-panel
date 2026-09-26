package dev.servercontrolpanel.feature.files.share

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.work.WorkInfo
import dev.servercontrolpanel.feature.files.browse.FileBrowserScreen
import dev.servercontrolpanel.feature.files.transfer.KEY_ERROR_REASON
import dev.servercontrolpanel.feature.files.transfer.KEY_PERCENT
import dev.servercontrolpanel.feature.files.transfer.KEY_RESULT_PATH
import dev.servercontrolpanel.feature.files.transfer.PERCENT_UNKNOWN
import dev.servercontrolpanel.feature.files.transfer.TransferViewModel

/**
 * Hosted by `ShareTargetActivity`: shows the shared items, lets the user upload to the inbox
 * or pick a folder, then shows per-item progress and the exact server path each item landed at.
 *
 * Uploads go only through [TransferViewModel.startUpload]; this screen just chooses `destDir`.
 */
@Composable
fun ShareDestinationScreen(
    sharedItems: List<SharedItem>,
    onDone: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: ShareDestinationViewModel = viewModel(),
    transferViewModel: TransferViewModel = viewModel(),
) {
    val step by viewModel.step.collectAsStateWithLifecycle()
    // Disambiguated once so the list key, upload filename and WorkManager query share one unique name.
    val items = remember(sharedItems) { disambiguateSharedItems(sharedItems) }

    when (val currentStep = step) {
        is DestinationStep.ChooseDestination -> {
            val inboxError by viewModel.inboxError.collectAsStateWithLifecycle()
            ChooseDestinationContent(
                modifier = modifier,
                items = items,
                errorMessage = inboxError,
                onSendToInbox = viewModel::sendToInbox,
                onChooseFolder = viewModel::choosePickFolder,
                onCancel = onDone,
            )
        }
        is DestinationStep.PickFolder -> {
            Column(modifier = modifier.fillMaxSize()) {
                TextButton(onClick = viewModel::cancelPickFolder) { Text(text = "Cancel") }
                FileBrowserScreen(
                    modifier = Modifier.fillMaxSize(),
                    pickMode = true,
                    onFolderPicked = viewModel::folderPicked,
                )
            }
        }
        is DestinationStep.Uploading -> UploadingContent(
            modifier = modifier,
            destDir = currentStep.destDir,
            items = items,
            transferViewModel = transferViewModel,
            viewModel = viewModel,
            onDone = onDone,
        )
    }
}

@Composable
private fun ChooseDestinationContent(
    items: List<SharedItem>,
    errorMessage: String?,
    onSendToInbox: () -> Unit,
    onChooseFolder: () -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(text = "Share with Server Control Panel", style = MaterialTheme.typography.titleLarge)
        LazyColumn(modifier = Modifier.weight(1f)) {
            items(items = items, key = { it.displayName }) { item ->
                ListItem(
                    headlineContent = { Text(text = item.displayName) },
                    supportingContent = { Text(text = formatShareSize(item.sizeBytes)) },
                )
            }
        }
        if (errorMessage != null) {
            Text(text = errorMessage, color = MaterialTheme.colorScheme.error)
        }
        Button(onClick = onSendToInbox, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Upload to the default folder")
        }
        OutlinedButton(onClick = onChooseFolder, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Choose folder…")
        }
        TextButton(onClick = onCancel, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Cancel")
        }
    }
}

@Composable
private fun UploadingContent(
    destDir: String,
    items: List<SharedItem>,
    transferViewModel: TransferViewModel,
    viewModel: ShareDestinationViewModel,
    onDone: () -> Unit,
    modifier: Modifier = Modifier,
) {
    // Keyed on destDir so recomposition does not re-issue the uploads (KEEP policy also dedupes).
    LaunchedEffect(destDir) {
        items.forEach { item ->
            transferViewModel.startUpload(
                sourceUri = item.uri,
                destDir = destDir,
                filename = item.displayName,
                totalSize = item.sizeBytes,
            )
        }
    }

    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(text = "Uploading to $destDir", style = MaterialTheme.typography.titleLarge)
        LazyColumn(modifier = Modifier.weight(1f)) {
            items(items = items, key = { it.displayName }) { item ->
                val workInfo by viewModel.uploadWorkInfo(destDir, item.displayName)
                    .collectAsStateWithLifecycle(initialValue = null)
                ShareItemRow(item = item, workInfo = workInfo)
            }
        }
        Button(onClick = onDone, modifier = Modifier.fillMaxWidth()) {
            Text(text = "Done")
        }
    }
}

@Composable
private fun ShareItemRow(item: SharedItem, workInfo: WorkInfo?) {
    val statusText = when {
        workInfo == null -> "Waiting…"
        workInfo.state == WorkInfo.State.SUCCEEDED -> {
            val landedPath = workInfo.outputData.getString(KEY_RESULT_PATH)
            if (landedPath != null) "Uploaded to: $landedPath" else "Uploaded."
        }
        workInfo.state == WorkInfo.State.FAILED ->
            workInfo.outputData.getString(KEY_ERROR_REASON) ?: "Could not finish the upload."
        workInfo.state == WorkInfo.State.CANCELLED -> "Upload canceled."
        else -> {
            val percent = workInfo.progress.getInt(KEY_PERCENT, PERCENT_UNKNOWN)
            if (percent == PERCENT_UNKNOWN) "Uploading…" else "Uploading… $percent%"
        }
    }
    Card(modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
        Column(modifier = Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(text = item.displayName, style = MaterialTheme.typography.titleMedium)
            Text(text = statusText, style = MaterialTheme.typography.bodyMedium)
        }
    }
}

private fun formatShareSize(bytes: Long): String = when {
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> "${bytes / 1024} KB"
    else -> "${bytes / (1024 * 1024)} MB"
}
