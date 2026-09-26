package com.vpsmanager.feature.terminal.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.vpsmanager.data.terminal.SessionBackup
import com.vpsmanager.data.terminal.agruparPorSessao
import com.vpsmanager.data.terminal.VersaoDeBackup
import com.vpsmanager.data.terminal.GrupoDeBackup
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * The session backups sheet.
 *
 * ## Why a sheet, and not a screen
 * A backup is an operation ON the session list, not a place you go to. The
 * sheet keeps the list visible behind it, which is the context for "restore
 * what, and where to" — and leaving it is a gesture, not a navigation with a
 * back stack.
 *
 * ## What each card shows
 * ```
 * ┌───────────────────────────────────────────────┐
 * │ 07/09 20:14 · automatic      1.2 MB   ↺   🗑  │
 * │ [Servidor] [Aplicativo] [tt]                   │
 * │ Servidor — build passed, pushing the deploy    │
 * └───────────────────────────────────────────────┘
 * ```
 * Date, origin, size, which sessions are inside, and one line saying what the
 * first of them was about. The summary is what tells two backups from the same
 * afternoon apart — without it, choosing which one to restore means choosing
 * by timestamp.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BackupsSheet(viewModel: SessionListViewModel, aoFechar: () -> Unit) {
    val estado by viewModel.backups.collectAsStateWithLifecycle()
    val ocupada by viewModel.ocupada.collectAsStateWithLifecycle()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var confirmandoExclusao by remember { mutableStateOf<VersaoParaExcluir?>(null) }
    // Which groups are open. All closed by default: the sheet opens showing
    // the SESSION LIST, which is the question ("which session?"), not a wall
    // of dates. Opening everything up front would hand the problem back.
    val abertos = remember { mutableStateMapOf<String, Boolean>() }

    // Load on opening, not in the ViewModel's `init`: listing backups reads
    // every one of the user's files, and that read must not happen every time
    // the sessions screen appears.
    LaunchedEffect(Unit) { viewModel.carregarBackups() }

    ModalBottomSheet(onDismissRequest = aoFechar, sheetState = sheetState) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = 16.dp, end = 16.dp, bottom = 24.dp)
                .heightIn(max = 560.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(text = "Session backups", style = MaterialTheme.typography.titleMedium)
                Spacer(modifier = Modifier.weight(1f))
                TextButton(onClick = { viewModel.salvarBackup(null) }) { Text(text = "Save now") }
            }
            Text(
                text = "The server also saves on its own every 6 h and keeps the 10 most recent.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            when (val atual = estado) {
                is BackupsUiState.Ocioso, is BackupsUiState.Carregando -> CaixaCentral {
                    CircularProgressIndicator(modifier = Modifier.size(28.dp))
                }
                is BackupsUiState.Vazio -> CaixaCentral {
                    Text(
                        text = "No backups yet.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                is BackupsUiState.Erro -> CaixaCentral {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(text = atual.mensagem, style = MaterialTheme.typography.bodyMedium)
                        TextButton(onClick = viewModel::carregarBackups) { Text(text = "Try again") }
                    }
                }
                is BackupsUiState.Pronto -> {
                    val grupos = remember(atual.backups) { agruparPorSessao(atual.backups) }
                    LazyColumn(
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                        modifier = Modifier.testTag(LISTA_BACKUPS_TAG),
                    ) {
                        items(items = grupos, key = { it.sessao }) { grupo ->
                            GrupoDeSessao(
                                grupo = grupo,
                                aberto = abertos[grupo.sessao] == true,
                                ocupado = ocupada != null,
                                aoAlternar = {
                                    abertos[grupo.sessao] = abertos[grupo.sessao] != true
                                },
                                aoRestaurarVersao = { versao ->
                                    viewModel.restaurar(versao.id, grupo.sessao)
                                },
                                aoRestaurarTudoDoSnapshot = { versao ->
                                    viewModel.restaurar(versao.id)
                                },
                                aoExcluirVersao = { versao ->
                                    confirmandoExclusao = VersaoParaExcluir(grupo.sessao, versao)
                                },
                            )
                        }
                    }
                }
            }
        }
    }

    confirmandoExclusao?.let { alvo ->
        AlertDialog(
            onDismissRequest = { confirmandoExclusao = null },
            title = { Text(text = "Delete this version?") },
            text = {
                // The wording names the SESSION and the DATE, not "this
                // backup": what is deleted here is one version of one
                // session. When the snapshot holds other sessions, they stay
                // — and not saying so would make a person think they are
                // deleting the whole backup.
                Text(
                    text = buildString {
                        append("\"${alvo.sessao}\" from ${dataLegivel(alvo.versao.criadoEm)}.")
                        if (alvo.versao.sessoesNoBackup > 1) {
                            append(
                                " The other ${alvo.versao.sessoesNoBackup - 1} session(s) " +
                                    "in this same backup are kept.",
                            )
                        }
                        append(" This cannot be undone.")
                    },
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    val id = alvo.versao.id
                    val sessao = alvo.sessao
                    confirmandoExclusao = null
                    viewModel.excluirBackup(id, sessao)
                }) { Text(text = "Delete") }
            },
            dismissButton = {
                TextButton(onClick = { confirmandoExclusao = null }) { Text(text = "Cancel") }
            },
        )
    }
}

