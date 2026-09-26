package com.vpsmanager.feature.terminal.attach

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import java.util.UUID

/** Test tag of the attachments bar. */
const val BARRA_ANEXOS_TAG = "barra-anexos-terminal"

/** Label of the button that puts the path of ONE ready attachment on the command line. */
const val INSERIR_LABEL = "Insert"

/** Label of the button that inserts every ready attachment at once. */
const val INSERIR_TODOS_LABEL = "Insert all"

/**
 * The attachments strip, between the grid and the key bar.
 *
 * **Closed, it does not exist.** With no attachment in progress or ready, this
 * function emits no node at all: zero cost in height, exactly the criterion
 * `TerminalRoute` applies to every piece of chrome on this screen (it was by
 * failing to respect it that three occasional controls cost a permanent 160.8 dp
 * before moving into the options sheet). It appears when it has something to
 * say, like the connection banner.
 *
 * **It sits ABOVE the key bar, flush against it**, and not at the top of the
 * screen: that is the corner the thumb reaches, and it is where the eye already
 * is while typing the command the path is going into.
 *
 * Every row states its status in words, not just in colour — "Uploading 40%",
 * "Ready", the reason for an error spelled out — because the information has to
 * survive a screen reader and a screen in sunlight.
 */
@Composable
fun BarraDeAnexos(
    anexos: List<AnexoNaTela>,
    aoInserir: (List<UUID>) -> Unit,
    aoCancelar: (UUID) -> Unit,
    aoDescartar: (UUID) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (anexos.isEmpty()) return

    val prontos = anexos.filter { it.estado is EstadoDoAnexo.Pronto }.map { it.id }

    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(horizontal = 12.dp, vertical = 4.dp)
            .testTag(BARRA_ANEXOS_TAG),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        anexos.forEach { anexo ->
            LinhaDeAnexo(
                anexo = anexo,
                aoInserir = { aoInserir(listOf(anexo.id)) },
                aoCancelar = { aoCancelar(anexo.id) },
                aoDescartar = { aoDescartar(anexo.id) },
            )
        }
        // Offering "all" only makes sense when there is more than one; with a
        // single attachment, the button on its own row is already the shortest
        // route.
        if (prontos.size > 1) {
            TextButton(onClick = { aoInserir(prontos) }) { Text(text = "$INSERIR_TODOS_LABEL (${prontos.size})") }
        }
    }
}

@Composable
private fun LinhaDeAnexo(
    anexo: AnexoNaTela,
    aoInserir: () -> Unit,
    aoCancelar: () -> Unit,
    aoDescartar: () -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = anexo.nome,
                style = MaterialTheme.typography.labelLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = descricaoDoEstado(anexo.estado),
                style = MaterialTheme.typography.bodySmall,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            val estado = anexo.estado
            if (estado is EstadoDoAnexo.Enviando && estado.percentual != PERCENTUAL_DESCONHECIDO) {
                LinearProgressIndicator(
                    progress = { estado.percentual / 100f },
                    modifier = Modifier.fillMaxWidth().widthIn(min = 0.dp),
                )
            }
        }
        when (anexo.estado) {
            is EstadoDoAnexo.Enviando -> TextButton(onClick = aoCancelar) { Text(text = "Cancel") }
            is EstadoDoAnexo.Pronto -> {
                TextButton(onClick = aoInserir) { Text(text = INSERIR_LABEL) }
                TextButton(onClick = aoDescartar) { Text(text = "Dismiss") }
            }
            is EstadoDoAnexo.Falhou, is EstadoDoAnexo.Cancelado ->
                TextButton(onClick = aoDescartar) { Text(text = "Dismiss") }
        }
    }
}

/**
 * The status in words. An error shows the reason COMING FROM THE SERVER (already
 * turned into an actionable sentence by `transferErrorFor`, in `:data`), never a
 * generic "failed": "no disk space left" and "file too large" call for opposite
 * actions from whoever is reading.
 */
internal fun descricaoDoEstado(estado: EstadoDoAnexo): String = when (estado) {
    is EstadoDoAnexo.Enviando ->
        if (estado.percentual == PERCENTUAL_DESCONHECIDO) "Uploading…" else "Uploading ${estado.percentual}%"
    is EstadoDoAnexo.Pronto -> estado.caminho
    is EstadoDoAnexo.Falhou -> estado.motivo
    is EstadoDoAnexo.Cancelado -> "Upload canceled."
}
