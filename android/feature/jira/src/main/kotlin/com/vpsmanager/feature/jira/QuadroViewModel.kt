package com.vpsmanager.feature.jira

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.vpsmanager.data.jira.CartaoDoJira
import com.vpsmanager.data.jira.ColunaDoJira
import com.vpsmanager.data.jira.FonteDoJira
import com.vpsmanager.data.jira.IssueDoJira
import com.vpsmanager.data.jira.JiraRepository
import com.vpsmanager.data.jira.MetaDoJira
import com.vpsmanager.data.jira.NovaIssue
import com.vpsmanager.data.jira.PessoaDoJira
import com.vpsmanager.data.jira.QuadroDoJira
import com.vpsmanager.data.jira.ResultadoDoJira
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** The state of the board screen. */
sealed interface EstadoDoQuadro {
    data object Carregando : EstadoDoQuadro

    /** No Jira account is connected yet — the way forward is the form, not an error. */
    data object Desconectado : EstadoDoQuadro

    data class Pronto(val quadro: QuadroDoJira) : EstadoDoQuadro
    data class Erro(val mensagem: String) : EstadoDoQuadro
}

/** The state of an open issue's sheet. */
sealed interface EstadoDaIssue {
    data object Fechada : EstadoDaIssue
    data class Carregando(val chave: String) : EstadoDaIssue
    data class Pronta(val issue: IssueDoJira) : EstadoDaIssue
    data class Erro(val chave: String, val mensagem: String) : EstadoDaIssue
}

/** The state of the creation form. */
data class EstadoDaCriacao(
    val aberto: Boolean = false,
    val carregandoMeta: Boolean = false,
    val meta: MetaDoJira = MetaDoJira(emptyList(), emptyList()),
    val pessoas: List<PessoaDoJira> = emptyList(),
    val enviando: Boolean = false,
)

/**
 * Drives the Jira board.
 *
 * ## The move is optimistic, and undoing it is mandatory
 *
 * When a card is dropped into a column, it changes place immediately — before
 * the server answers. Without that, the card would sit pinned under the finger
 * for a whole network round trip, and on a phone that is hundreds of
 * milliseconds in which the screen contradicts the gesture.
 *
 * The price of that choice is that the undo has to be exact: when the server
 * refuses — and it does refuse, because the project's workflow forbids certain
 * jumps — the card returns to the column AND to the position it came from,
 * with the reason on screen. Dropping it and letting it look like it worked is
 * the worst possible outcome: the person goes on believing they moved it, and
 * finds out days later.
 *
 * ## Why multi-select does not use long-press
 *
 * Long-press is the gesture for PICKING UP a card. Using it to select as well
 * would give one gesture two meanings, and the tie-break would come down to a
 * hold duration nobody hits on purpose. Selection is turned on by an explicit
 * menu item, and while it is on the cards show checkboxes — dragging still
 * works, but anyone who wants to tick taps the box.
 */
