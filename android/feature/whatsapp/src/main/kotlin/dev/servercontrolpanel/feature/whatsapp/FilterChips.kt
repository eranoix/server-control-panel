package dev.servercontrolpanel.feature.whatsapp

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

@Composable
internal fun FilterChips(
    selected: ChatFilter,
    counts: Map<ChatFilter, Int>?,
    onSelect: (ChatFilter) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 12.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        ChatFilter.entries.forEach { filter ->
            val n = counts?.get(filter)
            val number = n?.toString() ?: "—"
            FilterChip(
                selected = filter == selected,
                onClick = { onSelect(filter) },
                enabled = counts != null,
                label = { Text(text = "${filter.label} $number") },
                colors = FilterChipDefaults.filterChipColors(),
                modifier = Modifier.semantics {
                    contentDescription = when (n) {
                        null -> "${filter.label}, count unavailable"
                        1 -> "${filter.label}, 1 chat"
                        else -> "${filter.label}, $n chats"
                    }
                },
            )
        }
    }
}
