package com.vpsmanager.data.jira

import com.vpsmanager.mobileapiclient.api.JiraApi
import com.vpsmanager.mobileapiclient.infrastructure.ClientException
import com.vpsmanager.mobileapiclient.infrastructure.ServerException
import com.vpsmanager.mobileapiclient.model.JiraAssignRequest
import com.vpsmanager.mobileapiclient.model.JiraBulkAssignRequest
import com.vpsmanager.mobileapiclient.model.JiraBulkMoveRequest
import com.vpsmanager.mobileapiclient.model.JiraCommentRequest
import com.vpsmanager.mobileapiclient.model.JiraConnectRequest
import com.vpsmanager.mobileapiclient.model.JiraCreateRequest
import com.vpsmanager.mobileapiclient.model.JiraMoveRequest
import com.vpsmanager.mobileapiclient.model.JiraProjectRequest
import java.io.IOException

// --- domain models ------------------------------------------------------------
//
// The `:feature-jira` module NEVER sees the generated types: it talks only to
// what is declared here. Without that, renaming a field in the OpenAPI contract
// would recompile the whole screen, and the screen's test would have to build
// generated models just to exercise a card.

/** A Jira person, reduced to what a card shows. */
data class PessoaDoJira(
    val accountId: String,
    val nome: String,
    val avatarUrl: String? = null,
)

/** A project in the picker's list. */
data class ProjetoDoJira(val chave: String, val nome: String)

/** A quick filter, with the label the server sent. */
data class FiltroDoJira(val chave: String, val rotulo: String)

/** An issue as it appears on a board card. */
data class CartaoDoJira(
    val chave: String,
    val resumo: String,
    val status: String,
    val categoria: String,
    val tipo: String? = null,
    val prioridade: String? = null,
    val responsavel: String? = null,
    val responsavelId: String? = null,
    val avatarUrl: String? = null,
    val rotulos: List<String> = emptyList(),
    val atualizada: String? = null,
    val vence: String? = null,
)

/**
 * A column of the board.
 *
 * [rotulo] is the column's identity throughout the interface: it is what goes
 * back to the server on a move call. The app never invents a column name and
 * never translates a status — the server is what knows what a column is.
 */
data class ColunaDoJira(
    val rotulo: String,
    val sobra: Boolean = false,
    val cartoes: List<CartaoDoJira> = emptyList(),
)

/** The whole board, as one call returns it. */
data class QuadroDoJira(
    val conectado: Boolean,
    val site: String? = null,
    val projeto: String? = null,
    val projetos: List<ProjetoDoJira> = emptyList(),
    val eu: PessoaDoJira? = null,
    val filtro: String = "all",
    val filtros: List<FiltroDoJira> = emptyList(),
    val jql: String? = null,
    val colunas: List<ColunaDoJira> = emptyList(),
    val total: Int = 0,
    /** A refusal from Jira (malformed JQL, expired token) WITHOUT the screen having been wiped. */
    val recusa: String? = null,
)

/** A comment already flattened for reading. */
data class ComentarioDoJira(
    val id: String,
    val texto: String,
    val autor: String,
    val quando: String,
)

/**
 * A possible destination, in the vocabulary of the board's COLUMNS.
 *
 * It is what feeds the card menu's "move to…" — the accessible way to move,
 * because a screen reader does not drag.
 */
data class DestinoDoJira(val coluna: String, val status: String, val transicao: String? = null)

/** The other end of a link ("blocks", "is blocked by"). */
data class VinculoDoJira(
    val relacao: String,
    val chave: String,
    val resumo: String? = null,
    val status: String? = null,
)

/** An open issue: fields, comments and where it can go. */
data class IssueDoJira(
    val chave: String,
    val resumo: String,
    val descricao: String? = null,
    val status: String,
    val categoria: String,
    val coluna: String? = null,
    val tipo: String? = null,
    val prioridade: String? = null,
    val responsavel: PessoaDoJira? = null,
    val relator: PessoaDoJira? = null,
    val rotulos: List<String> = emptyList(),
    val criada: String? = null,
    val atualizada: String? = null,
    val vence: String? = null,
    val urlWeb: String? = null,
    val comentarios: List<ComentarioDoJira> = emptyList(),
    val destinos: List<DestinoDoJira> = emptyList(),
    val subtarefas: List<CartaoDoJira> = emptyList(),
    val vinculos: List<VinculoDoJira> = emptyList(),
)

