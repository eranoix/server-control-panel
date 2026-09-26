package com.vpsmanager.feature.jira

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.vpsmanager.data.jira.CartaoDoJira
import com.vpsmanager.data.jira.ColunaDoJira
import com.vpsmanager.data.jira.QuadroDoJira
import kotlinx.coroutines.delay

/** Test tags — the UI and the test both read from here, never duplicated literals. */
internal const val TAG_QUADRO = "jira-quadro"
internal const val TAG_ARRASTANDO = "jira-cartao-arrastando"

/** How many columns fit on screen at once. */
private const val COLUNAS_VISIVEIS = 3

/**
 * The Jira kanban board.
 *
 * ## Three columns on screen, and why that changes everything
 *
 * The first version showed ONE column per page, with the next one peeking at
 * the edge — which is, incidentally, what Trello, official Jira and most
 * kanban apps do on a phone. And it is bad: a board exists to show WHERE the
 * work has piled up, and one column at a time is a list with tabs.
 *
 * The alternative products reach for when they want the whole board is to zoom
 * out: narrow column, compact card. That is the path taken here. On a 411 dp
 * screen, three columns give about 125 dp each — and that is why the card lost
 * its labels, its priority and its spelled-out status (see [CartaoDoQuadro]).
 * Less per card, more cards in view.
 *
 * ## And dragging becomes trivial
 *
 * With every column on screen, the destination is **the column under the
 * finger** — no paging, no edge timer, no guessing where the board was about
 * to turn. The column beneath the card is highlighted while it is in the air,
 * so dropping is never a bet. Edge scrolling still exists, but it only serves
 * boards with MORE than three columns.
 *
 * ## Why there is no pull-to-refresh
 *
 * Pulling down at the top of the list would compete with the vertical drag of
 * a card that has just been picked up. Refreshing is rare enough to live in a
 * button, and an ambiguous gesture on a board where the other gesture MOVES
 * real work would be expensive the day the tie-break got it wrong.
 */
@Composable
fun QuadroDoJiraRoute(
    modifier: Modifier = Modifier,
    vm: QuadroViewModel = viewModel(),
) {
    val estado by vm.estado.collectAsStateWithLifecycle()
    val issue by vm.issue.collectAsStateWithLifecycle()
    val criacao by vm.criacao.collectAsStateWithLifecycle()
    val recado by vm.recado.collectAsStateWithLifecycle()
    val selecao by vm.selecao.collectAsStateWithLifecycle()
    val selecionando by vm.selecionando.collectAsStateWithLifecycle()
    val ocupado by vm.ocupado.collectAsStateWithLifecycle()

    val avisos = remember { SnackbarHostState() }
    LaunchedEffect(recado) {
        val texto = recado ?: return@LaunchedEffect
        avisos.showSnackbar(texto)
        vm.consumirRecado()
    }

    Scaffold(
        modifier = modifier.fillMaxSize(),
        snackbarHost = { SnackbarHost(avisos) },
        floatingActionButton = {
            if (estado is EstadoDoQuadro.Pronto && !selecionando) {
                FloatingActionButton(onClick = vm::abrirCriacao) {
                    Icon(Icons.Filled.Add, contentDescription = "Create issue")
                }
            }
        },
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            when (val e = estado) {
                is EstadoDoQuadro.Carregando -> Centralizado { CircularProgressIndicator() }

                is EstadoDoQuadro.Desconectado -> TelaDeConexao(
                    ocupado = ocupado,
                    aoConectar = vm::conectar,
                )

                is EstadoDoQuadro.Erro -> Centralizado {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(e.mensagem, style = MaterialTheme.typography.bodyMedium)
                        Spacer(Modifier.height(12.dp))
                        Button(onClick = vm::carregar) { Text("Try again") }
                    }
                }

                is EstadoDoQuadro.Pronto -> Quadro(
                    quadro = e.quadro,
                    recorte = vm.recorteAtual,
                    selecionando = selecionando,
                    selecao = selecao,
                    ocupado = ocupado,
                    vm = vm,
                )
            }
        }
    }

    FolhaDaIssue(
        estado = issue,
        aoFechar = vm::fecharIssue,
        aoMover = { chave, coluna -> vm.mover(chave, coluna) },
        aoComentar = vm::comentar,
        aoAtribuirAMim = vm::atribuirAMim,
        aoDesatribuir = { chave -> vm.atribuir(chave, null, null) },
    )

    if (criacao.aberto) {
        FolhaDeCriacao(
            estado = criacao,
            projeto = vm.recorteAtual.projeto.orEmpty(),
            aoFechar = vm::fecharCriacao,
            aoCriar = vm::criar,
        )
    }
}

