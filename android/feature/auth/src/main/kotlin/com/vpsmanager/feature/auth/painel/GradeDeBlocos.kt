package com.vpsmanager.feature.auth.painel

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.vpsmanager.data.dashboard.Severity
import com.vpsmanager.designsystem.vpsmStatusColors

/**
 * The dashboard grid: the blocks the person has assembled.
 *
 * ## Two columns, and not an adaptive grid
 *
 * The Administração screen uses `GridCells.Adaptive` because there the cards
 * are labels and more of them fit on a wide screen. Here every block is a
 * NUMBER that has to be read at a glance, at arm's length — three columns on
 * a phone would shrink the number to a size that forces you to bring the
 * device closer, which is exactly the gesture a dashboard exists to avoid.
 *
 * ## The wide block takes the whole row
 *
 * A block turns wide when its phrase does not fit in half a screen: a
 * reverted deploy says `hello: rolled_back`, and cut down to `hello: rolle…`
 * it tells you less than not existing at all.
 *
 * ## Why it is NOT a lazy grid
 *
 * This grid lives inside Início's `LazyColumn`. A `LazyVerticalGrid` nested
 * in a `LazyColumn` is handed an infinite maximum height and **breaks at
 * run time** — a classic Compose mistake, not a matter of taste. And there
 * would be no gain here at all: the dashboard is capped at twelve blocks
 * ([BlocosEscolhidos.MAXIMO]), so composing them all at once is cheap and
 * laziness would bring nothing but the defect.
 */
@Composable
internal fun GradeDeBlocos(
    blocos: List<BlocoDoPainel>,
    emEdicao: Boolean,
    aoTocar: (BlocoDoPainel) -> Unit,
    aoRemover: (BlocoDoPainel) -> Unit,
    aoPedirCatalogo: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        linhasDaGrade(blocos).forEach { linha ->
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                linha.forEach { bloco ->
                    Box(modifier = Modifier.weight(if (bloco.largo) 2f else 1f)) {
                        Bloco(
                            bloco = bloco,
                            emEdicao = emEdicao,
                            aoTocar = { aoTocar(bloco) },
                            aoRemover = { aoRemover(bloco) },
                        )
                    }
                }
                // A row with a single narrow block on it (an odd number of
                // blocks) needs a gap of the same weight, or that block
                // stretches to the full width and pretends to be a wide one.
                if (linha.size == 1 && !linha.first().largo) {
                    Box(modifier = Modifier.weight(1f))
                }
            }
        }
        if (emEdicao) AcrescentarBloco(aoPedirCatalogo)
    }
}

/**
 * Lays the blocks out in rows of two columns, honouring the wide ones.
 *
 * A wide block takes the whole row, so it closes the row in progress before
 * joining. Without that, a wide block after a narrow one would produce a row
 * three columns' worth of weight and the grid would lose its vertical
 * alignment — and the alignment is precisely what lets two numbers be
 * compared at a glance.
 */
internal fun linhasDaGrade(blocos: List<BlocoDoPainel>): List<List<BlocoDoPainel>> {
    val linhas = mutableListOf<List<BlocoDoPainel>>()
    var atual = mutableListOf<BlocoDoPainel>()
    fun fechar() {
        if (atual.isNotEmpty()) {
            linhas += atual.toList()
            atual = mutableListOf()
        }
    }
    blocos.forEach { bloco ->
        if (bloco.largo) {
            fechar()
            linhas += listOf(bloco)
        } else {
            atual += bloco
            if (atual.size == 2) fechar()
        }
    }
    fechar()
    return linhas
}

/**
 * One block.
 *
 * The colour comes from the same judgement the rest of the dashboard uses
 * (`vpsmStatusColors`), never from a palette of its own here — two different
 * reds on the same screen teach the eye that red means nothing.
 */
