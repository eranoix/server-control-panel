package com.vpsmanager.feature.admin

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.vpsmanager.data.sdui.SduiSection

/** Label of the search field. Both the UI and the test read it from here. */
internal const val ADMIN_BUSCA_LABEL = "Search sections"

/** Header of the recents strip. */
internal const val ADMIN_RECENTES_LABEL = "Recent"

/**
 * The Administration launcher: **search on top, a grid of sections below**.
 *
 * ## Why Administration now opens here, and not on a section
 *
 * Before, opening "Admin" landed straight on the first section in the
 * catalogue. That treats the 25 sections as if one of them were the right
 * answer — and none is: which one matters depends on what is going on. Opening
 * the launcher swaps a choice made by the app for a choice made by the person
 * who knows.
 *
 * The direct route still exists and does not come through here: a notification
 * deep link carries the concrete section id and opens on it, because there the
 * question has already been answered by whoever sent the notification.
 *
 * ## Why search AND grid, rather than one of the two
 *
 * They serve opposite situations, which is why both ways into the catalogue
 * fit together:
 *
 * - The **grid** serves recognition: a glance finds it by shape and position,
 *   without reading. It works up to about 12 targets.
 * - **Search** serves recall: you know the name, not where it lives. It is the
 *   only model that IMPROVES as the catalogue grows.
 *
 * With 25 sections the app sits exactly in the band where neither one alone is
 * enough. And they do not fight for space: the field takes one row and filters
 * the grid below it, instead of replacing it with another screen.
 *
 * ## The third state only search has
 *
 * "Searched and found nothing" is neither empty nor an error, and it has to say
 * so with the term on screen — otherwise it looks as though the catalogue has
 * vanished. See [SemResultado].
 *
 * The grid knows NO section name at all: label, group and order come whole from
 * `GET /screens`, already filtered by RBAC on the server.
 */
@Composable
internal fun AdminLauncher(
    sections: List<SduiSection>,
    busca: String,
    recentes: List<SduiSection>,
    onBuscaChange: (String) -> Unit,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val filtradas = filtrarSecoes(sections, busca)
    val buscando = busca.isNotBlank()

    Column(modifier = modifier.fillMaxSize()) {
        OutlinedTextField(
            value = busca,
            onValueChange = onBuscaChange,
            singleLine = true,
            label = { Text(ADMIN_BUSCA_LABEL) },
            leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
            trailingIcon = {
                if (buscando) {
                    IconButton(onClick = { onBuscaChange("") }) {
                        Icon(Icons.Filled.Close, contentDescription = "Clear search")
                    }
                }
            },
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 8.dp),
        )

        if (filtradas.isEmpty()) {
            SemResultado(termo = busca, total = sections.size, onLimpar = { onBuscaChange("") })
            return@Column
        }

        LazyVerticalGrid(
            // Adaptive, rather than a fixed number of columns: the same
            // launcher runs on a narrow phone and on a tablet, and 112dp is the
            // width at which a two-word label still fits on two lines.
            columns = GridCells.Adaptive(minSize = 112.dp),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(
                start = 12.dp, end = 12.dp, bottom = 24.dp,
            ),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.fillMaxSize(),
        ) {
            // Recents only appear when you are NOT searching: someone who has
            // typed has already said what they want, and repeating the recents
            // in the middle of the results would mix two answers to two
            // different questions.
            if (!buscando && recentes.isNotEmpty()) {
                item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                    CabecalhoDeGrupo(ADMIN_RECENTES_LABEL)
                }
                items(recentes, key = { "recente-${it.id}" }) { secao ->
                    CartaoDeSecao(secao = secao, onClick = { onSelect(secao.id) })
                }
                item(span = { androidx.compose.foundation.lazy.grid.GridItemSpan(maxLineSpan) }) {
                    CabecalhoDeGrupo("All sections")
                }
            }

            items(filtradas, key = { it.id }) { secao ->
                CartaoDeSecao(secao = secao, onClick = { onSelect(secao.id) })
            }
        }
    }
}

/**
 * Filters by label, group AND id.
 *
 * All three, because each is how a different person remembers the same screen:
 * by the name shown ("Containers"), by the family it belongs to ("Docker") or
 * by the id they saw in a log (`docker.containers`). Ignoring the id would make
 * search fail precisely for the person who arrived from an error message — the
 * case where it is worth the most.
 */
internal fun filtrarSecoes(sections: List<SduiSection>, busca: String): List<SduiSection> {
    val termo = busca.trim()
    if (termo.isEmpty()) return sections
    return sections.filter { secao ->
        secao.label.contains(termo, ignoreCase = true) ||
            secao.group.contains(termo, ignoreCase = true) ||
            secao.id.contains(termo, ignoreCase = true)
    }
}