@Composable
private fun Centralizado(conteudo: @Composable () -> Unit) {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { conteudo() }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Quadro(
    quadro: QuadroDoJira,
    recorte: QuadroViewModel.Recorte,
    selecionando: Boolean,
    selecao: Set<String>,
    ocupado: Boolean,
    vm: QuadroViewModel,
) {
    val colunas = quadro.colunas
    val arrasto = remember { EstadoDoArrasto() }
    val rolagem = rememberScrollState()
    val vibrar = LocalHapticFeedback.current

    // Edge scrolling only makes sense when there are MORE columns than fit.
    // With three on a three-column board the finger already reaches everything,
    // and a board that slides by itself mid-drag would be motion nobody asked
    // for.
    val borda = if (colunas.size > COLUNAS_VISIVEIS) arrasto.borda() else null
    LaunchedEffect(borda) {
        if (borda == null) return@LaunchedEffect
        while (true) {
            delay(90)
            val passo = when (borda) {
                BordaDoArrasto.Esquerda -> -40f
                BordaDoArrasto.Direita -> 40f
            }
            if (rolagem.scrollBy(passo) == 0f) break
        }
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .testTag(TAG_QUADRO)
            .onGloballyPositioned { arrasto.larguraDaRaiz = it.size.width },
    ) {
        Column(modifier = Modifier.fillMaxSize()) {
            BarraDeControles(quadro = quadro, recorte = recorte, selecionando = selecionando, vm = vm)

            if (selecionando) {
                BarraDeSelecao(
                    selecionadas = selecao.size,
                    colunas = colunas.map(ColunaDoJira::rotulo),
                    ocupado = ocupado,
                    aoMover = vm::moverSelecao,
                    aoAtribuirAMim = vm::atribuirSelecaoAMim,
                    aoLimpar = vm::limparSelecao,
                    aoSair = vm::alternarModoSelecao,
                )
            }

            quadro.recusa?.let { motivo ->
                // Jira's refusal takes the place of the cards, not of the
                // whole screen: the controls stay up so the filter that caused
                // the refusal can be changed.
                Text(
                    text = motivo,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onErrorContainer,
                    modifier = Modifier
                        .padding(horizontal = 8.dp, vertical = 4.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(MaterialTheme.colorScheme.errorContainer)
                        .padding(8.dp)
                        .fillMaxWidth(),
                )
            }

            if (colunas.isEmpty()) {
                Centralizado { Text("This board has no columns.") }
                return@Column
            }

            val alvo = arrasto.colunaAlvo()

            BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
                // The column width is whatever the screen has left, divided
                // by three. It is not an aesthetic choice: it is the sum that
                // makes "see all three at once" fit.
                val vao = 6.dp
                val margem = 8.dp
                val largura = (maxWidth - margem * 2 - vao * (COLUNAS_VISIVEIS - 1)) / COLUNAS_VISIVEIS

                Row(
                    modifier = Modifier
                        .fillMaxSize()
                        .horizontalScroll(rolagem, enabled = !arrasto.arrastando)
                        .padding(horizontal = margem),
                    horizontalArrangement = Arrangement.spacedBy(vao),
                ) {
                    colunas.forEach { coluna ->
                        ColunaDoQuadro(
                            coluna = coluna,
                            largura = largura,
                            arrasto = arrasto,
                            destacada = alvo == coluna.rotulo,
                            selecionando = selecionando,
                            selecao = selecao,
                            aoAbrir = vm::abrirIssue,
                            aoSelecionar = vm::alternarSelecao,
                            aoPegar = { vibrar.performHapticFeedback(HapticFeedbackType.LongPress) },
                            aoSoltar = { cartao ->
                                // The destination is the column under the
                                // finger AT THE MOMENT it lifts. Dropping
                                // outside all of them moves nothing — that is
                                // "I changed my mind".
                                arrasto.colunaAlvo()?.let { vm.mover(cartao.chave, it) }
                            },
                        )
                    }
                }
            }
        }

        // The floating card lives at the ROOT, above everything: inside the
        // column it would be clipped at the column's bounds — precisely the
        // bounds the gesture exists to cross.
        arrasto.cartao?.let { cartao ->
            val densidade = LocalDensity.current
            CartaoDoQuadro(
                cartao = cartao,
                modifier = Modifier
                    .width(with(densidade) { arrasto.tamanho.width.toDp() })
                    .graphicsLayer {
                        translationX = arrasto.origemNaRaiz.x + arrasto.deslocamento.x
                        translationY = arrasto.origemNaRaiz.y + arrasto.deslocamento.y
                        // Lifted off the board: slightly larger and with a
                        // shadow, so it is not confused with the stationary
                        // cards underneath it.
                        scaleX = 1.06f
                        scaleY = 1.06f
                        shadowElevation = 18f
                        alpha = 0.97f
                    }
                    .testTag(TAG_ARRASTANDO),
            )
        }
    }
}

/**
 * One column: a header with a label and a count, and the list of cards.
 *
 * [destacada] is the column under the finger during a drag. The outline exists
 * because dropping without it would be a bet — at 125 dp wide, the difference
 * between "I am over the second" and "I am over the third" is millimetres.
 */
@Composable
private fun ColunaDoQuadro(
    coluna: ColunaDoJira,
    largura: androidx.compose.ui.unit.Dp,
    arrasto: EstadoDoArrasto,
    destacada: Boolean,
    selecionando: Boolean,
    selecao: Set<String>,
    aoAbrir: (String) -> Unit,
    aoSelecionar: (String) -> Unit,
    aoPegar: () -> Unit,
    aoSoltar: (CartaoDoJira) -> Unit,
) {
    Column(
        modifier = Modifier
            .width(largura)
            .fillMaxHeight()
            .onGloballyPositioned {
                val x = it.positionInRoot().x
                arrasto.registrarColuna(coluna.rotulo, x, x + it.size.width)
            }
            .clip(RoundedCornerShape(10.dp))
            .background(
                if (destacada) {
                    MaterialTheme.colorScheme.secondaryContainer
                } else {
                    MaterialTheme.colorScheme.surfaceContainerLowest
                },
            )
            .border(
                width = if (destacada) 2.dp else 0.dp,
                color = if (destacada) MaterialTheme.colorScheme.primary else androidx.compose.ui.graphics.Color.Transparent,
                shape = RoundedCornerShape(10.dp),
            ),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = coluna.rotulo,
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            Spacer(Modifier.width(4.dp))
            Text(
                text = coluna.cartoes.size.toString(),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        HorizontalDivider()

        if (coluna.cartoes.isEmpty()) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.TopCenter) {
                Text(
                    text = "empty",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 16.dp),
                )
            }
            return@Column
        }

        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = PaddingValues(6.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            items(coluna.cartoes, key = { it.chave }) { cartao ->
                val noAr = arrasto.cartao?.chave == cartao.chave
                CartaoDoQuadro(
                    cartao = cartao,
                    selecionando = selecionando,
                    selecionado = cartao.chave in selecao,
                    aoSelecionar = { aoSelecionar(cartao.chave) },
                    modifier = Modifier
                        // The original card stays faded in place while the
                        // clone travels: removing it would make the column
                        // shrink and the cards below jump mid-gesture.
                        .alpha(if (noAr) 0.25f else 1f)
                        .arrastavel(
                            estado = arrasto,
                            cartao = cartao,
                            coluna = coluna.rotulo,
                            // While multi-select is on, the tap has another owner.
                            habilitado = !selecionando,
                            aoPegar = aoPegar,
                            aoSoltar = aoSoltar,
                        )
                        .clickable(
                            onClickLabel = if (selecionando) "Select" else "Open issue",
                            onClick = {
                                if (selecionando) aoSelecionar(cartao.chave) else aoAbrir(cartao.chave)
                            },
                        ),
                )
            }
        }
    }
}

