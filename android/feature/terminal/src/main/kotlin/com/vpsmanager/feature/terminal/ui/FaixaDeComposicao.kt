package com.vpsmanager.feature.terminal.ui

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

/** The node tag, for the tests that prove this strip's episodic height. */
const val FAIXA_COMPOSICAO_TAG = "faixa-composicao"

/**
 * The strip's fixed height.
 *
 * Fixed, and not measured from the text, because a variable height is
 * precisely what was corrupting the screen — see the KDoc on
 * [FaixaDeComposicao]. It fits one monospaced `bodyMedium` line plus the 4 dp
 * of slack the strip always had.
 */
private val ALTURA_DA_FAIXA = 28.dp

/**
 * Shows the word the keyboard is composing and has not yet handed to the
 * terminal.
 *
 * ## Why it exists
 *
 * In [com.vpsmanager.feature.terminal.prefs.ModoDeDigitacao.TEXTO] mode the
 * device's autocorrect only settles on a substitution once the word ends.
 * Until then the text is HELD in the keyboard: the terminal has received no
 * bytes, so the grid has nothing to echo and the screen sits still while the
 * fingers move. It is typing blind.
 *
 * That, incidentally, is why Android terminals tend to simply switch
 * composition off (Termux declares `TYPE_NULL`, ConnectBot likewise): with no
 * place to show the text in flight, autocorrect and a terminal are
 * incompatible in practice. Giving it that place is what makes text mode
 * usable — it is the same feature Blink Shell and Termius expose as a
 * "composition line".
 *
 * ## Why it RESERVES the height instead of appearing and vanishing
 *
 * This is the fix for a measured defect, and the previous version of this
 * comment argued exactly the opposite ("0 dp of cost… a permanent strip would
 * be one more line stolen from the grid in exchange for nothing"). The
 * argument was right about the cost and wrong about the price.
 *
 * By appearing and vanishing, the strip changed the height of the grid's node
 * at every word autocorrect held and released. Measured in the server log with
 * the owner using the app: **77 resizes in 45 minutes**, oscillating between
 * 48 and 50 rows, back and forth every second.
 *
 * Every resize is a SIGWINCH. A differential renderer repaints the whole frame
 * on every SIGWINCH, and a frame taller than the screen **cannot erase
 * itself** — `ESC[nA` saturates at the first row of the SCREEN. Every repaint
 * leaves the previous copy behind, and the result on the owner's screen was
 * overlapping rows with interleaved characters, including chunks of the footer
 * inside the text.
 *
 * It is the SAME class of defect that `1880a859` fixed for the keyboard (the
 * grid shrinking with `imePadding`), arriving from a different source. The
 * lesson it leaves is as big as the class: **the grid's height cannot depend
 * on typing state**. Font, line spacing and screen size change by an explicit
 * action; a word being composed changes ten times a sentence.
 *
 * [reservarEspaco] limits the cost to those who pay for it: in TERMINAL mode
 * there is no composition, the strip never appears, and nothing is reserved.
 * Switching mode with a live session produces ONE resize, not a burst.
 *
 * The text scrolls horizontally instead of wrapping onto two lines — for the
 * same reason, now made explicit: wrapping would change the height mid-typing.
 */
@Composable
fun FaixaDeComposicao(
    texto: String,
    modifier: Modifier = Modifier,
    reservarEspaco: Boolean = true,
) {
    // In TERMINAL mode composition does not exist: nothing appears and
    // nothing is reserved. It is the only case where vanishing outright is
    // safe, because the strip will not come back until the mode changes.
    if (texto.isEmpty() && !reservarEspaco) return

    Row(
        modifier = modifier
            .fillMaxWidth()
            .height(ALTURA_DA_FAIXA)
            .testTag(FAIXA_COMPOSICAO_TAG)
            .background(
                if (texto.isEmpty()) {
                    // Reserved and invisible: it takes the height, it draws no strip.
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
            text = texto,
            style = MaterialTheme.typography.bodyMedium,
            // Monospaced and underlined for the same reason an IME underlines
            // the composition: it is the universal sign for "this has not been
            // confirmed yet". Monospaced to match the grid just above, which is
            // where the text will come out once it is confirmed.
            fontFamily = FontFamily.Monospace,
            textDecoration = TextDecoration.Underline,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
        )
    }
}