/** What a creation form offers instead of asking you to type. */
data class MetaDoJira(val tipos: List<String>, val prioridades: List<String>)

/** An issue that did not make it, with the reason. */
data class FalhaEmLote(val chave: String, val motivo: String)

/** The honest result of a bulk action. */
data class ResultadoEmLote(val feitas: List<String>, val falhas: List<FalhaEmLote>)

/** What creating an issue needs. */
data class NovaIssue(
    val projeto: String,
    val tipo: String,
    val resumo: String,
    val descricao: String? = null,
    val prioridade: String? = null,
    val responsavelId: String? = null,
    val rotulos: List<String> = emptyList(),
    val vence: String? = null,
)

/**
 * The outcome of an operation.
 *
 * [Recusa] is kept apart from [Erro] on purpose, and the distinction is the
 * most important thing in this file: a refusal is Jira saying "that move does
 * not exist in this workflow" — the server is fine, the network is fine, and
 * trying again will give exactly the same result. An error is anything else,
 * and that one does call for another attempt. Conflating the two would have the
 * board offer "try again" for a move that will never be accepted.
 */
sealed interface ResultadoDoJira<out T> {
    data class Ok<T>(val valor: T) : ResultadoDoJira<T>
    data class Recusa(val motivo: String) : ResultadoDoJira<Nothing>
    data class Erro(val motivo: String) : ResultadoDoJira<Nothing>
}

/**
 * The narrow slice of the repository the board screen depends on.
 *
 * An interface rather than the concrete class for the same reason as
 * [com.vpsmanager.data.terminal.TerminalSessionsSource]: `:feature-jira` has no
 * visibility of the generated client, so its tests fake this instead of faking
 * OkHttp.
 */
interface FonteDoJira {
    suspend fun quadro(
        projeto: String? = null,
        filtro: String = "all",
        jql: String? = null,
        busca: String? = null,
        ordem: String? = null,
        ocultarConcluidasApos: Int = 0,
    ): ResultadoDoJira<QuadroDoJira>

    suspend fun mover(chave: String, coluna: String): ResultadoDoJira<String>
    suspend fun issue(chave: String): ResultadoDoJira<IssueDoJira>
    suspend fun comentar(chave: String, texto: String): ResultadoDoJira<ComentarioDoJira>
    suspend fun atribuir(chave: String, accountId: String?): ResultadoDoJira<Unit>
    suspend fun pessoas(projeto: String, busca: String? = null): ResultadoDoJira<List<PessoaDoJira>>
    suspend fun meta(projeto: String): ResultadoDoJira<MetaDoJira>
    suspend fun criar(nova: NovaIssue): ResultadoDoJira<String>
    suspend fun moverEmLote(chaves: List<String>, coluna: String): ResultadoDoJira<ResultadoEmLote>
    suspend fun atribuirEmLote(chaves: List<String>, accountId: String?): ResultadoDoJira<ResultadoEmLote>
    suspend fun conectar(site: String, email: String, token: String, projeto: String?): ResultadoDoJira<Unit>
    suspend fun fixarProjeto(projeto: String): ResultadoDoJira<Unit>
}

/**
 * The single entry point into the generated client for Jira.
 *
 * Every translation from a generated type to a domain type happens here — no
 * other module imports `com.vpsmanager.mobileapiclient`.
 */