/** The bar holding the project, the filters and search. */
@Composable
private fun BarraDeControles(
    quadro: QuadroDoJira,
    recorte: QuadroViewModel.Recorte,
    selecionando: Boolean,
    vm: QuadroViewModel,
) {
    var buscando by remember { mutableStateOf(false) }
    var textoBuscado by remember { mutableStateOf(recorte.busca) }
    var menuDeProjeto by remember { mutableStateOf(false) }
    var menuMais by remember { mutableStateOf(false) }

    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box {
                AssistChip(
                    onClick = { menuDeProjeto = true },
                    label = {
                        Text(
                            text = quadro.projeto?.takeIf { it.isNotBlank() } ?: "Project",
                            maxLines = 1,
                        )
                    },
                )
                DropdownMenu(expanded = menuDeProjeto, onDismissRequest = { menuDeProjeto = false }) {
                    if (quadro.projetos.isEmpty()) {
                        DropdownMenuItem(
                            text = { Text("No projects visible to this account") },
                            onClick = { menuDeProjeto = false },
                        )
                    }
                    quadro.projetos.forEach { p ->
                        DropdownMenuItem(
                            text = { Text("${p.chave} — ${p.nome}", maxLines = 1) },
                            onClick = {
                                menuDeProjeto = false
                                vm.trocarProjeto(p.chave)
                            },
                        )
                    }
                }
            }

            // The filters come from the SERVER, labels and all. A fixed list
            // here would go stale on its own the day the panel gained a new
            // filter — which is exactly how the two surfaces diverged last
            // time.
            //
            // They sit on the SAME line as the project and the buttons: the
            // previous version spent two bands of height on rows of similar
            // pills — one of filters, one of columns — which together ate a
            // fifth of the screen and were mistaken for each other. The column
            // row is gone because the columns are now in plain sight; this one
            // moved down here.
            Row(
                modifier = Modifier
                    .weight(1f)
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = 6.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                quadro.filtros.forEach { f ->
                    FilterChip(
                        selected = quadro.filtro == f.chave,
                        onClick = { vm.trocarFiltro(f.chave) },
                        label = { Text(f.rotulo, maxLines = 1) },
                    )
                }
            }

            IconButton(onClick = { buscando = !buscando }) {
                Icon(Icons.Filled.Search, contentDescription = "Search issues")
            }
            Box {
                IconButton(onClick = { menuMais = true }) {
                    Icon(Icons.Filled.MoreVert, contentDescription = "More board options")
                }
                DropdownMenu(expanded = menuMais, onDismissRequest = { menuMais = false }) {
                    DropdownMenuItem(
                        text = { Text("Refresh") },
                        leadingIcon = { Icon(Icons.Filled.Refresh, contentDescription = null) },
                        onClick = {
                            menuMais = false
                            vm.carregar()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(if (selecionando) "Exit selection" else "Select multiple") },
                        onClick = {
                            menuMais = false
                            vm.alternarModoSelecao()
                        },
                    )
                    HorizontalDivider()
                    listOf(
                        "" to "Jira order",
                        "updated:desc" to "Recently updated first",
                        "key:asc" to "By key",
                        "name:asc" to "By summary",
                        "type:asc" to "By type",
                    ).forEach { (criterio, rotulo) ->
                        DropdownMenuItem(
                            text = { Text(rotulo) },
                            leadingIcon = {
                                if (recorte.ordem == criterio) {
                                    Icon(Icons.Filled.Check, contentDescription = null)
                                }
                            },
                            onClick = {
                                menuMais = false
                                vm.ordenarPor(criterio)
                            },
                        )
                    }
                    HorizontalDivider()
                    listOf(
                        0 to "Show all done",
                        7 to "Done in the last 7 days",
                        30 to "Done in the last 30 days",
                    ).forEach { (dias, rotulo) ->
                        DropdownMenuItem(
                            text = { Text(rotulo) },
                            leadingIcon = {
                                if (recorte.ocultarConcluidasApos == dias) {
                                    Icon(Icons.Filled.Check, contentDescription = null)
                                }
                            },
                            onClick = {
                                menuMais = false
                                vm.ocultarConcluidasApos(dias)
                            },
                        )
                    }
                }
            }
        }

        if (buscando) {
            OutlinedTextField(
                value = textoBuscado,
                onValueChange = { textoBuscado = it },
                label = { Text("Search by key, summary, label or person") },
                singleLine = true,
                trailingIcon = {
                    IconButton(onClick = {
                        textoBuscado = ""
                        vm.buscar("")
                    }) {
                        Icon(Icons.Filled.Close, contentDescription = "Clear search")
                    }
                },
                modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp),
            )
            Row(modifier = Modifier.padding(horizontal = 12.dp)) {
                TextButton(onClick = { vm.buscar(textoBuscado) }) { Text("Search") }
            }
        }
    }
}

