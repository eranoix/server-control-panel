package com.vpsmanager.feature.jira

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.vpsmanager.data.jira.NovaIssue

/** Test tag for the creation sheet. */
internal const val TAG_FOLHA_CRIACAO = "jira-folha-criacao"

/**
 * Creating an issue.
 *
 * ## Everything that can be listed is listed
 *
 * Type, priority and assignee come from the server as a LIST — none of them is
 * a text field. A typed type gets the accent wrong, the capital wrong, and the
 * name of something that project does not have, and the mistake only shows on
 * submit, after filling in the rest. It is the same rule the rest of the
 * application follows: typing is the last resort.
 *
 * ## What is left out, and why
 *
 * Parent epic, components, versions and story points exist on the web panel
 * and are not here. It is not an oversight: they are refinement fields, done
 * in a planning session with a whole keyboard at hand. Creating from the phone
 * is recording what has just come up before it is forgotten — summary, type
 * and who for. Stuffing eight optional fields into this sheet would make quick
 * capture cost as much as refinement.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun FolhaDeCriacao(
    estado: EstadoDaCriacao,
    projeto: String,
    aoFechar: () -> Unit,
    aoCriar: (NovaIssue) -> Unit,
) {
    val folha = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var resumo by remember { mutableStateOf("") }
    var descricao by remember { mutableStateOf("") }
    var tipo by remember { mutableStateOf("") }
    var prioridade by remember { mutableStateOf("") }
    var responsavel by remember { mutableStateOf<com.vpsmanager.data.jira.PessoaDoJira?>(null) }
    var menuDePessoas by remember { mutableStateOf(false) }

    // The first type the project offers comes pre-selected: a form that
    // opens with the mandatory field empty makes everybody tap twice in the
    // same place before typing anything at all.
    if (tipo.isBlank() && estado.meta.tipos.isNotEmpty()) {
        tipo = estado.meta.tipos.first()
    }

    ModalBottomSheet(
        onDismissRequest = aoFechar,
        sheetState = folha,
        modifier = Modifier.testTag(TAG_FOLHA_CRIACAO),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(max = 620.dp)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp)
                .padding(bottom = 24.dp),
        ) {
            Text(
                text = if (projeto.isBlank()) "New issue" else "New issue in $projeto",
                style = MaterialTheme.typography.titleLarge,
            )

            if (projeto.isBlank()) {
                Spacer(Modifier.height(12.dp))
                Text(
                    text = "Pick a project on the board before creating — without a project, Jira does not know where the issue belongs.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(16.dp))
                TextButton(onClick = aoFechar) { Text("Close") }
                return@Column
            }

            Spacer(Modifier.height(16.dp))
            OutlinedTextField(
                value = resumo,
                onValueChange = { resumo = it },
                label = { Text("Summary") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )

            Spacer(Modifier.height(12.dp))
            OutlinedTextField(
                value = descricao,
                onValueChange = { descricao = it },
                label = { Text("Description (optional)") },
                modifier = Modifier.fillMaxWidth(),
            )

            if (estado.carregandoMeta) {
                Spacer(Modifier.height(16.dp))
                Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            }

            if (estado.meta.tipos.isNotEmpty()) {
                Spacer(Modifier.height(16.dp))
                Text("Type", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                LinhaDeEscolhas(
                    opcoes = estado.meta.tipos,
                    escolhida = tipo,
                    aoEscolher = { tipo = it },
                )
            }

            if (estado.meta.prioridades.isNotEmpty()) {
                Spacer(Modifier.height(12.dp))
                Text("Priority (optional)", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.height(4.dp))
                LinhaDeEscolhas(
                    opcoes = estado.meta.prioridades,
                    escolhida = prioridade,
                    aoEscolher = { prioridade = if (prioridade == it) "" else it },
                )
            }

            if (estado.pessoas.isNotEmpty()) {
                Spacer(Modifier.height(12.dp))
                Text("Assignee (optional)", style = MaterialTheme.typography.labelLarge)
                Box {
                    TextButton(onClick = { menuDePessoas = true }) {
                        Text(responsavel?.nome ?: "Nobody")
                    }
                    DropdownMenu(expanded = menuDePessoas, onDismissRequest = { menuDePessoas = false }) {
                        DropdownMenuItem(
                            text = { Text("Nobody") },
                            onClick = {
                                responsavel = null
                                menuDePessoas = false
                            },
                        )
                        estado.pessoas.forEach { p ->
                            DropdownMenuItem(
                                text = { Text(p.nome) },
                                onClick = {
                                    responsavel = p
                                    menuDePessoas = false
                                },
                            )
                        }
                    }
                }
            }

            Spacer(Modifier.height(20.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(
                    onClick = {
                        aoCriar(
                            NovaIssue(
                                projeto = projeto,
                                tipo = tipo,
                                resumo = resumo.trim(),
                                descricao = descricao.trim().takeIf { it.isNotBlank() },
                                prioridade = prioridade.takeIf { it.isNotBlank() },
                                responsavelId = responsavel?.accountId,
                            ),
                        )
                    },
                    enabled = resumo.isNotBlank() && tipo.isNotBlank() && !estado.enviando,
                ) {
                    Text(if (estado.enviando) "Creating…" else "Create")
                }
                TextButton(onClick = aoFechar, enabled = !estado.enviando) { Text("Cancel") }
            }
        }
    }
}

/** A row of exclusive choices, wrapping onto the next line. */
@Composable
private fun LinhaDeEscolhas(
    opcoes: List<String>,
    escolhida: String,
    aoEscolher: (String) -> Unit,
) {
    androidx.compose.foundation.layout.FlowRow(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        opcoes.forEach { opcao ->
            FilterChip(
                selected = escolhida == opcao,
                onClick = { aoEscolher(opcao) },
                label = { Text(opcao, maxLines = 1) },
            )
        }
    }
}