class JiraRepository(
    private val api: JiraApi = JiraApi(),
) : FonteDoJira {

    override suspend fun quadro(
        projeto: String?,
        filtro: String,
        jql: String?,
        busca: String?,
        ordem: String?,
        ocultarConcluidasApos: Int,
    ): ResultadoDoJira<QuadroDoJira> = protegido("Could not load the board.") {
        val r = api.getJiraBoard(
            project = projeto?.takeIf { it.isNotBlank() },
            filter = filtro,
            jql = jql?.takeIf { it.isNotBlank() },
            search = busca?.takeIf { it.isNotBlank() },
            sort = ordem?.takeIf { it.isNotBlank() },
            hideDoneDays = ocultarConcluidasApos.toLong(),
        )
        QuadroDoJira(
            conectado = r.connected,
            site = r.site,
            projeto = r.project,
            projetos = r.projects.orEmpty().map { ProjetoDoJira(it.key, it.name) },
            eu = r.me?.let { PessoaDoJira(it.accountId, it.displayName, it.avatarUrl) },
            filtro = r.filter,
            filtros = r.filters.orEmpty().map { FiltroDoJira(it.key, it.label) },
            jql = r.jql,
            colunas = r.columns.orEmpty().map { c ->
                ColunaDoJira(
                    rotulo = c.label,
                    sobra = c.fallback ?: false,
                    cartoes = c.cards.orEmpty().map(::paraCartao),
                )
            },
            total = r.total.toInt(),
            recusa = r.error?.takeIf { it.isNotBlank() },
        )
    }

    override suspend fun mover(chave: String, coluna: String): ResultadoDoJira<String> =
        protegido("Could not move $chave.") {
            api.moveJiraIssue(JiraMoveRequest(key = chave, column = coluna)).status
        }

    override suspend fun issue(chave: String): ResultadoDoJira<IssueDoJira> =
        protegido("Could not open $chave.") {
            val r = api.getJiraIssue(chave)
            IssueDoJira(
                chave = r.key,
                resumo = r.summary,
                descricao = r.description?.takeIf { it.isNotBlank() },
                status = r.status,
                categoria = r.category,
                coluna = r.column,
                tipo = r.type,
                prioridade = r.priority,
                responsavel = r.assignee?.let { PessoaDoJira(it.accountId, it.displayName, it.avatarUrl) },
                relator = r.reporter?.let { PessoaDoJira(it.accountId, it.displayName, it.avatarUrl) },
                rotulos = r.labels.orEmpty(),
                criada = r.created,
                atualizada = r.updated,
                vence = r.dueDate,
                urlWeb = r.webUrl,
                comentarios = r.comments.orEmpty().map {
                    ComentarioDoJira(it.id, it.body, it.author, it.created)
                },
                destinos = r.moves.orEmpty().map { DestinoDoJira(it.column, it.status, it.name) },
                subtarefas = r.subtasks.orEmpty().map(::paraCartao),
                vinculos = r.links.orEmpty().map {
                    VinculoDoJira(it.relation, it.key, it.summary, it.status)
                },
            )
        }

    override suspend fun comentar(chave: String, texto: String): ResultadoDoJira<ComentarioDoJira> =
        protegido("Could not post the comment.") {
            val c = api.commentJiraIssue(JiraCommentRequest(key = chave, text = texto))
            ComentarioDoJira(c.id, c.body, c.author, c.created)
        }

    override suspend fun atribuir(chave: String, accountId: String?): ResultadoDoJira<Unit> =
        protegido("Could not change the assignee.") {
            api.assignJiraIssue(JiraAssignRequest(key = chave, accountId = accountId.orEmpty()))
            Unit
        }

    override suspend fun pessoas(projeto: String, busca: String?): ResultadoDoJira<List<PessoaDoJira>> =
        protegido("Could not load the project's people.") {
            api.listJiraAssignableUsers(projeto, busca?.takeIf { it.isNotBlank() })
                .users.orEmpty().map { PessoaDoJira(it.accountId, it.displayName, it.avatarUrl) }
        }

    override suspend fun meta(projeto: String): ResultadoDoJira<MetaDoJira> =
        protegido("Could not load this project's issue types.") {
            val m = api.getJiraMeta(projeto)
            MetaDoJira(tipos = m.issueTypes.orEmpty(), prioridades = m.priorities.orEmpty())
        }

    override suspend fun criar(nova: NovaIssue): ResultadoDoJira<String> =
        protegido("Could not create the issue.") {
            api.createJiraIssue(
                JiraCreateRequest(
                    project = nova.projeto,
                    type = nova.tipo,
                    summary = nova.resumo,
                    description = nova.descricao,
                    priority = nova.prioridade,
                    assigneeId = nova.responsavelId,
                    labels = nova.rotulos.takeIf { it.isNotEmpty() },
                    dueDate = nova.vence,
                ),
            ).key
        }

    override suspend fun moverEmLote(chaves: List<String>, coluna: String): ResultadoDoJira<ResultadoEmLote> =
        protegido("Could not move the selection.") {
            paraLote(api.bulkMoveJiraIssues(JiraBulkMoveRequest(issueKeys = chaves, column = coluna)))
        }

    override suspend fun atribuirEmLote(chaves: List<String>, accountId: String?): ResultadoDoJira<ResultadoEmLote> =
        protegido("Could not assign the selection.") {
            paraLote(api.bulkAssignJiraIssues(JiraBulkAssignRequest(issueKeys = chaves, accountId = accountId.orEmpty())))
        }

    override suspend fun conectar(
        site: String,
        email: String,
        token: String,
        projeto: String?,
    ): ResultadoDoJira<Unit> = protegido("Could not connect to Jira.") {
        api.connectJira(JiraConnectRequest(site = site, email = email, token = token, project = projeto))
        Unit
    }

    override suspend fun fixarProjeto(projeto: String): ResultadoDoJira<Unit> =
        protegido("Could not switch projects.") {
            api.setJiraProject(JiraProjectRequest(project = projeto))
            Unit
        }

    private fun paraCartao(c: com.vpsmanager.mobileapiclient.model.JiraBoardCard) = CartaoDoJira(
        chave = c.key,
        resumo = c.summary,
        status = c.status,
        categoria = c.category,
        tipo = c.type,
        prioridade = c.priority,
        responsavel = c.assignee,
        responsavelId = c.assigneeId,
        avatarUrl = c.avatarUrl,
        rotulos = c.labels.orEmpty(),
        atualizada = c.updated,
        vence = c.dueDate,
    )

    private fun paraLote(r: com.vpsmanager.mobileapiclient.model.JiraBulkResult) = ResultadoEmLote(
        feitas = r.done.orEmpty(),
        falhas = r.failed.orEmpty().map { FalhaEmLote(it.key, it.reason) },
    )

    /**
     * Runs the call and translates whatever goes wrong.
     *
     * A 409 becomes [ResultadoDoJira.Recusa] carrying the server's own text,
     * and that is why this wrapper exists: the 409's message is the only one
     * that says WHERE you can go from there. Replacing it with "could not move"
     * would throw away the information that turns a refusal into a next step.
     */
    private suspend fun <T> protegido(
        quandoFalhar: String,
        bloco: suspend () -> T,
    ): ResultadoDoJira<T> = try {
        ResultadoDoJira.Ok(bloco())
    } catch (e: ClientException) {
        val detalhe = detalheDoErro(e.message)
        when (e.statusCode) {
            409 -> ResultadoDoJira.Recusa(detalhe ?: quandoFalhar)
            // A 400 is also the server's decision about the ACTION (a column
            // that no longer exists, a missing field) — retrying changes
            // nothing.
            400 -> ResultadoDoJira.Recusa(detalhe ?: quandoFalhar)
            401, 403 -> ResultadoDoJira.Erro("Session expired. Sign in again.")
            else -> ResultadoDoJira.Erro(detalhe ?: quandoFalhar)
        }
    } catch (e: ServerException) {
        ResultadoDoJira.Erro("The server is unavailable right now.")
    } catch (e: IOException) {
        ResultadoDoJira.Erro("Connection failed. Check your network and try again.")
    } catch (e: Exception) {
        ResultadoDoJira.Erro(quandoFalhar)
    }
}

