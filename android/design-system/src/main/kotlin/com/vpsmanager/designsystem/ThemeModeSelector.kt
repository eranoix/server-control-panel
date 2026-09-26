package com.vpsmanager.designsystem

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/** The section's title. The only literal — the UI and the test read it from here. */
const val THEME_SELECTOR_LABEL = "Appearance"

/** Marca de teste do seletor inteiro. */
const val THEME_SELECTOR_TAG = "seletor-de-aparencia"

/** Test tag for one option of the selector. */
fun themeOptionTag(mode: ThemeMode): String = "aparencia-${mode.id}"

/**
 * The three appearances as a segmented selector: all three visible at once,
 * the current one marked, one tap to switch.
 *
 * ## Why segmented and not a menu, a dialog or a settings screen
 * These are three short, mutually exclusive options — the literal use case for
 * Material 3's `SegmentedButton`. A dropdown menu would hide the options
 * behind one tap and the current state's label behind another; a dialog would
 * cover the screen for a one-click decision; a Settings screen just for this
 * would be a whole screen for one line. Here the owner OPENS THE DRAWER AND
 * ALREADY SEES which mode they are in — the state is the interface itself, not
 * something to go and look up.
 *
 * Nothing is typed and nothing has to be remembered: the three alternatives
 * are listed, you just point.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ThemeModeSelector(
    selected: ThemeMode,
    onSelect: (ThemeMode) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier) {
        Text(
            text = THEME_SELECTOR_LABEL,
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Clip,
            modifier = Modifier.padding(bottom = 8.dp),
        )
        SingleChoiceSegmentedButtonRow(
            modifier = Modifier.fillMaxWidth().testTag(THEME_SELECTOR_TAG),
        ) {
            ThemeMode.entries.forEachIndexed { indice, mode ->
                SegmentedButton(
                    selected = mode == selected,
                    onClick = { onSelect(mode) },
                    shape = SegmentedButtonDefaults.itemShape(
                        index = indice,
                        count = ThemeMode.entries.size,
                    ),
                    // No check icon: with it, the three labels share the
                    // drawer's width with three icons and "Sistema" is the
                    // first to be cut. The current option already stands out
                    // through the colour of its own segment.
                    icon = {},
                    modifier = Modifier.testTag(themeOptionTag(mode)).semantics {
                        contentDescription = "${mode.label} appearance"
                    },
                ) {
                    Text(
                        text = mode.label,
                        maxLines = 1,
                        // Hard clip, not an ellipsis: an ellipsis hides the
                        // problem, a clip shows up in the screenshot and in
                        // the test — the same rule the drawer already follows
                        // for its own labels.
                        overflow = TextOverflow.Clip,
                    )
                }
            }
        }
    }
}