/** The bar that appears while multi-select is on. */
@Composable
private fun BarraDeSelecao(
    selecionadas: Int,
    colunas: List<String>,
    ocupado: Boolean,
    aoMover: (String) -> Unit,
    aoAtribuirAMim: () -> Unit,
    aoLimpar: () -> Unit,
    aoSair: () -> Unit,
) {
    var menuMover by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.secondaryContainer)
            .padding(horizontal = 12.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = if (selecionadas == 0) "None selected" else "$selecionadas selected",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSecondaryContainer,
        )
        Spacer(Modifier.weight(1f))
        if (ocupado) {
            CircularProgressIndicator(modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
        }
        Box {
            TextButton(onClick = { menuMover = true }, enabled = selecionadas > 0 && !ocupado) {
                Text("Move")
            }
            DropdownMenu(expanded = menuMover, onDismissRequest = { menuMover = false }) {
                colunas.forEach { rotulo ->
                    DropdownMenuItem(
                        text = { Text(rotulo) },
                        onClick = {
                            menuMover = false
                            aoMover(rotulo)
                        },
                    )
                }
            }
        }
        TextButton(onClick = aoAtribuirAMim, enabled = selecionadas > 0 && !ocupado) {
            Text("Assign to me")
        }
        IconButton(onClick = if (selecionadas > 0) aoLimpar else aoSair) {
            Icon(
                Icons.Filled.Close,
                contentDescription = if (selecionadas > 0) "Clear selection" else "Exit selection",
            )
        }
    }
}
