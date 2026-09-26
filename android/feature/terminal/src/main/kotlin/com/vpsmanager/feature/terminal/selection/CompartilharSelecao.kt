package com.vpsmanager.feature.terminal.selection

import android.content.Context
import android.content.Intent

/** Title of the share chooser. A constant so the test can assert on it. */
const val TITULO_COMPARTILHAR_SELECAO = "Share selection"

/**
 * Builds the `Intent` that hands [texto] to another app through the system
 * chooser.
 *
 * It is `ACTION_SEND` with `text/plain` — the same contract the "Share"
 * action of any Android text field uses, and that is why the list of
 * destinations that appears is the one the device's owner already knows
 * (messengers, e-mail, notes), without the app having to know any of them.
 *
 * Kept apart from [compartilharTexto] because the `Intent` is verifiable on
 * the JVM and `startActivity` is not.
 */
fun intentDeCompartilharTexto(texto: String): Intent = Intent.createChooser(
    Intent(Intent.ACTION_SEND).apply {
        type = "text/plain"
        putExtra(Intent.EXTRA_TEXT, texto)
    },
    TITULO_COMPARTILHAR_SELECAO,
)

/**
 * Opens the system's share chooser with the selected text.
 *
 * Why this lives in the overflow menu and not on the bar: sharing a snippet
 * of log is useful and rare — copying is what one does all the time. The
 * floating bar has room for two or three buttons before it turns into a
 * ruler, and the hierarchy exists precisely so that the rare does not steal
 * the frequent one's place (see [Destaque]).
 */
fun compartilharTexto(context: Context, texto: String) {
    context.startActivity(intentDeCompartilharTexto(texto))
}
