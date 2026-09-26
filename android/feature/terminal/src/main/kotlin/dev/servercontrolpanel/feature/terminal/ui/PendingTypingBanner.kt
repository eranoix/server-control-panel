package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

/**
 * The strip that shows what was typed while offline.
 *
 * ## The defect it fixes
 *
 * *"I press the keys and what I typed does not show up"*. In a terminal, what
 * you type only appears because the **server** sends the echo back — there is
 * no local echo. With the connection down, the keys queue up on the socket and
 * the screen stays mute, and a mute screen is indistinguishable from a frozen
 * app. The person then types it again, and on reconnecting the command comes
 * out twice.
 *
 * ## Why it is a STRIP and not text in the grid
 *
 * Writing into the grid would look better and would be worse: the real echo
 * would arrive afterwards and the text would appear twice, and the emulator —
 * which is the exact replica of what the remote program drew — would fall out
 * of sync with the server. See the KDoc of `typingSummary`.
 *
 * The strip is honest about what it is: it does not say the command is in the
 * shell, it says it is **stored, ready to go**.
 *
 * ## Monospace, and scrolling to the end
 *
 * Monospace because it is terminal content and lining up with the grid helps
 * you recognise what you wrote. Scrolling to the end because what matters is
 * what has just been typed — the start of a long command has already been read.
 */
@Composable
internal fun PendingTypingBanner(text: String, modifier: Modifier = Modifier) {
    AnimatedVisibility(visible = text.isNotEmpty(), modifier = modifier) {
        val scroll = rememberScrollState()
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.tertiaryContainer)
                .padding(horizontal = 12.dp, vertical = 6.dp)
                // Polite rather than assertive: the information is timely, but
                // interrupting somebody who is listening to something else over
                // a keystroke would be worse than the silence it came to solve.
                .semantics { liveRegion = LiveRegionMode.Polite },
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "Held",
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onTertiaryContainer,
            )
            Text(
                text = text,
                style = MaterialTheme.typography.labelMedium,
                fontFamily = FontFamily.Monospace,
                maxLines = 1,
                color = MaterialTheme.colorScheme.onTertiaryContainer,
                modifier = Modifier
                    .weight(1f, fill = false)
                    .horizontalScroll(scroll, reverseScrolling = true),
            )
            Text(
                // The sentence says what IS GOING TO happen, not what is wrong.
                // The connection strip just above already says the network is
                // down; repeating that here would spend the line adding nothing.
                text = "sent on reconnect",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onTertiaryContainer,
            )
        }
    }
}
