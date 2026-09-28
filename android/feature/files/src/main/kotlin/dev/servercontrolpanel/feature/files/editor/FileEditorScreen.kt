package dev.servercontrolpanel.feature.files.editor

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.lifecycle.viewmodel.initializer
import androidx.lifecycle.viewmodel.viewModelFactory
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileEditorScreen(
    path: String,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: FileEditorViewModel = viewModel(
        factory = viewModelFactory { initializer { FileEditorViewModel(path = path) } },
    ),
) {
    val state by viewModel.uiState.collectAsStateWithLifecycle()
    val snackbarHostState = remember { SnackbarHostState() }
    val coroutineScope = rememberCoroutineScope()

    var wasSaving by remember { mutableStateOf(false) }
    LaunchedEffect(state) {
        val current = state
        if (wasSaving && current is FileEditorUiState.Editing && current.saveError == null) {
            coroutineScope.launch { snackbarHostState.showSnackbar("Saved") }
        }
        wasSaving = current is FileEditorUiState.Saving
    }

    Scaffold(
        modifier = modifier,
        topBar = {
            TopAppBar(
                title = { Text(text = fileNameOf(path)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = "Back",
                        )
                    }
                },
                actions = {
                    val (isDirty, isSaving) = when (state) {
                        is FileEditorUiState.Editing -> (state as FileEditorUiState.Editing).isDirty to false
                        is FileEditorUiState.Saving -> true to true
                        else -> false to false
                    }
                    TextButton(onClick = viewModel::save, enabled = isDirty && !isSaving) {
                        Text(text = if (isSaving) "Saving…" else "Save")
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(hostState = snackbarHostState) },
    ) { innerPadding ->
        Box(modifier = Modifier.fillMaxSize().padding(innerPadding)) {
            when (val current = state) {
                is FileEditorUiState.Loading -> LoadingContent()
                is FileEditorUiState.Error -> ErrorContent(message = current.message, onRetry = viewModel::retry)
                is FileEditorUiState.Editing -> EditingContent(state = current, onContentChanged = viewModel::onContentChanged)
                is FileEditorUiState.Saving -> Column(modifier = Modifier.fillMaxSize()) {
                    LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                    SoraEditorView(
                        content = current.content,
                        language = current.language,
                        onContentChanged = {},
                        modifier = Modifier.fillMaxSize(),
                    )
                }
                is FileEditorUiState.Conflict -> {
                    SoraEditorView(
                        content = current.localContent,
                        language = current.language,
                        onContentChanged = {},
                        modifier = Modifier.fillMaxSize(),
                    )
                    ConflictDialog(
                        state = current,
                        onReload = viewModel::resolveReload,
                        onOverwrite = viewModel::resolveOverwrite,
                        onCancel = viewModel::resolveCancel,
                    )
                }
            }
        }
    }
}

@Composable
private fun EditingContent(state: FileEditorUiState.Editing, onContentChanged: (String) -> Unit) {
    Column(modifier = Modifier.fillMaxSize()) {
        if (state.saveError != null) {
            Text(
                text = "Could not save: ${state.saveError}",
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            )
        }
        SoraEditorView(
            content = state.content,
            language = state.language,
            onContentChanged = onContentChanged,
            modifier = Modifier.fillMaxSize(),
        )
    }
}

@Composable
private fun ConflictDialog(
    state: FileEditorUiState.Conflict,
    onReload: () -> Unit,
    onOverwrite: () -> Unit,
    onCancel: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text(text = "The file changed on the server") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(text = "Someone (or another session) saved a different version of this file after you opened it.")
                TextButton(onClick = onReload) { Text(text = "Reload the server version") }
            }
        },
        confirmButton = {
            Button(
                onClick = onOverwrite,
                colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error),
            ) { Text(text = "Overwrite anyway") }
        },
        dismissButton = { TextButton(onClick = onCancel) { Text(text = "Cancel") } },
    )
}

@Composable
private fun LoadingContent() {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            CircularProgressIndicator()
            Text(text = "Loading file…")
        }
    }
}

@Composable
private fun ErrorContent(message: String, onRetry: () -> Unit) {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(
            modifier = Modifier.padding(24.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(text = "Could not open the file", style = MaterialTheme.typography.titleLarge)
            Text(text = message)
            Button(onClick = onRetry) { Text(text = "Try again") }
        }
    }
}

private fun fileNameOf(path: String): String = path.trimEnd('/').substringAfterLast('/')
