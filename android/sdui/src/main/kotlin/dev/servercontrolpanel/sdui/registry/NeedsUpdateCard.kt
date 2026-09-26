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

/**
 * Shown in place of a critical [dev.servercontrolpanel.core.sdui.SduiComponent.Unknown]
 * — a component type the server considered essential to this screen, but this
 * build of the app does not recognize. Never rendered for a non-critical
 * unknown, which is skipped entirely instead (see [RenderComponent]).
 *
 * [onUpdateClick] is now WIRED UP: the app shell installs
 * [dev.servercontrolpanel.sdui.registry.LocalUpdateRequest] with the same path the
 * update banner uses, so tapping this card asks for the update that would make
 * it disappear. The default is still a no-op, so that a preview (or a test) can
 * compose the card without needing a coordinator.
 */
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
