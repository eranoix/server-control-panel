package dev.servercontrolpanel.feature.files.transfer

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel

/**
 * In-flight transfer list, rendered inline inside
 * [dev.servercontrolpanel.feature.files.browse.FileBrowserScreen] -- one card per
 * active or finished download/upload, each with its own cancel action.
 * Transfers are a secondary affordance of the file browser, not a
 * destination of their own, so nothing here touches `AppNavHost`.
 */
@Composable
fun TransferScreen(modifier: Modifier = Modifier, viewModel: TransferViewModel = viewModel()) {
    val transfers by viewModel.transfers.collectAsStateWithLifecycle()

    if (transfers.isEmpty()) return

    Column(modifier = modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        transfers.forEach { (workId, state) ->
            Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp)) {
                Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    when (state) {
                        is TransferUiState.InProgress -> {
                            Text(text = "Transfer in progress", style = MaterialTheme.typography.labelLarge)
                            if (state.percent in 0..100) {
                                LinearProgressIndicator(
                                    progress = { state.percent / 100f },
                                    modifier = Modifier.fillMaxWidth(),
                                )
                                Text(text = "${state.percent}%")
                            } else {
                                LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                            }
                            Row { TextButton(onClick = { viewModel.cancel(workId) }) { Text(text = "Cancel") } }
                        }
                        is TransferUiState.Completed -> Text(text = "Done: ${state.path}")
                        is TransferUiState.Failed -> Text(text = "Failed: ${state.reason}")
                        TransferUiState.Cancelled -> Text(text = "Canceled")
                    }
                }
            }
        }
    }
}
