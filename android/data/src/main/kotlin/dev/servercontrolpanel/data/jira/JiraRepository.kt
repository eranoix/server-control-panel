package dev.servercontrolpanel.data.jira

import dev.servercontrolpanel.mobileapiclient.api.JiraApi
import dev.servercontrolpanel.mobileapiclient.infrastructure.ClientException
import dev.servercontrolpanel.mobileapiclient.infrastructure.ServerException
import dev.servercontrolpanel.mobileapiclient.model.JiraAssignRequest
import dev.servercontrolpanel.mobileapiclient.model.JiraBulkAssignRequest
import dev.servercontrolpanel.mobileapiclient.model.JiraBulkMoveRequest
import dev.servercontrolpanel.mobileapiclient.model.JiraCommentRequest
import dev.servercontrolpanel.mobileapiclient.model.JiraConnectRequest
import dev.servercontrolpanel.mobileapiclient.model.JiraCreateRequest
import dev.servercontrolpanel.mobileapiclient.model.JiraMoveRequest
import dev.servercontrolpanel.mobileapiclient.model.JiraProjectRequest
import java.io.IOException

// --- domain models ------------------------------------------------------------
//
// The `:feature-jira` module NEVER sees the generated types: it talks only to
// what is declared here. Without that, renaming a field in the OpenAPI contract
// would recompile the whole screen, and the screen's test would have to build
// generated models just to exercise a card.

/** A Jira person, reduced to what a card shows. */
data class JiraPerson(
    val accountId: String,
    val name: String,
    val avatarUrl: String? = null,
)

/** A project in the picker's list. */
data class JiraProject(val key: String, val name: String)

/** A quick filter, with the label the server sent. */
data class JiraFilter(val key: String, val label: String)

/** An issue as it appears on a board card. */
data class JiraCard(
    val key: String,
    val summary: String,
    val status: String,
    val category: String,
    val type: String? = null,
    val priority: String? = null,
    val assignee: String? = null,
    val assigneeId: String? = null,
    val avatarUrl: String? = null,
    val labels: List<String> = emptyList(),
    val updated: String? = null,
    val due: String? = null,
)

/**
 * A column of the board.
 *
 * [label] is the column's identity throughout the interface: it is what goes
 * back to the server on a move call. The app never invents a column name and
 * never translates a status — the server is what knows what a column is.
 */
data class JiraColumn(
    val label: String,
    val leftover: Boolean = false,
    val cards: List<JiraCard> = emptyList(),
)

/** The whole board, as one call returns it. */
data class JiraBoard(
    val connected: Boolean,
    val site: String? = null,
    val project: String? = null,
    val projects: List<JiraProject> = emptyList(),
    val me: JiraPerson? = null,
    val filter: String = "all",
    val filters: List<JiraFilter> = emptyList(),
    val jql: String? = null,
    val columns: List<JiraColumn> = emptyList(),
    val total: Int = 0,
    /** A refusal from Jira (malformed JQL, expired token) WITHOUT the screen having been wiped. */
    val rejection: String? = null,
)

/** A comment already flattened for reading. */
data class JiraComment(
    val id: String,
    val text: String,
    val author: String,
    val whenText: String,
)

/**
 * A possible destination, in the vocabulary of the board's COLUMNS.
 *
 * It is what feeds the card menu's "move to…" — the accessible way to move,
 * because a screen reader does not drag.
 */
data class JiraDestination(val column: String, val status: String, val transition: String? = null)

/** The other end of a link ("blocks", "is blocked by"). */
data class JiraLink(
    val relation: String,
    val key: String,
    val summary: String? = null,
    val status: String? = null,
)