class QuadroViewModel(
    private val fonte: FonteDoJira = JiraRepository(),
) : ViewModel() {

    private val _estado = MutableStateFlow<EstadoDoQuadro>(EstadoDoQuadro.Carregando)
    val estado: StateFlow<EstadoDoQuadro> = _estado.asStateFlow()

    private val _issue = MutableStateFlow<EstadoDaIssue>(EstadoDaIssue.Fechada)
    val issue: StateFlow<EstadoDaIssue> = _issue.asStateFlow()

    private val _criacao = MutableStateFlow(EstadoDaCriacao())
    val criacao: StateFlow<EstadoDaCriacao> = _criacao.asStateFlow()

    /**
     * The last sentence of feedback. The screen consumes it and clears it.
     *
     * It exists because every action here happens FAR from its effect: whoever
     * moves a card is looking at the destination column, and what changes is
     * the issue on the server. Without a sentence, success and silence look
     * identical.
     */
    private val _recado = MutableStateFlow<String?>(null)
    val recado: StateFlow<String?> = _recado.asStateFlow()

    private val _selecao = MutableStateFlow<Set<String>>(emptySet())
    val selecao: StateFlow<Set<String>> = _selecao.asStateFlow()

    private val _selecionando = MutableStateFlow(false)
    val selecionando: StateFlow<Boolean> = _selecionando.asStateFlow()

    private val _ocupado = MutableStateFlow(false)
    val ocupado: StateFlow<Boolean> = _ocupado.asStateFlow()

    // The board's current filter. Kept here rather than on the screen so that
    // a reload (pull to refresh, coming back from an action) repeats exactly
    // the same filter instead of falling back to the default.
    private var projeto: String? = null
    private var filtro: String = "all"
    private var jqlProprio: String = ""
    private var busca: String = ""
    private var ordem: String = ""
    private var ocultarConcluidasApos: Int = 0

    /** The current filter, so the screen can draw the controls as ticked. */
    val recorteAtual: Recorte
        get() = Recorte(projeto, filtro, jqlProprio, busca, ordem, ocultarConcluidasApos)

    data class Recorte(
        val projeto: String?,
        val filtro: String,
        val jql: String,
        val busca: String,
        val ordem: String,
        val ocultarConcluidasApos: Int,
    )

    init {
        carregar()
    }

    fun carregar() {
        viewModelScope.launch {
            // Reloading does NOT go back to "Loading" when there is already
            // a board on screen: replacing the board with a spinner on every
            // filter change makes the screen flash white and loses the scroll
            // position.
            if (_estado.value !is EstadoDoQuadro.Pronto) {
                _estado.value = EstadoDoQuadro.Carregando
            }
            when (
                val r = fonte.quadro(
                    projeto = projeto,
                    filtro = filtro,
                    jql = jqlProprio,
                    busca = busca,
                    ordem = ordem,
                    ocultarConcluidasApos = ocultarConcluidasApos,
                )
            ) {
                is ResultadoDoJira.Ok -> {
                    val q = r.valor
                    _estado.value = if (!q.conectado) EstadoDoQuadro.Desconectado else EstadoDoQuadro.Pronto(q)
                    if (q.conectado && projeto == null) projeto = q.projeto
                    // The selection held keys that may no longer be on the
                    // board after a new filter. Keeping them would leave the
                    // counter saying "3 selected" with one on screen.
                    podarSelecao(q)
                }
                is ResultadoDoJira.Recusa -> _estado.value = EstadoDoQuadro.Erro(r.motivo)
                is ResultadoDoJira.Erro -> _estado.value = EstadoDoQuadro.Erro(r.motivo)
            }
        }
    }

    private fun podarSelecao(q: QuadroDoJira) {
        val visiveis = q.colunas.flatMap { c -> c.cartoes.map { it.chave } }.toSet()
        _selecao.update { atual -> atual.intersect(visiveis) }
        if (_selecao.value.isEmpty() && _selecionando.value.not()) return
    }

    fun trocarProjeto(chave: String) {
        projeto = chave
        _selecao.value = emptySet()
        carregar()
        // Pinning it on the server is what makes the choice survive the app's
        // next launch — and it is the SAME preference the web panel uses, so
        // changing it here changes it there.
        viewModelScope.launch { fonte.fixarProjeto(chave) }
    }

    fun trocarFiltro(chave: String) {
        filtro = chave
        carregar()
    }

    fun trocarJQL(jql: String) {
        jqlProprio = jql
        filtro = "custom"
        carregar()
    }

    fun buscar(texto: String) {
        busca = texto
        carregar()
    }

    fun ordenarPor(criterio: String) {
        ordem = criterio
        carregar()
    }

    fun ocultarConcluidasApos(dias: Int) {
        ocultarConcluidasApos = dias
        carregar()
    }

    // --- move --------------------------------------------------------------

    /**
     * Moves a card to a column, with the card changing place right away.
     *
     * [origem] and [posicao] are captured before anything else: they are what
     * allows the card to be put back in its EXACT place when the server
     * refuses.
     */
    fun mover(chave: String, paraColuna: String) {
        val pronto = _estado.value as? EstadoDoQuadro.Pronto ?: return
        val quadro = pronto.quadro
        val origem = quadro.colunas.firstOrNull { c -> c.cartoes.any { it.chave == chave } } ?: return
        if (origem.rotulo == paraColuna) return
        val posicao = origem.cartoes.indexOfFirst { it.chave == chave }
        val cartao = origem.cartoes[posicao]

        _estado.value = EstadoDoQuadro.Pronto(quadro.comCartaoMovido(chave, paraColuna))

        viewModelScope.launch {
            when (val r = fonte.mover(chave, paraColuna)) {
                is ResultadoDoJira.Ok -> {
                    _recado.value = "$chave → $paraColuna"
                    // Reload to get the REAL status: the destination column
                    // can map to several statuses, and the card needs to show
                    // which of them the transition left it in.
                    carregar()
                }
                is ResultadoDoJira.Recusa -> {
                    devolverCartao(cartao, origem.rotulo, posicao)
                    _recado.value = r.motivo
                }
                is ResultadoDoJira.Erro -> {
                    devolverCartao(cartao, origem.rotulo, posicao)
                    _recado.value = r.motivo
                }
            }
        }
    }

    private fun devolverCartao(cartao: CartaoDoJira, paraColuna: String, posicao: Int) {
        val pronto = _estado.value as? EstadoDoQuadro.Pronto ?: return
        _estado.value = EstadoDoQuadro.Pronto(
            pronto.quadro.semCartao(cartao.chave).comCartaoEm(cartao, paraColuna, posicao),
        )
    }

    // --- the open issue ------------------------------------------------------

    fun abrirIssue(chave: String) {
        _issue.value = EstadoDaIssue.Carregando(chave)
        viewModelScope.launch {
            _issue.value = when (val r = fonte.issue(chave)) {
                is ResultadoDoJira.Ok -> EstadoDaIssue.Pronta(r.valor)
                is ResultadoDoJira.Recusa -> EstadoDaIssue.Erro(chave, r.motivo)
                is ResultadoDoJira.Erro -> EstadoDaIssue.Erro(chave, r.motivo)
            }
        }
    }

    fun fecharIssue() {
        _issue.value = EstadoDaIssue.Fechada
    }

    fun comentar(chave: String, texto: String) {
        if (texto.isBlank()) return
        viewModelScope.launch {
            _ocupado.value = true
            when (val r = fonte.comentar(chave, texto)) {
                is ResultadoDoJira.Ok -> {
                    // The new comment appears in the open sheet without a
                    // second trip to the server — someone who has just written
                    // something needs to see their own text in place.
                    val atual = _issue.value
                    if (atual is EstadoDaIssue.Pronta && atual.issue.chave == chave) {
                        _issue.value = EstadoDaIssue.Pronta(
                            atual.issue.copy(comentarios = atual.issue.comentarios + r.valor),
                        )
                    }
                    _recado.value = "Comment posted on $chave"
                }
                is ResultadoDoJira.Recusa -> _recado.value = r.motivo
                is ResultadoDoJira.Erro -> _recado.value = r.motivo
            }
            _ocupado.value = false
        }
    }

    /** Assigns the issue to someone; a null [accountId] unassigns it. */
    fun atribuir(chave: String, accountId: String?, nome: String?) {
        viewModelScope.launch {
            _ocupado.value = true
            when (val r = fonte.atribuir(chave, accountId)) {
                is ResultadoDoJira.Ok -> {
                    _recado.value = if (accountId == null) {
                        "$chave unassigned"
                    } else {
                        "$chave assigned to ${nome ?: "the new assignee"}"
                    }
                    if (_issue.value is EstadoDaIssue.Pronta) abrirIssue(chave)
                    carregar()
                }
                is ResultadoDoJira.Recusa -> _recado.value = r.motivo
                is ResultadoDoJira.Erro -> _recado.value = r.motivo
            }
            _ocupado.value = false
        }
    }

    /** Assigns the issue to whoever is using the app. */
    fun atribuirAMim(chave: String) {
        val eu = (_estado.value as? EstadoDoQuadro.Pronto)?.quadro?.eu
        if (eu == null) {
            _recado.value = "The server did not say who you are in Jira."
            return
        }
        atribuir(chave, eu.accountId, eu.nome)
    }

    // --- selection and batches -----------------------------------------------

    fun alternarModoSelecao() {
        _selecionando.update { !it }
        if (!_selecionando.value) _selecao.value = emptySet()
    }

    fun alternarSelecao(chave: String) {
        _selecao.update { atual -> if (chave in atual) atual - chave else atual + chave }
    }

    fun limparSelecao() {
        _selecao.value = emptySet()
    }

    fun moverSelecao(paraColuna: String) {
        val chaves = _selecao.value.toList()
        if (chaves.isEmpty()) return
        viewModelScope.launch {
            _ocupado.value = true
            when (val r = fonte.moverEmLote(chaves, paraColuna)) {
                is ResultadoDoJira.Ok -> {
                    _recado.value = resumoDoLote(r.valor.feitas.size, r.valor.falhas)
                    _selecao.value = emptySet()
                    carregar()
                }
                is ResultadoDoJira.Recusa -> _recado.value = r.motivo
                is ResultadoDoJira.Erro -> _recado.value = r.motivo
            }
            _ocupado.value = false
        }
    }

    fun atribuirSelecaoAMim() {
        val chaves = _selecao.value.toList()
        if (chaves.isEmpty()) return
        val eu = (_estado.value as? EstadoDoQuadro.Pronto)?.quadro?.eu
        if (eu == null) {
            _recado.value = "The server did not say who you are in Jira."
            return
        }
        viewModelScope.launch {
            _ocupado.value = true
            when (val r = fonte.atribuirEmLote(chaves, eu.accountId)) {
                is ResultadoDoJira.Ok -> {
                    _recado.value = resumoDoLote(r.valor.feitas.size, r.valor.falhas)
                    _selecao.value = emptySet()
                    carregar()
                }
                is ResultadoDoJira.Recusa -> _recado.value = r.motivo
                is ResultadoDoJira.Erro -> _recado.value = r.motivo
            }
            _ocupado.value = false
        }
    }

    // --- create ---------------------------------------------------------------

    fun abrirCriacao() {
        val proj = projeto ?: (_estado.value as? EstadoDoQuadro.Pronto)?.quadro?.projeto
        _criacao.value = EstadoDaCriacao(aberto = true, carregandoMeta = proj != null)
        if (proj == null) return
        viewModelScope.launch {
            val meta = fonte.meta(proj)
            val pessoas = fonte.pessoas(proj)
            _criacao.update {
                it.copy(
                    carregandoMeta = false,
                    meta = (meta as? ResultadoDoJira.Ok)?.valor ?: MetaDoJira(emptyList(), emptyList()),
                    pessoas = (pessoas as? ResultadoDoJira.Ok)?.valor.orEmpty(),
                )
            }
        }
    }

    fun fecharCriacao() {
        _criacao.value = EstadoDaCriacao()
    }

    fun criar(nova: NovaIssue) {
        viewModelScope.launch {
            _criacao.update { it.copy(enviando = true) }
            when (val r = fonte.criar(nova)) {
                is ResultadoDoJira.Ok -> {
                    _criacao.value = EstadoDaCriacao()
                    _recado.value = "${r.valor} created"
                    carregar()
                }
                is ResultadoDoJira.Recusa -> {
                    _criacao.update { it.copy(enviando = false) }
                    _recado.value = r.motivo
                }
                is ResultadoDoJira.Erro -> {
                    _criacao.update { it.copy(enviando = false) }
                    _recado.value = r.motivo
                }
            }
        }
    }

    // --- connect --------------------------------------------------------------

    fun conectar(site: String, email: String, token: String, projeto: String?) {
        viewModelScope.launch {
            _ocupado.value = true
            when (val r = fonte.conectar(site, email, token, projeto)) {
                is ResultadoDoJira.Ok -> {
                    this@QuadroViewModel.projeto = projeto?.takeIf { it.isNotBlank() }
                    carregar()
                }
                is ResultadoDoJira.Recusa -> _estado.value = EstadoDoQuadro.Erro(r.motivo)
                is ResultadoDoJira.Erro -> _estado.value = EstadoDoQuadro.Erro(r.motivo)
            }
            _ocupado.value = false
        }
    }

    fun consumirRecado() {
        _recado.value = null
    }
}

