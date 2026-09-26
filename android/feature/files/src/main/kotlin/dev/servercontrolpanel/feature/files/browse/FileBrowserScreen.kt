package dev.servercontrolpanel.feature.files.browse

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import dev.servercontrolpanel.core.model.FileEntry
import dev.servercontrolpanel.core.shell.BridgeCommands
import dev.servercontrolpanel.core.shell.TerminalBridge
import dev.servercontrolpanel.feature.files.transfer.TransferScreen
import dev.servercontrolpanel.feature.files.transfer.TransferViewModel

/**
 * Remote directory browser for [FileBrowserUiState]. Loading, error and empty states each
 * render distinctly, never as a blank screen.
 *
 * [onOpenFile] receives the tapped file's full server path. Upload and download actions go
 * through [TransferViewModel], with progress shown inline by [TransferScreen].
 *
 * With [pickMode] the screen becomes a folder picker: folders and the current directory get a
 * Select action that reports the path via [onFolderPicked] instead of opening or transferring.
 */
@Composable
fun FileBrowserScreen(
    modifier: Modifier = Modifier,
    onOpenFile: (String) -> Unit = {},
    pickMode: Boolean = false,
    onFolderPicked: (String) -> Unit = {},
    viewModel: FileBrowserViewModel = viewModel(),
    transferViewModel: TransferViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val context = LocalContext.current

    var notificationsAsked by remember { mutableStateOf(false) }
    val notificationPermissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { /* Denied notifications still let the transfer run -- progress stays visible via TransferScreen. */ }

    fun ensureNotificationPermission() {
        if (!notificationsAsked) {
            notificationsAsked = true
            val granted = ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) ==
                PackageManager.PERMISSION_GRANTED
            if (!granted) notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    val uploadPickerLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenDocument(),
    ) { pickedUri: Uri? ->
        if (pickedUri != null) {
            ensureNotificationPermission()
            // UploadWorker may run after a retry or process death, so the one-shot read grant must be persisted.
            context.contentResolver.takePersistableUriPermission(
                pickedUri,
                android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION,
            )
            val (pickedName, pickedSize) = queryDisplayNameAndSize(context, pickedUri)
            transferViewModel.startUpload(
                sourceUri = pickedUri.toString(),
                destDir = viewModel.currentPath,
                filename = pickedName ?: pickedUri.lastPathSegment ?: "file",
                totalSize = pickedSize,
            )
        }
    }

    Column(modifier = modifier.fillMaxSize()) {
        FileBrowserHeader(
            currentPath = viewModel.currentPath,
            canNavigateUp = viewModel.currentPath != ROOT_PATH,
            onNavigateUp = viewModel::navigateUp,
            onGoToPath = viewModel::goTo,
            pickMode = pickMode,
            onUploadClick = { uploadPickerLauncher.launch(arrayOf("*/*")) },
            onSelectCurrentClick = { onFolderPicked(viewModel.currentPath) },
        )
        if (!pickMode) {
            TransferScreen(modifier = Modifier.fillMaxWidth(), viewModel = transferViewModel)
        }
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(16.dp),
            contentAlignment = Alignment.Center,
        ) {
            when (val current = state) {
                is FileBrowserUiState.Loading -> LoadingContent()
                is FileBrowserUiState.Error -> ErrorContent(
                    message = current.message,
                    onRetry = viewModel::retry,
                    // Going up usually works when a retry would not (the parent is readable).
                    onUpOneLevel = viewModel::navigateUp.takeIf { viewModel.currentPath != ROOT_PATH },
                )
                is FileBrowserUiState.Empty -> EmptyContent()
                is FileBrowserUiState.Success -> DirectoryContent(
                    entries = current.entries,
                    pathOf = viewModel::pathFor,
                    pickMode = pickMode,
                    onEntryClick = { entry ->
                        when {
                            entry.isDir -> viewModel.navigateInto(entry)
                            !pickMode -> onOpenFile(viewModel.pathFor(entry))
                            // In pickMode tapping a file does nothing.
                        }
                    },
                    onDownloadClick = { entry ->
                        ensureNotificationPermission()
                        transferViewModel.startDownload(
                            path = viewModel.pathFor(entry),
                            filename = entry.name,
                            mimeType = mimeTypeFor(entry.name),
                            totalSize = entry.size,
                        )
                    },
                    onSelectFolderClick = { entry -> onFolderPicked(viewModel.pathFor(entry)) },
                )
            }
        }
    }
}

