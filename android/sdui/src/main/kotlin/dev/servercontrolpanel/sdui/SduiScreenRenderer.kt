package dev.servercontrolpanel.sdui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.core.sdui.SduiEnvelope
import dev.servercontrolpanel.sdui.actionrunner.ActionOutcome
import dev.servercontrolpanel.sdui.registry.RenderComponent

@Composable
fun SduiScreen(
    envelope: SduiEnvelope,
    modifier: Modifier = Modifier,
    onOutcome: (ActionOutcome) -> Unit = {},
    refreshKey: Any? = null,
) {
    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        items(
            items = envelope.screen.components,
            key = { it.id },
        ) { component ->
            RenderComponent(component, onOutcome, refreshKey)
        }
    }
}
