package com.vpsmanager.feature.terminal.selection

import android.content.Context
import android.content.Intent

/** Title of the share chooser. A constant so the test can assert on it. */
const val SHARE_SELECTION_TITLE = "Share selection"

/**
 * Builds the `Intent` that hands [text] to another app through the system
 * chooser.
 *
 * It is `ACTION_SEND` with `text/plain` — the same contract the "Share"
 * action of any Android text field uses, and that is why the list of
 * destinations that appears is the one the device's owner already knows
 * (messengers, e-mail, notes), without the app having to know any of them.
 *
 * Kept apart from [shareText] because the `Intent` is verifiable on
 * the JVM and `startActivity` is not.
 */
fun shareTextIntent(text: String): Intent = Intent.createChooser(
    Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, text)
    },
    SHARE_SELECTION_TITLE,
)

/**
 * Opens the system's share chooser with the selected text.
 *
 * Why this lives in the overflow menu and not on the bar: sharing a snippet
 * of log is useful and rare — copying is what one does all the time. The
 * floating bar has room for two or three buttons before it turns into a
 * ruler, and the hierarchy exists precisely so that the rare does not steal
 * the frequent one's place (see [Placement]).
 */
fun shareText(context: Context, text: String) {
    context.startActivity(shareTextIntent(text))
}
