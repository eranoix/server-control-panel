package com.vpsmanager.feature.admin

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
import com.vpsmanager.data.sdui.SduiSection

/** Label of the palette's field. Both the UI and the test read it from here. */
internal const val PALETTE_PLACEHOLDER = "Go to…"

/** Description of the button that opens the palette, for screen readers. */
internal const val PALETTE_OPEN_DESCRIPTION = "Open the command palette"

/**
 * The command palette: type and go.
 *
 * ## Why it exists alongside the launcher, and not instead of it
 *
 * The launcher and its search solve "I am in Administration and I want another
 * section". The palette solves something else: **getting there without being
 * there**. It opens on top of whatever is on screen, takes the name, and takes
 * you — without going through the launcher, and without losing your place if
 * you change your mind.
 *
 * It is the pattern that gains the most from 25 sections and the one fewest
 * people discover on their own, and that asymmetry dictates two decisions:
 *
 * 1. There is a **visible button** in the bar, not just the keyboard shortcut.
 *    A palette that only opens with Ctrl+K is a feature only those who already
 *    know about it use — and those who already know were never the problem.
 * 2. With the field **empty**, it lists the recents and then the whole
 *    catalogue. A palette that starts blank waiting for typing does not teach
 *    what it accepts, and the first impression is that it did nothing.
 *
 * ## Why a bottom sheet, and not a dialog
 *
 * The keyboard comes up as soon as it opens, and a bottom sheet is the only
 * Material container that is born anchored at the bottom — close to the thumb
 * and to the keyboard. A centred dialog would be pushed upwards by the IME and
 * would fight the list for space.
 *
 * It **knows no section name at all**: it receives the catalogue ready-made and
 * uses the launcher's own [filterSections], so that the two never disagree about
 * what "docker" finds.
 */
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

    // The keyboard opens along with the sheet. That is the whole gesture:
    // whoever taps the palette is about to type — asking for a second tap on
    // the field would charge twice for the same intent.
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

        // Height ceiling: without it the sheet grows until 25 rows cover the
        // whole screen, and the sense of where you were disappears — which is
        // half of what a palette is worth.
        LazyColumn(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(max = 380.dp)
                .padding(top = 8.dp, bottom = 16.dp),
        ) {
            if (empty && recents.isNotEmpty()) {
                item { Header(ADMIN_RECENTS_LABEL) }
                items(recents, key = { "p-recente-${it.id}" }) { section ->
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

/**
 * One row of the palette: label on the left, group on the right.
 *
 * The group is shown because two sections can have similar labels in different
 * families ("Images" under Docker and under Files), and picking the wrong one
 * costs a whole navigation back.
 */
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

/** `clickable` over the whole row as the target, and not just the text. */
private fun Modifier.clickableRow(onClick: () -> Unit): Modifier = this.clickable(onClick = onClick)
