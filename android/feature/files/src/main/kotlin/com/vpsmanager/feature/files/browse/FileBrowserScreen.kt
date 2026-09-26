package com.vpsmanager.feature.files.browse

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
import com.vpsmanager.core.model.FileEntry
import com.vpsmanager.core.shell.BridgeCommands
import com.vpsmanager.core.shell.TerminalBridge
import com.vpsmanager.feature.files.transfer.TransferScreen
import com.vpsmanager.feature.files.transfer.TransferViewModel

/**
 * Remote directory browser: renders [FileBrowserUiState] as it comes back
 * from [FileBrowserViewModel], which in turn calls the generated mobile BFF
 * client via `FilesRepository.list()`. Every state below is real, distinct
 * UI -- a failed listing, a permission error and an empty directory never
 * look the same as each other or as a blank screen.
 *
 * [onOpenFile] receives the tapped file's full server path (via
 * [FileBrowserViewModel.pathFor], since [FileEntry] itself carries no path)
 * -- the editor screen is the destination `AppNavHost` wires it to.
 *
 * Each file row also carries a "Baixar" action and the header an "Enviar"
 * action that hands off to [TransferViewModel], which enqueues the actual
 * [com.vpsmanager.feature.files.transfer.DownloadWorker]/
 * [com.vpsmanager.feature.files.transfer.UploadWorker] job -- transfers are
 * a secondary affordance of this screen, not a new `AppNavHost` destination,
 * so their progress renders inline via [TransferScreen].
 *
 * [pickMode] turns this same screen into a folder picker: directory rows
 * still navigate deeper on tap, but also gain a "Selecionar" action, and the
 * header gains a "Selecionar esta pasta" action for the directory currently
 * open -- both report the chosen path via [onFolderPicked] instead of
 * opening a file or starting a manual transfer. This is purely additive:
 * with [pickMode] at its default (`false`), every existing affordance
 * (upload, download, open-in-editor, inline transfer progress) behaves
 * exactly as before.
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
            // UploadWorker may resume long after this picker call returns
            // (retry, process death) -- without a persistable grant, the
            // one-shot read permission `OpenDocument` hands back would not
            // survive that, and re-opening the source file would fail.
            context.contentResolver.takePersistableUriPermission(
                pickedUri,
                android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION,
            )
            val (pickedName, pickedSize) = queryDisplayNameAndSize(context, pickedUri)
            transferViewModel.startUpload(
                sourceUri = pickedUri.toString(),
                destDir = viewModel.currentPath,
                filename = pickedName ?: pickedUri.lastPathSegment ?: "arquivo",
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
                    // On an error, reloading retries exactly what has just
                    // failed. Going up one level usually WORKS (the parent
                    // folder exists and is readable), which is why it is
                    // offered alongside — the breadcrumb above stays tappable
                    // for the same reason.
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
                            // A file row is not a valid folder pick -- tapping it in
                            // pickMode is a no-op, it neither navigates nor opens.
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
            // THE BREADCRUMB COMES FIRST, alone on its line. It is this
            // screen's primary information (see PathBreadcrumb), and sharing
            // the width with two action buttons would squeeze it to three
            // characters on a deep path — which is exactly when it is most
            // useful.
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
                    // "Folder up", not "Back": this button goes up one level in
                    // the FILE TREE, not in the navigation stack. Files is a
                    // top-level destination (it carries the shell's hamburger, not
                    // a back arrow), so labelling this "Back" next to the hamburger
                    // promised a navigation it does not perform.
                    TextButton(onClick = onNavigateUp) { Text(text = "Up one folder") }
                }
                Spacer(modifier = Modifier.weight(1f))
                if (!pickMode) {
                    // "In the terminal": opens the CURRENT folder in an
                    // `ls -la`. It is the way out of every question this
                    // browser does not answer — permissions, owner, symlink,
                    // the real size of a directory.
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
                // Going up a level usually works when reloading will not:
                // the commonest cause of failure here is a permission on the
                // CURRENT folder, and the parent is almost always readable.
                // Without this exit, a folder you cannot read becomes a dead
                // end — the only alternative being to reopen the whole screen
                // and navigate again from the root.
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
                    // "View" sends `cat <file>` to the terminal. It is the
                    // answer for a file the editor does not open well:
                    // binary, enormous, or a log you want to follow with
                    // `tail -f` straight afterwards — the command arrives
                    // editable.
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

/** `null` name/zero size are both legitimate misses (a provider that doesn't answer these columns). */
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
