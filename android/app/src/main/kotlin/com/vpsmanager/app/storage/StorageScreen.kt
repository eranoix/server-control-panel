package com.vpsmanager.app.storage

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.vpsmanager.data.storage.AppStorage

const val TAG_STORAGE = "tela-armazenamento"
const val TAG_FREE_SPACE = "botao-liberar-espaco"

/**
 * What the app takes up, item by item, and a button that frees the rebuildable space. It gives
 * a safe alternative to Android's "clear storage", which also wipes preferences, the server
 * and the session. Items the cleanup does not touch are listed too, so the total matches what
 * Android shows.
 */
@Composable
internal fun StorageScreen(
    usages: List<AppStorage.Usage>,
    onFreeSpace: () -> Unit,
    lastResult: AppStorage.CleanupResult?,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(20.dp)
            .testTag(TAG_STORAGE),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        val total = usages.sumOf { it.bytes }
        Text(
            text = AppStorage.formatBytes(total),
            style = MaterialTheme.typography.headlineMedium,
        )
        Text(
            text = "in data the app can download again",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        HorizontalDivider(Modifier.padding(vertical = 16.dp))

        usages.forEach { usage ->
            Row(
                modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp),
                verticalAlignment = Alignment.Top,
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Column(Modifier.weight(1f).padding(end = 16.dp)) {
                    Text(usage.name, style = MaterialTheme.typography.bodyLarge)
                    Text(
                        usage.explanation,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    AppStorage.formatBytes(usage.bytes),
                    style = MaterialTheme.typography.bodyLarge,
                )
            }
        }

        HorizontalDivider(Modifier.padding(vertical = 16.dp))

        Button(
            onClick = onFreeSpace,
            modifier = Modifier.testTag(TAG_FREE_SPACE),
        ) {
            Text("Free up space now")
        }

        // Report bytes, not "done": even "0 B" tells the user the space was not here.
        lastResult?.let {
            Text(
                text = "Freed ${AppStorage.formatBytes(it.freedBytes)} " +
                    "across ${it.filesRemoved} file(s).",
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.padding(top = 12.dp),
            )
        }

        Text(
            text = "Preferences, the configured server, the session and anything queued " +
                "for upload are never deleted here. The app also does this " +
                "cleanup on its own, once a day.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 20.dp),
        )
    }
}