/** An open issue: fields, comments and where it can go. */
data class JiraIssue(
    val key: String,
    val summary: String,
    val description: String? = null,
    val status: String,
    val category: String,
    val column: String? = null,
    val type: String? = null,
    val priority: String? = null,
    val assignee: JiraPerson? = null,
    val reporter: JiraPerson? = null,
    val labels: List<String> = emptyList(),
    val created: String? = null,
    val updated: String? = null,
    val due: String? = null,
    val urlWeb: String? = null,
    val comments: List<JiraComment> = emptyList(),
    val destinations: List<JiraDestination> = emptyList(),
    val subtasks: List<JiraCard> = emptyList(),
    val links: List<JiraLink> = emptyList(),
)

/** What a creation form offers instead of asking you to type. */
data class JiraMeta(val types: List<String>, val priorities: List<String>)

/** An issue that did not make it, with the reason. */
data class BulkFailure(val key: String, val reason: String)

/** The honest result of a bulk action. */
data class BulkResult(val done: List<String>, val failures: List<BulkFailure>)

/** What creating an issue needs. */
data class NewIssue(
    val project: String,
    val type: String,
    val summary: String,
    val description: String? = null,
    val priority: String? = null,
    val assigneeId: String? = null,
    val labels: List<String> = emptyList(),
    val due: String? = null,
)

/**
 * The outcome of an operation.
 *
 * [Rejected] is kept apart from [Error] on purpose, and the distinction is the
 * most important thing in this file: a refusal is Jira saying "that move does
 * not exist in this workflow" — the server is fine, the network is fine, and
 * trying again will give exactly the same result. An error is anything else,
 * and that one does call for another attempt. Conflating the two would have the
 * board offer "try again" for a move that will never be accepted.
 */
sealed interface JiraResult<out T> {
    data class Ok<T>(val value: T) : JiraResult<T>
    data class Rejected(val reason: String) : JiraResult<Nothing>
    data class Error(val reason: String) : JiraResult<Nothing>
}

/**
 * The narrow slice of the repository the board screen depends on.
 *
 * An interface rather than the concrete class for the same reason as
 * [dev.servercontrolpanel.data.terminal.TerminalSessionsSource]: `:feature-jira` has no
 * visibility of the generated client, so its tests fake this instead of faking
 * OkHttp.
 */
interface JiraSource {
    suspend fun board(
        project: String? = null,
        filter: String = "all",
        jql: String? = null,
        query: String? = null,
        order: String? = null,
        hideDoneAfter: Int = 0,
    ): JiraResult<JiraBoard>

    suspend fun move(key: String, column: String): JiraResult<String>
    suspend fun issue(key: String): JiraResult<JiraIssue>
    suspend fun comment(key: String, text: String): JiraResult<JiraComment>
    suspend fun assign(key: String, accountId: String?): JiraResult<Unit>
    suspend fun people(project: String, query: String? = null): JiraResult<List<JiraPerson>>
    suspend fun meta(project: String): JiraResult<JiraMeta>
    suspend fun create(next: NewIssue): JiraResult<String>
    suspend fun bulkMove(keys: List<String>, column: String): JiraResult<BulkResult>
    suspend fun bulkAssign(keys: List<String>, accountId: String?): JiraResult<BulkResult>
    suspend fun connect(site: String, email: String, token: String, project: String?): JiraResult<Unit>
    suspend fun pinProject(project: String): JiraResult<Unit>
}

/**
 * The single entry point into the generated client for Jira.
 *
 * Every translation from a generated type to a domain type happens here — no
 * other module imports `dev.servercontrolpanel.mobileapiclient`.
 */