/**
 * Extracts the `detail` from the error body the BFF returns (RFC 7807, via
 * huma).
 *
 * Done by hand, without deserializing: the generated client's exception message
 * is free text with the body embedded ("Client error : 409 {...}"), not a whole
 * valid JSON document — `Json.decodeFromString` would fail on the first word.
 * And a failure HERE would erase precisely the sentence that explains the
 * refusal.
 */
internal fun detalheDoErro(mensagem: String?): String? {
    if (mensagem.isNullOrBlank()) return null
    val marca = "\"detail\":"
    val i = mensagem.indexOf(marca)
    if (i < 0) return null
    var j = i + marca.length
    while (j < mensagem.length && mensagem[j].isWhitespace()) j++
    if (j >= mensagem.length || mensagem[j] != '"') return null
    j++
    val sb = StringBuilder()
    while (j < mensagem.length) {
        val c = mensagem[j]
        when {
            c == '\\' && j + 1 < mensagem.length -> {
                // JSON escape sequences. `\uXXXX` is left out: the BFF does
                // not emit them (Go serializes an accent as literal UTF-8), and
                // decoding them here would mean writing half a parser for a
                // case that does not occur.
                when (val prox = mensagem[j + 1]) {
                    'n' -> sb.append('\n')
                    't' -> sb.append('\t')
                    'r' -> sb.append('\r')
                    else -> sb.append(prox)
                }
                j += 2
            }
            c == '"' -> return sb.toString().ifBlank { null }
            else -> {
                sb.append(c)
                j++
            }
        }
    }
    return null
}
