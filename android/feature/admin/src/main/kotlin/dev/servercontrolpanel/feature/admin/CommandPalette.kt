package dev.servercontrolpanel.feature.admin

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.data.sdui.SduiSection

internal const val PALETTE_PLACEHOLDER = "Go to…"

internal const val PALETTE_OPEN_DESCRIPTION = "Open the command palette"

@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun CommandPalette(
    sections: List<SduiSection>,
    recents: List<SduiSection>,
    onSelect: (String) -> Unit,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var term by remember { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    LaunchedEffect(Unit) { focus.requestFocus() }

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = state, modifier = modifier) {
        val filtered = filterSections(sections, term)
        val empty = term.isBlank()

        OutlinedTextField(
            value = term,
            onValueChange = { term = it },
            singleLine = true,
            placeholder = { Text(PALETTE_PLACEHOLDER) },
            leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp)
                .focusRequester(focus),
        )

        LazyColumn(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(max = 380.dp)
                .padding(top = 8.dp, bottom = 16.dp),
        ) {
            if (empty && recents.isNotEmpty()) {
                item { Header(ADMIN_RECENTS_LABEL) }
                items(recents, key = { "p-recent-${it.id}" }) { section ->
                    PaletteRow(section = section, onClick = { onSelect(section.id) })
                }
                item { HorizontalDivider(modifier = Modifier.padding(vertical = 4.dp)) }
            }

            if (filtered.isEmpty()) {
                item {
                    Text(
                        text = "Nothing matches “${term.trim()}” among the ${sections.size} sections.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 20.dp),
                    )
                }
            } else {
                if (empty) item { Header("All sections") }
                items(filtered, key = { "p-${it.id}" }) { section ->
                    PaletteRow(section = section, onClick = { onSelect(section.id) })
                }
            }
        }
    }
}

@Composable
private fun Header(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 16.dp, top = 10.dp, bottom = 2.dp),
    )
}

@Composable
private fun PaletteRow(section: SduiSection, onClick: () -> Unit) {
    ListItem(
        headlineContent = {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Text(
                    text = section.label,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                Text(
                    text = section.group,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(start = 12.dp),
                )
            }
        },
        modifier = Modifier.clickableRow(onClick),
    )
}

private fun Modifier.clickableRow(onClick: () -> Unit): Modifier = this.clickable(onClick = onClick)
