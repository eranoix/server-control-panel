package dev.servercontrolpanel.sdui.list

import android.util.Log
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.sdui.data.ComponentDataState
import dev.servercontrolpanel.sdui.data.rememberComponentDataState
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive

private const val TAG = "SduiListComponent"

/** The closed set of card layouts a [SduiComponent.ListComponent] can request. */
private val KNOWN_ITEM_TEMPLATES = setOf("notification_card", "default")

/**
 * Renders a [SduiComponent.ListComponent] as a column of cards using its
 * [SduiComponent.ListComponent.itemTemplate]. An unknown template falls back to
 * `default` and is logged, so the fallback is never silent.
 */
@Composable
fun ListComponent(component: SduiComponent.ListComponent) {
    when (val state = rememberComponentDataState(component.rowsSource).value) {
        is ComponentDataState.Loading -> LoadingBlock()
        is ComponentDataState.Error -> ErrorBlock(state.reason)
        is ComponentDataState.Empty -> EmptyBlock()
        is ComponentDataState.Data -> {
            val template = resolveTemplate(component.itemTemplate)
            // Never LazyColumn: this sits inside SduiScreen's LazyColumn, and a
            // nested vertical scroll crashes with an infinite-height IllegalStateException.
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                state.rows.forEach { row ->
                    when (template) {
                        "notification_card" -> NotificationCard(row)
                        else -> DefaultCard(row)
                    }
                }
            }
        }
    }
}

private fun resolveTemplate(itemTemplate: String): String {
    if (itemTemplate in KNOWN_ITEM_TEMPLATES) return itemTemplate
    Log.w(TAG, "Unrecognized item_template \"$itemTemplate\", falling back to \"default\"")
    return "default"
}

@Composable
private fun NotificationCard(row: JsonObject) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(12.dp)) {
            Text(text = row.stringValue("title") ?: row.stringValue("name").orEmpty(), style = MaterialTheme.typography.bodyLarge)
            row.stringValue("body")?.let { body ->
                Text(text = body, style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

@Composable
private fun DefaultCard(row: JsonObject) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(12.dp)) {
            row.entries.forEach { (key, value) ->
                Text(text = "$key: ${value.jsonPrimitive.contentOrNull.orEmpty()}", style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

private fun JsonObject.stringValue(key: String): String? = this[key]?.jsonPrimitive?.contentOrNull

@Composable
private fun LoadingBlock() {
    Box(modifier = Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ErrorBlock(reason: String) {
    Text(text = reason, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
}

@Composable
private fun EmptyBlock() {
    Text(text = "Nothing to show.", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(16.dp))
}
