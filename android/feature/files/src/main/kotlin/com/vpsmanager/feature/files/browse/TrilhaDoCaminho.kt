package com.vpsmanager.feature.files.browse

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

/**
 * The path trail: every folder of the current path, tappable.
 *
 * ## Why the trail is this screen's main piece of information
 *
 * A remote file browser has a problem a local one does not: there is nothing
 * around it to say where you are. On your own phone, a folder called `data` is
 * recognisable. In `/opt/panel/data` versus
 * `/var/lib/docker/volumes/x/_data`, the name `data` distinguishes nothing —
 * and the next action is almost always destructive enough that confusing the
 * two costs dearly.
 *
 * That is why the trail remains the content even when the list is **empty** (a
 * folder with nothing in it still answers "where am I") and even when the list
 * has **failed** (there it is what lets you go up a level instead of only
 * retrying what has just failed). It is the only component on this screen with
 * no empty state.
 *
 * ## Two behavioural decisions
 *
 * **It scrolls itself to the end.** A deep path does not fit the width of a
 * phone, and the half that matters is the tail — the folder you are in, not the
 * root. Anchoring at the start would always show `/ opt / vps-…` and hide
 * precisely the answer.
 *
 * **The last segment is not tappable.** It is where you already are; a tap that
 * reloads the same folder feels like a broken button. It is also the only one
 * in bold — the current position has to be distinguishable from the trail
 * behind it.
 */
@Composable
internal fun TrilhaDoCaminho(
    caminho: String,
    aoTocarSegmento: (String) -> Unit,
    modifier: Modifier = Modifier,
    estadoDaRolagem: LazyListState = rememberLazyListState(),
) {
    val degraus = degrausDoCaminho(caminho)

    LaunchedEffect(caminho, degraus.size) {
        if (degraus.isNotEmpty()) estadoDaRolagem.scrollToItem(degraus.lastIndex)
    }

    LazyRow(
        state = estadoDaRolagem,
        modifier = modifier
            .fillMaxWidth()
            // The whole trail is ONE target for the screen reader: announcing
            // seven buttons called "opt", "vps-manager", "data" one by one
            // would force you through all seven to learn where you are. The
            // description says the path in one go; the taps stay individual for
            // those who can see.
            .clearAndSetSemantics { contentDescription = "Current path: $caminho" },
        horizontalArrangement = Arrangement.spacedBy(2.dp),
        verticalAlignment = Alignment.CenterVertically,
        contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 4.dp),
    ) {
        items(items = degraus, key = { it.caminho }) { degrau ->
            val atual = degrau.caminho == caminho.trimEnd('/').ifEmpty { "/" }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = degrau.rotulo,
                    style = MaterialTheme.typography.bodyMedium,
                    fontWeight = if (atual) FontWeight.Bold else FontWeight.Normal,
                    color = if (atual) {
                        MaterialTheme.colorScheme.onSurface
                    } else {
                        MaterialTheme.colorScheme.primary
                    },
                    modifier = Modifier
                        .clip(RoundedCornerShape(6.dp))
                        .then(
                            if (atual) {
                                Modifier.background(MaterialTheme.colorScheme.surfaceVariant)
                            } else {
                                Modifier.clickable { aoTocarSegmento(degrau.caminho) }
                            },
                        )
                        // A 48dp TOUCH TARGET — not an aesthetic detail.
                        //
                        // The text on its own with the original padding came to
                        // ~32 dp of height. In a navigation control, missing the
                        // target is not an inconvenience: it takes you to the
                        // wrong folder, and the next action in a remote file
                        // browser tends to be destructive. 48 dp is the minimum
                        // from Material and from Android's own accessibility
                        // guidance, measured in fingers and not in pixels.
                        //
                        // The height goes on the TARGET, not on the text: the
                        // horizontal padding stays tight so a deep path still
                        // fits, and only the tappable area grows.
                        .heightIn(min = 48.dp)
                        .wrapContentHeight(Alignment.CenterVertically)
                        .padding(horizontal = 8.dp),
                )
                if (!atual) {
                    Text(
                        text = "/",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.outline,
                    )
                }
            }
        }
    }
}

/** One step of the trail: what you read and where it leads. */
internal data class DegrauDoCaminho(val rotulo: String, val caminho: String)

/**
 * Breaks an absolute path into the steps of the trail, from the root down to
 * the path itself.
 *
 * The root appears as `/` and not as an empty label: a path starts with a
 * slash, and an invisible step would be a touch target nobody can see.
 *
 * Empty segments are discarded — `//opt///data` is a valid path as far as the
 * kernel is concerned and would produce phantom steps here.
 */
internal fun degrausDoCaminho(caminho: String): List<DegrauDoCaminho> {
    val limpo = caminho.trimEnd('/')
    val degraus = mutableListOf(DegrauDoCaminho(rotulo = "/", caminho = "/"))
    if (limpo.isEmpty() || limpo == "/") return degraus
    var acumulado = ""
    limpo.split('/').filter { it.isNotEmpty() }.forEach { parte ->
        acumulado = "$acumulado/$parte"
        degraus += DegrauDoCaminho(rotulo = parte, caminho = acumulado)
    }
    return degraus
}
