package dev.servercontrolpanel.feature.jira

import dev.servercontrolpanel.data.jira.JiraCard
import dev.servercontrolpanel.data.jira.JiraColumn
import dev.servercontrolpanel.data.jira.JiraComment
import dev.servercontrolpanel.data.jira.JiraFilter
import dev.servercontrolpanel.data.jira.JiraSource
import dev.servercontrolpanel.data.jira.JiraIssue
import dev.servercontrolpanel.data.jira.JiraMeta
import dev.servercontrolpanel.data.jira.NewIssue
import dev.servercontrolpanel.data.jira.JiraPerson
import dev.servercontrolpanel.data.jira.JiraBoard
import dev.servercontrolpanel.data.jira.JiraResult
import dev.servercontrolpanel.data.jira.BulkResult

internal fun card(key: String, status: String = "Backlog", category: String = "new") =
    JiraCard(key = key, summary = "summary of $key", status = status, category = category)

internal fun testBoard(
    toDo: List<JiraCard> = emptyList(),
    inProgress: List<JiraCard> = emptyList(),
    done: List<JiraCard> = emptyList(),
    me: JiraPerson? = JiraPerson("acc-eu", "Sam Rivera"),
    rejection: String? = null,
) = JiraBoard(
    connected = true,
    project = "PANEL",
    me = me,
    filter = "all",
    filters = listOf(JiraFilter("all", "All"), JiraFilter("mine", "Mine")),
    columns = listOf(
        JiraColumn("To Do", cards = toDo),
        JiraColumn("In Progress", cards = inProgress),
        JiraColumn("Done", cards = done),
    ),
    total = toDo.size + inProgress.size + done.size,
    rejection = rejection,
)

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
        JiraIssue(key = key, summary = "summary of $key", status = "Backlog", category = "new"),
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
        JiraResult.Ok(JiraMeta(listOf("Task", "Bug"), listOf("High", "Medium")))

    override suspend fun create(newIssue: NewIssue): JiraResult<String> {
        created += newIssue
        return JiraResult.Ok("KAN-99")
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