/**
 * The sentence that sums up a batch.
 *
 * It names the issues that FAILED, not just how many: those are the ones that
 * need acting on, and a bare "2 failed" forces you to compare the board before
 * with the board after to work out which. Past three, the first one's reason
 * stands for the set — the whole sentence still has to fit in a snackbar.
 */
internal fun resumoDoLote(feitas: Int, falhas: List<com.vpsmanager.data.jira.FalhaEmLote>): String {
    if (falhas.isEmpty()) return "$feitas moved"
    if (feitas == 0 && falhas.size == 1) return "${falhas[0].chave}: ${falhas[0].motivo}"
    val nomes = falhas.take(3).joinToString(", ") { it.chave }
    val resto = if (falhas.size > 3) " and ${falhas.size - 3} more" else ""
    return "$feitas done; failed: $nomes$resto — ${falhas[0].motivo}"
}

// --- board transformations -----------------------------------------------------
//
// Deliberately pure: they are the half of the optimistic move that has to be
// exercisable with no network, no ViewModel and no Compose.

/** The board without a given card, wherever it happens to be. */
internal fun QuadroDoJira.semCartao(chave: String): QuadroDoJira =
    copy(colunas = colunas.map { c -> c.copy(cartoes = c.cartoes.filterNot { it.chave == chave }) })

