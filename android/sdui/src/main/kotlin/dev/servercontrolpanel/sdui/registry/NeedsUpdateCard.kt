package dev.servercontrolpanel.sdui.registry

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable
fun NeedsUpdateCard(
    unknownType: String,
    modifier: Modifier = Modifier,
    onUpdateClick: () -> Unit = {},
) {
    Card(
        modifier = modifier.fillMaxWidth().clickable(onClick = onUpdateClick),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
    ) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(
                text = "This section needs a newer version of the app",
                style = MaterialTheme.typography.bodyLarge,
            )
            Text(
                text = "Unrecognized type: $unknownType",
                style = MaterialTheme.typography.bodySmall,
                modifier = Modifier.padding(top = 4.dp),
            )
        }
    }
}
