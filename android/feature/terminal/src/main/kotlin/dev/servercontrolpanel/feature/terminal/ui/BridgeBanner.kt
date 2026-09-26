package dev.servercontrolpanel.feature.terminal.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

/**
 * The line that appears when the bridge inserts a command coming from another
 * screen.
 *
 * ## Why it needs to exist
 *
 * The command arrives PASTED onto the line, without running, and the session
 * may have ten lines of something else above it. Without this line, someone
 * who was looking at their finger the instant the terminal opened finds a
 * `docker logs -f` on the line and cannot tell whether they typed it, pasted
 * it, or the app decided on its own. The line answers the only question that
 * matters in that second: **where did this come from**.
 *
 * ## Why it goes away
 *
 * It is a message, not a state. A state deserves permanent space; a message
 * that stays becomes one more strip competing for the few grid rows a phone
 * has — and this app has already learned, the hard way, what it costs to eat
 * grid height without noticing.
 *
 * [LiveRegionMode.Polite] because the information is timely but not urgent:
 * TalkBack announces it once it finishes what it is reading, without cutting
 * the person off mid-sentence.
 */
@Composable
internal fun BridgeBanner(origin: String?, modifier: Modifier = Modifier) {
    AnimatedVisibility(visible = origin != null, modifier = modifier) {
        // `?: return@AnimatedVisibility` will not do here: during the exit
        // animation the value is already null and the text has to go on
        // existing until the animation ends. Keeping the last one seen is what
        // stops the strip from "blinking empty" as it closes.
        val text = origin ?: lastOrigin
        if (origin != null) lastOrigin = origin
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.secondaryContainer)
                .padding(horizontal = 12.dp, vertical = 6.dp)
                .semantics { liveRegion = LiveRegionMode.Polite },
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "Command inserted",
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
            )
            Text(
                // "not executed" is the more important half of the line: it
                // is what tells the person the decision is still theirs.
                text = "· $text · not run, press Enter",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
            )
        }
    }
}

/**
 * The last value seen, so the exit animation has something to draw.
 *
 * File level rather than `remember`: the strip is destroyed and recreated on
 * every change of visibility, so a `remember` inside it would be born empty at
 * exactly the frame where it is needed.
 */
private var lastOrigin: String = ""
