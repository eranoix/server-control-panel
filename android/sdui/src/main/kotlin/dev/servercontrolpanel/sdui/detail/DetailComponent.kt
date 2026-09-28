package dev.servercontrolpanel.sdui.detail

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.core.sdui.SduiDetailField
import dev.servercontrolpanel.sdui.data.ComponentDataState
import dev.servercontrolpanel.sdui.data.rememberComponentDataState
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive

@Composable
fun DetailComponent(component: SduiComponent.Detail) {
    when (val state = rememberComponentDataState(component.dataSource).value) {
        is ComponentDataState.Loading -> LoadingBlock()
        is ComponentDataState.Error -> ErrorBlock(state.reason)
        is ComponentDataState.Empty -> EmptyBlock()
        is ComponentDataState.Data -> {
            val resource = state.rows.firstOrNull()
            if (resource == null) {
                EmptyBlock()
            } else {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    component.fields.forEach { field ->
                        DetailRow(field = field, resource = resource)
                    }
                }
            }
        }
    }
}

@Composable
private fun DetailRow(field: SduiDetailField, resource: JsonObject) {
    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(text = field.label, style = MaterialTheme.typography.labelMedium)
        Text(
            text = resource[field.key]?.jsonPrimitive?.contentOrNull.orEmpty(),
            style = MaterialTheme.typography.bodyMedium,
        )
    }
}

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