@Composable
private fun Bloco(
    bloco: BlocoDoPainel,
    emEdicao: Boolean,
    aoTocar: () -> Unit,
    aoRemover: () -> Unit,
) {
    val cores = vpsmStatusColors
    // The pair comes whole from the design system: background and ink are
    // chosen together over there so that they have contrast in both themes.
    // Mixing one pair's container with the theme's `onSurfaceVariant` — which
    // was the first reflex here — produces exactly the illegible text that
    // file exists to prevent.
    val par = when (bloco.severidade) {
        Severity.OK -> cores.ok
        Severity.ATENCAO -> cores.warning
        Severity.CRITICO -> cores.critical
    }
    val fundo = par.container
    val tinta = par.content
    Card(
        colors = CardDefaults.cardColors(containerColor = fundo, contentColor = tinta),
        modifier = Modifier
            .fillMaxWidth()
            // A minimum height and not a fixed one: the wide block with a
            // two-line phrase needs room to grow, and a number block needs
            // not to shrink below what is read at a glance.
            .heightIn(min = 92.dp)
            .clickable(enabled = !emEdicao, onClick = aoTocar),
    ) {
        Column(modifier = Modifier.padding(10.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                // The icon comes BEFORE the label and is recognised before
                // it is read — it is what turns the grid into a dashboard
                // rather than a list with big numbers. No description: the
                // label is right beside it and that is what the screen reader
                // should announce.
                Icon(
                    imageVector = iconeDoBloco(bloco.id),
                    contentDescription = null,
                    tint = tinta,
                    modifier = Modifier.size(14.dp).padding(end = 0.dp),
                )
                Spacer(modifier = Modifier.width(5.dp))
                Text(
                    text = bloco.rotulo.uppercase(),
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (emEdicao) {
                    // "Tirar" and not an X: an X beside a number on a server
                    // dashboard is ambiguous enough to be frightening — the
                    // word says it removes the BLOCK, not what it measures.
                    TextButton(onClick = aoRemover, contentPadding = androidx.compose.foundation.layout.PaddingValues(4.dp)) {
                        Text(text = "Remove", style = MaterialTheme.typography.labelSmall)
                    }
                }
            }
            Text(
                text = bloco.valor,
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (bloco.sub.isNotBlank()) {
                Text(
                    text = bloco.sub,
                    style = MaterialTheme.typography.bodySmall,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun AcrescentarBloco(aoPedirCatalogo: () -> Unit) {
    OutlinedCard(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = 56.dp)
            .clickable(onClick = aoPedirCatalogo),
    ) {
        Box(modifier = Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
            Text(text = "+  Add tile", style = MaterialTheme.typography.labelLarge)
        }
    }
}

/**
 * The catalogue: everything the server offers today, with whatever is already
 * on the dashboard marked.
 *
 * It shows the ALREADY CHOSEN blocks alongside the rest, disabled, instead of
 * hiding them. A list that vanishes as you pick from it forces you to
 * remember what you have already picked in order to understand why an item
 * went missing — and the answer "because it is already there" is only obvious
 * to whoever wrote the screen.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun CatalogoDeBlocos(
    catalogo: List<BlocoDoPainel>,
    escolhidos: List<String>,
    noTeto: Boolean,
    aoEscolher: (BlocoDoPainel) -> Unit,
    aoFechar: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = aoFechar) {
        Column(modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp)) {
            Text(text = "Available tiles", style = MaterialTheme.typography.titleMedium)
            Text(
                text = if (noTeto) {
                    "The dashboard is at its limit of ${BlocosEscolhidos.MAXIMO} tiles. " +
                        "Remove one to add another."
                } else {
                    "The list comes from the server — a new disk shows up here on its own."
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(bottom = 8.dp),
            )
        }
        LazyVerticalGrid(
            columns = GridCells.Fixed(2),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.heightIn(max = 380.dp),
        ) {
            items(items = catalogo, key = { it.id }) { bloco ->
                val jaEsta = bloco.id in escolhidos
                OutlinedCard(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable(enabled = !jaEsta && !noTeto) { aoEscolher(bloco) },
                ) {
                    Column(modifier = Modifier.padding(10.dp)) {
                        Text(
                            text = bloco.rotulo,
                            style = MaterialTheme.typography.labelLarge,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                            color = if (jaEsta) {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            } else {
                                MaterialTheme.colorScheme.onSurface
                            },
                        )
                        Text(
                            text = if (jaEsta) "already on the dashboard" else bloco.valor,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
    }
}
