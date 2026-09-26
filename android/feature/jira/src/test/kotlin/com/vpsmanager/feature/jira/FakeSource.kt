package com.vpsmanager.feature.jira

import com.vpsmanager.data.jira.JiraCard
import com.vpsmanager.data.jira.JiraColumn
import com.vpsmanager.data.jira.JiraComment
import com.vpsmanager.data.jira.JiraFilter
import com.vpsmanager.data.jira.JiraSource
import com.vpsmanager.data.jira.JiraIssue
import com.vpsmanager.data.jira.JiraMeta
import com.vpsmanager.data.jira.NewIssue
import com.vpsmanager.data.jira.JiraPerson
import com.vpsmanager.data.jira.JiraBoard
import com.vpsmanager.data.jira.JiraResult
import com.vpsmanager.data.jira.BulkResult

/** A test card, with the bare minimum filled in. */
internal fun card(key: String, status: String = "Backlog", category: String = "new") =
    JiraCard(key = key, summary = "resumo de $key", status = status, category = category)

/** A three-column board, holding whatever cards it is given. */
internal fun testBoard(
    toDo: List<JiraCard> = emptyList(),
    inProgress: List<JiraCard> = emptyList(),
    done: List<JiraCard> = emptyList(),
    me: JiraPerson? = JiraPerson("acc-eu", "Sam Rivera"),
    rejection: String? = null,
) = JiraBoard(
    connected = true,
    project = "VPSM",
    me = me,
    filter = "all",
    filters = listOf(JiraFilter("all", "Todas"), JiraFilter("mine", "Minhas")),
    columns = listOf(
        JiraColumn("A fazer", cards = toDo),
        JiraColumn("Em andamento", cards = inProgress),
        JiraColumn("Concluído", cards = done),
    ),
    total = toDo.size + inProgress.size + done.size,
    rejection = rejection,
)

/**
 * A double for the source.
 *
 * It records what was ASKED, and not only what came back: half of these tests
 * are about a call that must NOT happen (moving to the column the card is
 * already in) or about what the client sent (the column by its label, never a
 * transition id).
 */
internal class FakeSource(
    private var board: JiraResult<JiraBoard> = JiraResult.Ok(testBoard()),
    private var onMove: (String, String) -> JiraResult<String> = { _, _ -> JiraResult.Ok("Pronto") },
) : JiraSource {

    val moves = mutableListOf<Pair<String, String>>()
    val bulkMoves = mutableListOf<Pair<List<String>, String>>()
    val assignments = mutableListOf<Pair<String, String?>>()
    val created = mutableListOf<NewIssue>()
    val pinnedProjects = mutableListOf<String>()
    var boardsRequested = 0
        private set

    fun returnBoard(next: JiraResult<JiraBoard>) {
        board = next
    }

    override suspend fun board(
        project: String?,
        filter: String,
        jql: String?,
        query: String?,
        order: String?,
        hideDoneAfter: Int,
    ): JiraResult<JiraBoard> {
        boardsRequested++
        return board
    }

    override suspend fun move(key: String, column: String): JiraResult<String> {
        moves += key to column
        return onMove(key, column)
    }

    override suspend fun issue(key: String): JiraResult<JiraIssue> = JiraResult.Ok(
        JiraIssue(key = key, summary = "resumo de $key", status = "Backlog", category = "new"),
    )

    override suspend fun comment(key: String, text: String): JiraResult<JiraComment> =
        JiraResult.Ok(JiraComment("1", text, "Sam Rivera", "2026-09-09T12:00:00.000-0300"))

    override suspend fun assign(key: String, accountId: String?): JiraResult<Unit> {
        assignments += key to accountId
        return JiraResult.Ok(Unit)
    }

    override suspend fun people(project: String, query: String?): JiraResult<List<JiraPerson>> =
        JiraResult.Ok(listOf(JiraPerson("acc-eu", "Sam Rivera")))

    override suspend fun meta(project: String): JiraResult<JiraMeta> =
        JiraResult.Ok(JiraMeta(listOf("Task", "Bug"), listOf("Alta", "Média")))

    override suspend fun create(newIssue: NewIssue): JiraResult<String> {
        created += newIssue
        return JiraResult.Ok("TASK-99")
    }

    override suspend fun bulkMove(keys: List<String>, column: String): JiraResult<BulkResult> {
        bulkMoves += keys to column
        return JiraResult.Ok(BulkResult(keys, emptyList()))
    }

    override suspend fun bulkAssign(keys: List<String>, accountId: String?): JiraResult<BulkResult> =
        JiraResult.Ok(BulkResult(keys, emptyList()))

    override suspend fun connect(
        site: String,
        email: String,
        token: String,
        project: String?,
    ): JiraResult<Unit> = JiraResult.Ok(Unit)

    override suspend fun pinProject(project: String): JiraResult<Unit> {
        pinnedProjects += project
        return JiraResult.Ok(Unit)
    }
}