/** The board with a card inserted at an exact position in a column. */
internal fun QuadroDoJira.comCartaoEm(cartao: CartaoDoJira, coluna: String, posicao: Int): QuadroDoJira =
    copy(
        colunas = colunas.map { c ->
            if (c.rotulo != coluna) {
                c
            } else {
                val destino = c.cartoes.toMutableList()
                destino.add(posicao.coerceIn(0, destino.size), cartao)
                c.copy(cartoes = destino)
            }
        },
    )

/**
 * The board with a card moved to the TOP of another column.
 *
 * Top and not bottom: the card you have just moved is the one that matters
 * now, and burying it at the end of a long column makes the gesture look like
 * it did nothing. The card's status changes immediately too, so its label does
 * not go on stating the old state while the response is still in flight.
 */
internal fun QuadroDoJira.comCartaoMovido(chave: String, paraColuna: String): QuadroDoJira {
    val cartao = colunas.firstNotNullOfOrNull { c -> c.cartoes.firstOrNull { it.chave == chave } } ?: return this
    return semCartao(chave).comCartaoEm(cartao.copy(status = paraColuna), paraColuna, 0)
}

/** The column labels, in board order. */
internal fun QuadroDoJira.rotulos(): List<String> = colunas.map(ColunaDoJira::rotulo)
