package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp

/** Node tag, for the tests that check the strip's height behaviour. */
const val COMPOSITION_STRIP_TAG = "composition-strip"

/**
 * The strip's fixed height (one monospaced `bodyMedium` line plus 4 dp). Fixed
 * rather than measured, since a variable height corrupts the screen; see
 * [CompositionStrip].
 */
private val BAND_HEIGHT = 28.dp

/**
 * Shows the word the keyboard is composing and has not yet handed to the terminal.
 *
 * In [dev.servercontrolpanel.feature.terminal.prefs.TypingMode.TEXT] mode autocorrect
 * holds the text until the word ends, so the terminal has nothing to echo and the
 * user would type blind. This is why many Android terminals disable composition
 * (Termux uses `TYPE_NULL`); a composition line makes text mode usable.
 *
 * The strip RESERVES its height instead of appearing and vanishing: changing the
 * grid height on every held word produced dozens of resizes (measured: 77 in 45
 * minutes), and each SIGWINCH makes differential renderers repaint frames that
 * leave overlapping copies. The grid height must not depend on typing state.
 * [reserveSpace] is false in TERMINAL mode, where there is no composition, so
 * switching mode causes a single resize. The text scrolls horizontally rather
 * than wrapping, for the same reason.
 */
@Composable
fun CompositionStrip(
    text: String,
    modifier: Modifier = Modifier,
    reserveSpace: Boolean = true,
) {
    // In TERMINAL mode there is no composition, so nothing is shown or reserved.
    // Safe because the strip cannot return until the mode changes.
    if (text.isEmpty() && !reserveSpace) return

    Row(
        modifier = modifier
            .fillMaxWidth()
            .height(BAND_HEIGHT)
            .testTag(COMPOSITION_STRIP_TAG)
            .background(
                if (text.isEmpty()) {
                    // Reserved but invisible: takes the height, draws nothing.
                    Color.Transparent
                } else {
                    MaterialTheme.colorScheme.surfaceVariant
                },
            )
            .padding(horizontal = 12.dp, vertical = 4.dp)
            .horizontalScroll(rememberScrollState()),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodyMedium,
            // Underlined like an IME composition ("not confirmed yet") and
            // monospaced to match the grid where the text will appear.
            fontFamily = FontFamily.Monospace,
            textDecoration = TextDecoration.Underline,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
        )
    }
}