/** What the confirmation needs to know: which session, from which version. */
private data class VersaoParaExcluir(val sessao: String, val versao: VersaoDeBackup)

/**
 * One group: the session, and its versions once open.
 *
 * ## Why closed by default
 *
 * Once open, this sheet answers "which session?"; the date only matters after
 * that question has been answered. Eight expanded groups would hand back the
 * wall of repeated dates that motivated the grouping in the first place.
 *
 * ## Why the count sits in the header
 *
 * "3 versions · most recent 09/09 08:40" answers, without expanding, the two
 * things that decide whether expanding is worth it: whether there is anything
 * to choose from, and whether the latest one is recent enough.
 */
@Composable
private fun GrupoDeSessao(
    grupo: GrupoDeBackup,
    aberto: Boolean,
    ocupado: Boolean,
    aoAlternar: () -> Unit,
    aoRestaurarVersao: (VersaoDeBackup) -> Unit,
    aoRestaurarTudoDoSnapshot: (VersaoDeBackup) -> Unit,
    aoExcluirVersao: (VersaoDeBackup) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.fillMaxWidth()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable(onClick = aoAlternar)
                    .padding(horizontal = 12.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = grupo.sessao,
                        style = MaterialTheme.typography.titleSmall,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        text = "${grupo.versoes.size} version(s) · latest " +
                            dataLegivel(grupo.versoes.first().criadoEm),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = if (aberto) "▲" else "▼",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            if (!aberto && grupo.resumo.isNotBlank()) {
                // The collapsed summary is the clue to WHICH session this is
                // when the name does not say (`tt`, `proxy`). It goes away on
                // expanding: there the versions already have the attention.
                Text(
                    text = grupo.resumo,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(start = 12.dp, end = 12.dp, bottom = 10.dp),
                )
            }

            if (aberto) {
                grupo.versoes.forEach { versao ->
                    HorizontalDivider()
                    LinhaDeVersao(
                        versao = versao,
                        ocupado = ocupado,
                        aoRestaurar = { aoRestaurarVersao(versao) },
                        aoRestaurarTudo = { aoRestaurarTudoDoSnapshot(versao) },
                        aoExcluir = { aoExcluirVersao(versao) },
                    )
                }
            }
        }
    }
}