@Composable
private fun CabecalhoDeGrupo(texto: String) {
    Text(
        text = texto,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 4.dp, top = 12.dp, bottom = 2.dp),
    )
}

/**
 * A target in the grid.
 *
 * The "icon" is the initial of the GROUP, not a drawing: `material-icons-core`
 * has no symbol for "Docker" or "Scheduler", and mapping section id to icon on
 * the client would reintroduce exactly the coupling that SDUI exists to remove
 * — a new section on the server would once again need a release. The initial
 * gives the same recognition cue (sections of the same group end up with the
 * same mark) without the app knowing a single name.
 */
@Composable
private fun CartaoDeSecao(secao: SduiSection, onClick: () -> Unit) {
    // The colour of the FAMILY. See CorDoGrupo: in a grid of thirty identical
    // blocks the eye cannot jump to "the Docker part" — the group label is
    // there, but reading thirty labels is the work the colour does for free.
    val cor = corDoGrupo(secao.group)
    Card(
        onClick = onClick,
        shape = RoundedCornerShape(16.dp),
        colors = CardDefaults.cardColors(
            // The BACKGROUND stays neutral, on purpose. Thirty fully coloured
            // blocks would fight one another and none would stand out; colour
            // comes in as an ACCENT — on the symbol and on the bar — which is
            // enough to group without turning into a carnival.
            containerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
        ),
        // 128dp and not 108: two double-line labels plus the group did not fit
        // in 108, and the group line was cut in half — visible on "Docker
        // images" and "Services (systemd)". See the header of this file.
        modifier = Modifier.height(128.dp),
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 8.dp, vertical = 10.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Surface(
                shape = CircleShape,
                // 18% of the accent: a chip visible in both themes without
                // competing with the symbol it carries.
                color = cor.copy(alpha = 0.18f),
                modifier = Modifier.size(40.dp),
            ) {
                Box(contentAlignment = Alignment.Center) {
                    // An ICON, rather than the group initial.
                    //
                    // The initial gave visual rhythm and ZERO identity — the
                    // owner sent a photo: five Docker sections became five
                    // "D"s, and `Sistema` and `Segurança`, the two families it
                    // matters most to tell apart on a control panel, were both
                    // "S". A symbol that does not distinguish is worse than no
                    // symbol, because it occupies the place where the eye looks
                    // for the difference.
                    //
                    // The description is deliberately NULL: the section label
                    // sits right below it, on the same card, and the whole card
                    // is a single target. Repeating "Docker" on the icon would
                    // make the screen reader say the same word twice.
                    Icon(
                        imageVector = iconeDaSecao(secao.id),
                        contentDescription = null,
                        tint = cor,
                        modifier = Modifier.size(22.dp),
                    )
                }
            }
            Text(
                // The label arrives SHORTENED. See rotuloCurto: "Metrics (CPU,
                // memory, dis…" took two lines to say nothing — what sat inside
                // the parentheses was detail, and it was the part surviving the
                // cut.
                text = rotuloCurto(secao.label),
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 6.dp),
            )
            // The group name takes the colour of the family: it is the legend
            // for the colour itself, stated once per block. Without it, the
            // colour would be a code nobody was given.
            Text(
                text = secao.group,
                style = MaterialTheme.typography.labelSmall,
                color = cor,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * "Searched and found nothing" — the third state.
 *
 * It states the term searched for and how many sections exist in total. Without
 * both, this screen is indistinguishable from "the catalogue has vanished",
 * which is a completely different problem and frightens people for no reason.
 */
@Composable
private fun SemResultado(termo: String, total: Int, onLimpar: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Icon(
            imageVector = Icons.Filled.Search,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(40.dp),
        )
        Text(
            text = "Nothing matches “$termo”",
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(top = 12.dp),
        )
        Text(
            text = "None of the $total sections match that text.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 4.dp),
        )
        Text(
            text = "Clear search",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier
                .padding(top = 16.dp)
                .clickable(onClick = onLimpar)
                .padding(8.dp),
        )
    }
}

/**
 * What appears when the server offers this user NO section at all.
 *
 * This is neither an error nor a network failure: it is a user without
 * administrative permission, and the text says exactly that instead of leaving
 * a blank page that looks like a defect.
 *
 * It is also the state that must NOT be confused with the launcher emptied by a
 * search: here there is nothing to search, and so there is no field. Collapsing
 * the two would show a filter over a list that will never have items.
 */
@Composable
internal fun AdminCatalogVazio(modifier: Modifier = Modifier) {
    Column(modifier = modifier.fillMaxWidth().padding(24.dp)) {
        Text(
            text = "No sections available",
            style = MaterialTheme.typography.titleMedium,
        )
        Text(
            text = "Your account does not have permission for any of the admin sections " +
                "on this server. Ask an administrator to grant access to what you need.",
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.padding(top = 8.dp),
        )
    }
}