class JiraRepository(
    private val api: JiraApi = JiraApi(),
) : JiraSource {

    override suspend fun board(
        project: String?,
        filter: String,
        jql: String?,
        query: String?,
        order: String?,
        hideDoneAfter: Int,
    ): JiraResult<JiraBoard> = guarded("Could not load the board.") {
        val r = api.getJiraBoard(
            project = project?.takeIf { it.isNotBlank() },
            filter = filter,
            jql = jql?.takeIf { it.isNotBlank() },
            search = query?.takeIf { it.isNotBlank() },
            sort = order?.takeIf { it.isNotBlank() },
            hideDoneDays = hideDoneAfter.toLong(),
        )
        JiraBoard(
            connected = r.connected,
            site = r.site,
            project = r.project,
            projects = r.projects.orEmpty().map { JiraProject(it.key, it.name) },
            me = r.me?.let { JiraPerson(it.accountId, it.displayName, it.avatarUrl) },
            filter = r.filter,
            filters = r.filters.orEmpty().map { JiraFilter(it.key, it.label) },
            jql = r.jql,
            columns = r.columns.orEmpty().map { c ->
                JiraColumn(
                    label = c.label,
                    leftover = c.fallback ?: false,
                    cards = c.cards.orEmpty().map(::toCard),
                )
            },
            total = r.total.toInt(),
            rejection = r.error?.takeIf { it.isNotBlank() },
        )
    }

    override suspend fun move(key: String, column: String): JiraResult<String> =
        guarded("Could not move $key.") {
            api.moveJiraIssue(JiraMoveRequest(key = key, column = column)).status
        }

    override suspend fun issue(key: String): JiraResult<JiraIssue> =
        guarded("Could not open $key.") {
            val r = api.getJiraIssue(key)
            JiraIssue(
                key = r.key,
                summary = r.summary,
                description = r.description?.takeIf { it.isNotBlank() },
                status = r.status,
                category = r.category,
                column = r.column,
                type = r.type,
                priority = r.priority,
                assignee = r.assignee?.let { JiraPerson(it.accountId, it.displayName, it.avatarUrl) },
                reporter = r.reporter?.let { JiraPerson(it.accountId, it.displayName, it.avatarUrl) },
                labels = r.labels.orEmpty(),
                created = r.created,
                updated = r.updated,
                due = r.dueDate,
                urlWeb = r.webUrl,
                comments = r.comments.orEmpty().map {
                    JiraComment(it.id, it.body, it.author, it.created)
                },
                destinations = r.moves.orEmpty().map { JiraDestination(it.column, it.status, it.name) },
                subtasks = r.subtasks.orEmpty().map(::toCard),
                links = r.links.orEmpty().map {
                    JiraLink(it.relation, it.key, it.summary, it.status)
                },
            )
        }

    override suspend fun comment(key: String, text: String): JiraResult<JiraComment> =
        guarded("Could not post the comment.") {
            val c = api.commentJiraIssue(JiraCommentRequest(key = key, text = text))
            JiraComment(c.id, c.body, c.author, c.created)
        }

    override suspend fun assign(key: String, accountId: String?): JiraResult<Unit> =
        guarded("Could not change the assignee.") {
            api.assignJiraIssue(JiraAssignRequest(key = key, accountId = accountId.orEmpty()))
            Unit
        }

    override suspend fun people(project: String, query: String?): JiraResult<List<JiraPerson>> =
        guarded("Could not load the project's people.") {
            api.listJiraAssignableUsers(project, query?.takeIf { it.isNotBlank() })
                .users.orEmpty().map { JiraPerson(it.accountId, it.displayName, it.avatarUrl) }
        }

    override suspend fun meta(project: String): JiraResult<JiraMeta> =
        guarded("Could not load this project's issue types.") {
            val m = api.getJiraMeta(project)
            JiraMeta(types = m.issueTypes.orEmpty(), priorities = m.priorities.orEmpty())
        }

    override suspend fun create(next: NewIssue): JiraResult<String> =
        guarded("Could not create the issue.") {
            api.createJiraIssue(
                JiraCreateRequest(
                    project = next.project,
                    type = next.type,
                    summary = next.summary,
                    description = next.description,
                    priority = next.priority,
                    assigneeId = next.assigneeId,
                    labels = next.labels.takeIf { it.isNotEmpty() },
                    dueDate = next.due,
                ),
            ).key
        }

    override suspend fun bulkMove(keys: List<String>, column: String): JiraResult<BulkResult> =
        guarded("Could not move the selection.") {
            toBulk(api.bulkMoveJiraIssues(JiraBulkMoveRequest(issueKeys = keys, column = column)))
        }

    override suspend fun bulkAssign(keys: List<String>, accountId: String?): JiraResult<BulkResult> =
        guarded("Could not assign the selection.") {
            toBulk(api.bulkAssignJiraIssues(JiraBulkAssignRequest(issueKeys = keys, accountId = accountId.orEmpty())))
        }

    override suspend fun connect(
        site: String,
        email: String,
        token: String,
        project: String?,
    ): JiraResult<Unit> = guarded("Could not connect to Jira.") {
        api.connectJira(JiraConnectRequest(site = site, email = email, token = token, project = project))
        Unit
    }

    override suspend fun pinProject(project: String): JiraResult<Unit> =
        guarded("Could not switch projects.") {
            api.setJiraProject(JiraProjectRequest(project = project))
            Unit
        }

    private fun toCard(c: dev.servercontrolpanel.mobileapiclient.model.JiraBoardCard) = JiraCard(
        key = c.key,
        summary = c.summary,
        status = c.status,
        category = c.category,
        type = c.type,
        priority = c.priority,
        assignee = c.assignee,
        assigneeId = c.assigneeId,
        avatarUrl = c.avatarUrl,
        labels = c.labels.orEmpty(),
        updated = c.updated,
        due = c.dueDate,
    )

    private fun toBulk(r: dev.servercontrolpanel.mobileapiclient.model.JiraBulkResult) = BulkResult(
        done = r.done.orEmpty(),
        failures = r.failed.orEmpty().map { BulkFailure(it.key, it.reason) },
    )

    /**
     * Runs the call and translates whatever goes wrong.
     *
     * A 409 becomes [JiraResult.Rejected] carrying the server's own text,
     * and that is why this wrapper exists: the 409's message is the only one
     * that says WHERE you can go from there. Replacing it with "could not move"
     * would throw away the information that turns a refusal into a next step.
     */
    private suspend fun <T> guarded(
        onFailureText: String,
        tile: suspend () -> T,
    ): JiraResult<T> = try {
        JiraResult.Ok(tile())
    } catch (e: ClientException) {
        val detail = errorDetail(e.message)
        when (e.statusCode) {
            409 -> JiraResult.Rejected(detail ?: onFailureText)
            // A 400 is also the server's decision about the ACTION (a column
            // that no longer exists, a missing field) — retrying changes
            // nothing.
            400 -> JiraResult.Rejected(detail ?: onFailureText)
            401, 403 -> JiraResult.Error("Session expired. Sign in again.")
            else -> JiraResult.Error(detail ?: onFailureText)
        }
    } catch (e: ServerException) {
        JiraResult.Error("The server is unavailable right now.")
    } catch (e: IOException) {
        JiraResult.Error("Connection failed. Check your network and try again.")
    } catch (e: Exception) {
        JiraResult.Error(onFailureText)
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
internal fun errorDetail(message: String?): String? {
    if (message.isNullOrBlank()) return null
    val mark = "\"detail\":"
    val i = message.indexOf(mark)
    if (i < 0) return null
    var j = i + mark.length
    while (j < message.length && message[j].isWhitespace()) j++
    if (j >= message.length || message[j] != '"') return null
    j++
    val sb = StringBuilder()
    while (j < message.length) {
        val c = message[j]
        when {
            c == '\\' && j + 1 < message.length -> {
                // JSON escape sequences. `\uXXXX` is left out: the BFF does
                // not emit them (Go serializes an accent as literal UTF-8), and
                // decoding them here would mean writing half a parser for a
                // case that does not occur.
                when (val nextChar = message[j + 1]) {
                    'n' -> sb.append('\n')
                    't' -> sb.append('\t')
                    'r' -> sb.append('\r')
                    else -> sb.append(nextChar)
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