/** One version: date, origin, and what can be done with it. */
@Composable
private fun LinhaDeVersao(
    versao: VersaoDeBackup,
    ocupado: Boolean,
    aoRestaurar: () -> Unit,
    aoRestaurarTudo: () -> Unit,
    aoExcluir: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp)) {
        Text(text = dataLegivel(versao.criadoEm), style = MaterialTheme.typography.bodyMedium)
        Text(
            // THE SIZE IS THAT OF THE WHOLE FILE, and the wording says so
            // when it holds more than one session. "89 kB" beside the name of
            // ONE session implies that this session takes 89 kB, which is
            // false in a backup of eight.
            text = buildString {
                append(origemLegivel(versao.origem))
                append(" · ")
                if (versao.sessoesNoBackup > 1) {
                    append("${tamanhoLegivel(versao.bytes)} in a backup of ${versao.sessoesNoBackup} sessions")
                } else {
                    append(tamanhoLegivel(versao.bytes))
                }
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (versao.resumo.isNotBlank()) {
            Text(
                text = versao.resumo,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 2.dp),
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            TextButton(onClick = aoRestaurar, enabled = !ocupado) { Text(text = "Restore") }
            // "Restore the snapshot" only appears when there is a snapshot
            // to restore: in a backup of a single session it would do exactly
            // the same as the button next to it, under another name.
            if (versao.sessoesNoBackup > 1) {
                TextButton(onClick = aoRestaurarTudo, enabled = !ocupado) {
                    Text(text = "All ${versao.sessoesNoBackup}")
                }
            }
            Spacer(modifier = Modifier.weight(1f))
            TextButton(onClick = aoExcluir, enabled = !ocupado) { Text(text = "Delete") }
        }
    }
}

@Composable
private fun CaixaCentral(conteudo: @Composable () -> Unit) {
    Box(
        modifier = Modifier.fillMaxWidth().height(160.dp),
        contentAlignment = Alignment.Center,
    ) { conteudo() }
}

@Composable
private fun CartaoDeBackup(
    backup: SessionBackup,
    ocupado: Boolean,
    aoRestaurarTudo: () -> Unit,
    aoRestaurarSessao: (String) -> Unit,
    aoExcluir: () -> Unit,
) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
    ) {
        Column(
            modifier = Modifier.padding(start = 12.dp, end = 4.dp, top = 10.dp, bottom = 10.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(
                        text = dataLegivel(backup.criadoEm),
                        style = MaterialTheme.typography.titleSmall,
                    )
                    Text(
                        text = listOfNotNull(
                            origemLegivel(backup.origem),
                            tamanhoLegivel(backup.bytes),
                            "${backup.sessoes.size} session(s)",
                        ).joinToString(" · "),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                IconButton(onClick = aoRestaurarTudo, enabled = !ocupado) {
                    Icon(
                        imageVector = Icons.Filled.Refresh,
                        contentDescription = "Restore all sessions in this backup",
                    )
                }
                IconButton(onClick = aoExcluir, enabled = !ocupado) {
                    Icon(
                        imageVector = Icons.Filled.Delete,
                        contentDescription = "Delete this backup",
                        tint = MaterialTheme.colorScheme.error,
                    )
                }
            }

            // Tapping a chip restores ONLY that session. That is the truly
            // common case: one almost never wants the whole bundle back, one
            // wants the session that was lost.
            ChipsDeSessoes(
                nomes = backup.sessoes.map { it.nome },
                aoTocar = { if (!ocupado) aoRestaurarSessao(it) },
            )

            backup.sessoes.firstOrNull { it.resumo.isNotBlank() }?.let { s ->
                Surface(
                    color = MaterialTheme.colorScheme.surface,
                    shape = RoundedCornerShape(8.dp),
                    modifier = Modifier.fillMaxWidth().padding(end = 8.dp),
                ) {
                    Text(
                        text = s.resumo,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(horizontal = 8.dp, vertical = 6.dp),
                    )
                }
            }
        }
    }
}

/**
 * Short date and time. No year: a terminal session backup lives for days, not
 * years — pruning keeps ten — and the year would spend the width that the
 * session names need.
 */
private fun dataLegivel(segundos: Long): String {
    if (segundos <= 0L) return "—"
    return SimpleDateFormat("dd/MM HH:mm", Locale.getDefault()).format(Date(segundos * 1000))
}

/**
 * The origin, spelled out in Portuguese. It matters because it changes the
 * expectation: an automatic one disappears by itself on pruning, a manual one
 * belongs to the user, a scheduled one has a retention of its own.
 */
private fun origemLegivel(origem: String?): String = when (origem) {
    "manual" -> "manual"
    "auto" -> "automatic"
    "scheduled" -> "scheduled"
    else -> "legacy"
}

private fun tamanhoLegivel(bytes: Long): String = when {
    bytes <= 0 -> "—"
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> String.format(Locale.getDefault(), "%.0f kB", bytes / 1024.0)
    else -> String.format(Locale.getDefault(), "%.1f MB", bytes / (1024.0 * 1024.0))
}

internal const val LISTA_BACKUPS_TAG = "backups-lista"