@Composable
private fun FileBrowserHeader(
    currentPath: String,
    canNavigateUp: Boolean,
    onNavigateUp: () -> Unit,
    onGoToPath: (String) -> Unit,
    pickMode: Boolean,
    onUploadClick: () -> Unit,
    onSelectCurrentClick: () -> Unit,
) {
    Surface(tonalElevation = 2.dp) {
        Column(modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
            // The breadcrumb gets its own line so buttons do not squeeze it on deep paths.
            PathBreadcrumb(
                path = currentPath,
                onTapSegment = onGoToPath,
                modifier = Modifier.padding(horizontal = 12.dp),
            )
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                if (canNavigateUp) {
                    // Not labelled "Back": it moves up the file tree, not the navigation stack.
                    TextButton(onClick = onNavigateUp) { Text(text = "Up one folder") }
                }
                Spacer(modifier = Modifier.weight(1f))
                if (!pickMode) {
                    // Runs `ls -la` on the current folder for details this browser does not show.
                    TextButton(
                        onClick = {
                            val cmd = BridgeCommands.listDir(currentPath)
                            TerminalBridge.send(cmd.command, cmd.origin)
                        },
                    ) { Text(text = "In terminal") }
                }
                if (pickMode) {
                    TextButton(onClick = onSelectCurrentClick) { Text(text = "Select this folder") }
                } else {
                    TextButton(onClick = onUploadClick) { Text(text = "Upload") }
                }
            }
        }
    }
}

@Composable
private fun LoadingContent() {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        CircularProgressIndicator()
        Text(text = "Loading folder…")
    }
}

@Composable
private fun EmptyContent() {
    Card {
        Column(
            modifier = Modifier.padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(text = "Empty folder", style = MaterialTheme.typography.titleLarge)
            Text(text = "This folder has no files or subfolders.")
        }
    }
}

@Composable
private fun ErrorContent(message: String, onRetry: () -> Unit, onUpOneLevel: (() -> Unit)?) {
    Card {
        Column(
            modifier = Modifier.padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text(text = "Could not load", style = MaterialTheme.typography.titleLarge)
            Text(text = message)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = onRetry) {
                    Text(text = "Try again")
                }
                // Failures are usually a permission on the current folder, so going up avoids a dead end.
                onUpOneLevel?.let {
                    TextButton(onClick = it) { Text(text = "Up one folder") }
                }
            }
        }
    }
}

@Composable
private fun DirectoryContent(
    entries: List<FileEntry>,
    pathOf: (FileEntry) -> String,
    pickMode: Boolean,
    onEntryClick: (FileEntry) -> Unit,
    onDownloadClick: (FileEntry) -> Unit,
    onSelectFolderClick: (FileEntry) -> Unit,
) {
    LazyColumn(modifier = Modifier.fillMaxSize()) {
        items(items = entries, key = { it.name }) { entry ->
            FileRow(
                entry = entry,
                fullPath = pathOf(entry),
                pickMode = pickMode,
                onClick = { onEntryClick(entry) },
                onDownloadClick = { onDownloadClick(entry) },
                onSelectFolderClick = { onSelectFolderClick(entry) },
            )
        }
    }
}

@Composable
private fun FileRow(
    entry: FileEntry,
    fullPath: String,
    pickMode: Boolean,
    onClick: () -> Unit,
    onDownloadClick: () -> Unit,
    onSelectFolderClick: () -> Unit,
) {
    ListItem(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick),
        headlineContent = {
            Text(
                text = entry.name,
                fontWeight = if (entry.isDir) FontWeight.Bold else FontWeight.Normal,
            )
        },
        supportingContent = {
            Text(text = if (entry.isDir) "Folder" else formatSize(entry.size))
        },
        leadingContent = {
            Surface(
                modifier = Modifier.size(32.dp),
                shape = CircleShape,
                color = if (entry.isDir) {
                    MaterialTheme.colorScheme.primaryContainer
                } else {
                    MaterialTheme.colorScheme.secondaryContainer
                },
            ) {
                Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    Text(
                        text = if (entry.isDir) "D" else "A",
                        style = MaterialTheme.typography.labelLarge,
                    )
                }
            }
        },
        trailingContent = {
            when {
                pickMode && entry.isDir -> TextButton(onClick = onSelectFolderClick) { Text(text = "Select") }
                !pickMode && !entry.isDir -> Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    // Sends an editable `cat <file>` to the terminal, for files the editor handles badly.
                    TextButton(
                        onClick = {
                            val cmd = BridgeCommands.viewFile(fullPath)
                            TerminalBridge.send(cmd.command, cmd.origin)
                        },
                    ) { Text(text = "View") }
                    TextButton(onClick = onDownloadClick) { Text(text = "Download") }
                }
            }
        },
    )
}

private fun formatSize(bytes: Long): String = when {
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> "${bytes / 1024} KB"
    else -> "${bytes / (1024 * 1024)} MB"
}

private fun mimeTypeFor(filename: String): String {
    val extension = filename.substringAfterLast('.', missingDelimiterValue = "")
    if (extension.isEmpty()) return "application/octet-stream"
    return MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension.lowercase())
        ?: "application/octet-stream"
}

/** Returns a null name or zero size when the provider does not report those columns. */
private fun queryDisplayNameAndSize(context: android.content.Context, uri: Uri): Pair<String?, Long> {
    var name: String? = null
    var size = 0L
    context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
        val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
        val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
        if (cursor.moveToFirst()) {
            if (nameIndex >= 0) name = cursor.getString(nameIndex)
            if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) size = cursor.getLong(sizeIndex)
        }
    }
    return name to size
}
