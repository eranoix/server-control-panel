package com.vpsmanager.feature.jira

import com.vpsmanager.data.jira.CartaoDoJira
import com.vpsmanager.data.jira.ColunaDoJira
import com.vpsmanager.data.jira.ComentarioDoJira
import com.vpsmanager.data.jira.FiltroDoJira
import com.vpsmanager.data.jira.FonteDoJira
import com.vpsmanager.data.jira.IssueDoJira
import com.vpsmanager.data.jira.MetaDoJira
import com.vpsmanager.data.jira.NovaIssue
import com.vpsmanager.data.jira.PessoaDoJira
import com.vpsmanager.data.jira.QuadroDoJira
import com.vpsmanager.data.jira.ResultadoDoJira
import com.vpsmanager.data.jira.ResultadoEmLote

/** A test card, with the bare minimum filled in. */
internal fun cartao(chave: String, status: String = "Backlog", categoria: String = "new") =
    CartaoDoJira(chave = chave, resumo = "resumo de $chave", status = status, categoria = categoria)

/** A three-column board, holding whatever cards it is given. */
internal fun quadroDeTeste(
    aFazer: List<CartaoDoJira> = emptyList(),
    emAndamento: List<CartaoDoJira> = emptyList(),
    concluido: List<CartaoDoJira> = emptyList(),
    eu: PessoaDoJira? = PessoaDoJira("acc-eu", "Sam Rivera"),
    recusa: String? = null,
) = QuadroDoJira(
    conectado = true,
    projeto = "VPSM",
    eu = eu,
    filtro = "all",
    filtros = listOf(FiltroDoJira("all", "Todas"), FiltroDoJira("mine", "Minhas")),
    colunas = listOf(
        ColunaDoJira("A fazer", cartoes = aFazer),
        ColunaDoJira("Em andamento", cartoes = emAndamento),
        ColunaDoJira("Concluído", cartoes = concluido),
    ),
    total = aFazer.size + emAndamento.size + concluido.size,
    recusa = recusa,
)

/**
 * A double for the source.
 *
 * It records what was ASKED, and not only what came back: half of these tests
 * are about a call that must NOT happen (moving to the column the card is
 * already in) or about what the client sent (the column by its label, never a
 * transition id).
 */
internal class FonteFalsa(
    private var quadro: ResultadoDoJira<QuadroDoJira> = ResultadoDoJira.Ok(quadroDeTeste()),
    private var aoMover: (String, String) -> ResultadoDoJira<String> = { _, _ -> ResultadoDoJira.Ok("Pronto") },
) : FonteDoJira {

    val movimentos = mutableListOf<Pair<String, String>>()
    val lotesMovidos = mutableListOf<Pair<List<String>, String>>()
    val atribuicoes = mutableListOf<Pair<String, String?>>()
    val criadas = mutableListOf<NovaIssue>()
    val projetosFixados = mutableListOf<String>()
    var quadrosPedidos = 0
        private set

    fun devolverQuadro(novo: ResultadoDoJira<QuadroDoJira>) {
        quadro = novo
    }

    override suspend fun quadro(
        projeto: String?,
        filtro: String,
        jql: String?,
        busca: String?,
        ordem: String?,
        ocultarConcluidasApos: Int,
    ): ResultadoDoJira<QuadroDoJira> {
        quadrosPedidos++
        return quadro
    }

    override suspend fun mover(chave: String, coluna: String): ResultadoDoJira<String> {
        movimentos += chave to coluna
        return aoMover(chave, coluna)
    }

    override suspend fun issue(chave: String): ResultadoDoJira<IssueDoJira> = ResultadoDoJira.Ok(
        IssueDoJira(chave = chave, resumo = "resumo de $chave", status = "Backlog", categoria = "new"),
    )

    override suspend fun comentar(chave: String, texto: String): ResultadoDoJira<ComentarioDoJira> =
        ResultadoDoJira.Ok(ComentarioDoJira("1", texto, "Sam Rivera", "2026-09-09T12:00:00.000-0300"))

    override suspend fun atribuir(chave: String, accountId: String?): ResultadoDoJira<Unit> {
        atribuicoes += chave to accountId
        return ResultadoDoJira.Ok(Unit)
    }

    override suspend fun pessoas(projeto: String, busca: String?): ResultadoDoJira<List<PessoaDoJira>> =
        ResultadoDoJira.Ok(listOf(PessoaDoJira("acc-eu", "Sam Rivera")))

    override suspend fun meta(projeto: String): ResultadoDoJira<MetaDoJira> =
        ResultadoDoJira.Ok(MetaDoJira(listOf("Task", "Bug"), listOf("Alta", "Média")))

    override suspend fun criar(nova: NovaIssue): ResultadoDoJira<String> {
        criadas += nova
        return ResultadoDoJira.Ok("TASK-99")
    }

    override suspend fun moverEmLote(chaves: List<String>, coluna: String): ResultadoDoJira<ResultadoEmLote> {
        lotesMovidos += chaves to coluna
        return ResultadoDoJira.Ok(ResultadoEmLote(chaves, emptyList()))
    }

    override suspend fun atribuirEmLote(chaves: List<String>, accountId: String?): ResultadoDoJira<ResultadoEmLote> =
        ResultadoDoJira.Ok(ResultadoEmLote(chaves, emptyList()))

    override suspend fun conectar(
        site: String,
        email: String,
        token: String,
        projeto: String?,
    ): ResultadoDoJira<Unit> = ResultadoDoJira.Ok(Unit)

    override suspend fun fixarProjeto(projeto: String): ResultadoDoJira<Unit> {
        projetosFixados += projeto
        return ResultadoDoJira.Ok(Unit)
    }
}
