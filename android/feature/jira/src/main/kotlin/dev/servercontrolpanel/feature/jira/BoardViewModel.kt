package dev.servercontrolpanel.feature.jira

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.servercontrolpanel.data.jira.JiraCard
import dev.servercontrolpanel.data.jira.JiraColumn
import dev.servercontrolpanel.data.jira.JiraSource
import dev.servercontrolpanel.data.jira.JiraIssue
import dev.servercontrolpanel.data.jira.JiraRepository
import dev.servercontrolpanel.data.jira.JiraMeta
import dev.servercontrolpanel.data.jira.NewIssue
import dev.servercontrolpanel.data.jira.JiraPerson
import dev.servercontrolpanel.data.jira.JiraBoard
import dev.servercontrolpanel.data.jira.JiraResult
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** The state of the board screen. */
sealed interface BoardState {
    data object Loading : BoardState

    /** No Jira account is connected yet — the way forward is the form, not an error. */
    data object Disconnected : BoardState

    data class Ready(val board: JiraBoard) : BoardState
    data class Error(val message: String) : BoardState
}

/** The state of an open issue's sheet. */
sealed interface IssueState {
    data object Closed : IssueState
    data class Loading(val key: String) : IssueState
    data class Ready(val issue: JiraIssue) : IssueState
    data class Error(val key: String, val message: String) : IssueState
}

/** The state of the creation form. */
data class CreationState(
    val isOpen: Boolean = false,
    val loadingMeta: Boolean = false,
    val meta: JiraMeta = JiraMeta(emptyList(), emptyList()),
    val people: List<JiraPerson> = emptyList(),
    val sending: Boolean = false,
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
class BoardViewModel(
    private val source: JiraSource = JiraRepository(),
) : ViewModel() {

    private val _state = MutableStateFlow<BoardState>(BoardState.Loading)
    val state: StateFlow<BoardState> = _state.asStateFlow()

    private val _issue = MutableStateFlow<IssueState>(IssueState.Closed)
    val issue: StateFlow<IssueState> = _issue.asStateFlow()

    private val _creation = MutableStateFlow(CreationState())
    val creation: StateFlow<CreationState> = _creation.asStateFlow()

    /**
     * The last sentence of feedback. The screen consumes it and clears it.
     *
     * It exists because every action here happens FAR from its effect: whoever
     * moves a card is looking at the destination column, and what changes is
     * the issue on the server. Without a sentence, success and silence look
     * identical.
     */
    private val _notice = MutableStateFlow<String?>(null)
    val notice: StateFlow<String?> = _notice.asStateFlow()

    private val _selection = MutableStateFlow<Set<String>>(emptySet())
    val selection: StateFlow<Set<String>> = _selection.asStateFlow()

    private val _selecting = MutableStateFlow(false)
    val selecting: StateFlow<Boolean> = _selecting.asStateFlow()

    private val _busy = MutableStateFlow(false)
    val busy: StateFlow<Boolean> = _busy.asStateFlow()

    // The board's current filter. Kept here rather than on the screen so that
    // a reload (pull to refresh, coming back from an action) repeats exactly
    // the same filter instead of falling back to the default.
    private var project: String? = null
    private var filter: String = "all"
    private var customJql: String = ""
    private var query: String = ""
    private var order: String = ""
    private var hideDoneAfter: Int = 0

    /** The current filter, so the screen can draw the controls as ticked. */
    val currentSlice: Slice
        get() = Slice(project, filter, customJql, query, order, hideDoneAfter)

    data class Slice(
        val project: String?,
        val filter: String,
        val jql: String,
        val query: String,
        val order: String,
        val hideDoneAfter: Int,
    )

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            // Reloading does NOT go back to "Loading" when there is already
            // a board on screen: replacing the board with a spinner on every
            // filter change makes the screen flash white and loses the scroll
            // position.
            if (_state.value !is BoardState.Ready) {
                _state.value = BoardState.Loading
            }
            when (
                val r = source.board(
                    project = project,
                    filter = filter,
                    jql = customJql,
                    query = query,
                    order = order,
                    hideDoneAfter = hideDoneAfter,
                )
            ) {
                is JiraResult.Ok -> {
                    val q = r.value
                    _state.value = if (!q.connected) BoardState.Disconnected else BoardState.Ready(q)
                    if (q.connected && project == null) project = q.project
                    // The selection held keys that may no longer be on the
                    // board after a new filter. Keeping them would leave the
                    // counter saying "3 selected" with one on screen.
                    pruneSelection(q)
                }
                is JiraResult.Rejected -> _state.value = BoardState.Error(r.reason)
                is JiraResult.Error -> _state.value = BoardState.Error(r.reason)
            }
        }
    }

    private fun pruneSelection(q: JiraBoard) {
        val visible = q.columns.flatMap { c -> c.cards.map { it.key } }.toSet()
        _selection.update { current -> current.intersect(visible) }
        if (_selection.value.isEmpty() && _selecting.value.not()) return
    }

    fun switchProject(key: String) {
        project = key
        _selection.value = emptySet()
        load()
        // Pinning it on the server is what makes the choice survive the app's
        // next launch — and it is the SAME preference the web panel uses, so
        // changing it here changes it there.
        viewModelScope.launch { source.pinProject(key) }
    }

    fun switchFilter(key: String) {
        filter = key
        load()
    }

    fun switchJql(jql: String) {
        customJql = jql
        filter = "custom"
        load()
    }

    fun search(text: String) {
        query = text
        load()
    }

    fun sortBy(criterion: String) {
        order = criterion
        load()
    }

    fun hideDoneAfter(days: Int) {
        hideDoneAfter = days
        load()
    }

    // --- move --------------------------------------------------------------

    /**
     * Moves a card to a column, with the card changing place right away.
     *
     * [origin] and [position] are captured before anything else: they are what
     * allows the card to be put back in its EXACT place when the server
     * refuses.
     */
    fun move(key: String, toColumn: String) {
        val ready = _state.value as? BoardState.Ready ?: return
        val board = ready.board
        val origin = board.columns.firstOrNull { c -> c.cards.any { it.key == key } } ?: return
        if (origin.label == toColumn) return
        val position = origin.cards.indexOfFirst { it.key == key }
        val card = origin.cards[position]

        _state.value = BoardState.Ready(board.withCardMoved(key, toColumn))

        viewModelScope.launch {
            when (val r = source.move(key, toColumn)) {
                is JiraResult.Ok -> {
                    _notice.value = "$key → $toColumn"
                    // Reload to get the REAL status: the destination column
                    // can map to several statuses, and the card needs to show
                    // which of them the transition left it in.
                    load()
                }
                is JiraResult.Rejected -> {
                    returnCard(card, origin.label, position)
                    _notice.value = r.reason
                }
                is JiraResult.Error -> {
                    returnCard(card, origin.label, position)
                    _notice.value = r.reason
                }
            }
        }
    }

    private fun returnCard(card: JiraCard, toColumn: String, position: Int) {
        val ready = _state.value as? BoardState.Ready ?: return
        _state.value = BoardState.Ready(
            ready.board.withoutCard(card.key).withCardAt(card, toColumn, position),
        )
    }

    // --- the open issue ------------------------------------------------------

    fun openIssue(key: String) {
        _issue.value = IssueState.Loading(key)
        viewModelScope.launch {
            _issue.value = when (val r = source.issue(key)) {
                is JiraResult.Ok -> IssueState.Ready(r.value)
                is JiraResult.Rejected -> IssueState.Error(key, r.reason)
                is JiraResult.Error -> IssueState.Error(key, r.reason)
            }
        }
    }

    fun closeIssue() {
        _issue.value = IssueState.Closed
    }

    fun comment(key: String, text: String) {
        if (text.isBlank()) return
        viewModelScope.launch {
            _busy.value = true
            when (val r = source.comment(key, text)) {
                is JiraResult.Ok -> {
                    // The new comment appears in the open sheet without a
                    // second trip to the server — someone who has just written
                    // something needs to see their own text in place.
                    val current = _issue.value
                    if (current is IssueState.Ready && current.issue.key == key) {
                        _issue.value = IssueState.Ready(
                            current.issue.copy(comments = current.issue.comments + r.value),
                        )
                    }
                    _notice.value = "Comment posted on $key"
                }
                is JiraResult.Rejected -> _notice.value = r.reason
                is JiraResult.Error -> _notice.value = r.reason
            }
            _busy.value = false
        }
    }

    /** Assigns the issue to someone; a null [accountId] unassigns it. */
    fun assign(key: String, accountId: String?, name: String?) {
        viewModelScope.launch {
            _busy.value = true
            when (val r = source.assign(key, accountId)) {
                is JiraResult.Ok -> {
                    _notice.value = if (accountId == null) {
                        "$key unassigned"
                    } else {
                        "$key assigned to ${name ?: "the new assignee"}"
                    }
                    if (_issue.value is IssueState.Ready) openIssue(key)
                    load()
                }
                is JiraResult.Rejected -> _notice.value = r.reason
                is JiraResult.Error -> _notice.value = r.reason
            }
            _busy.value = false
        }
    }

    /** Assigns the issue to whoever is using the app. */
    fun assignToMe(key: String) {
        val me = (_state.value as? BoardState.Ready)?.board?.me
        if (me == null) {
            _notice.value = "The server did not say who you are in Jira."
            return
        }
        assign(key, me.accountId, me.name)
    }

    // --- selection and batches -----------------------------------------------

    fun toggleSelectionMode() {
        _selecting.update { !it }
        if (!_selecting.value) _selection.value = emptySet()
    }

    fun toggleSelection(key: String) {
        _selection.update { current -> if (key in current) current - key else current + key }
    }

    fun clearSelection() {
        _selection.value = emptySet()
    }

    fun moveSelection(toColumn: String) {
        val keys = _selection.value.toList()
        if (keys.isEmpty()) return
        viewModelScope.launch {
            _busy.value = true
            when (val r = source.bulkMove(keys, toColumn)) {
                is JiraResult.Ok -> {
                    _notice.value = bulkSummary(r.value.done.size, r.value.failures)
                    _selection.value = emptySet()
                    load()
                }
                is JiraResult.Rejected -> _notice.value = r.reason
                is JiraResult.Error -> _notice.value = r.reason
            }
            _busy.value = false
        }
    }

    fun assignSelectionToMe() {
        val keys = _selection.value.toList()
        if (keys.isEmpty()) return
        val me = (_state.value as? BoardState.Ready)?.board?.me
        if (me == null) {
            _notice.value = "The server did not say who you are in Jira."
            return
        }
        viewModelScope.launch {
            _busy.value = true
            when (val r = source.bulkAssign(keys, me.accountId)) {
                is JiraResult.Ok -> {
                    _notice.value = bulkSummary(r.value.done.size, r.value.failures)
                    _selection.value = emptySet()
                    load()
                }
                is JiraResult.Rejected -> _notice.value = r.reason
                is JiraResult.Error -> _notice.value = r.reason
            }
            _busy.value = false
        }
    }

    // --- create ---------------------------------------------------------------

    fun openCreation() {
        val proj = project ?: (_state.value as? BoardState.Ready)?.board?.project
        _creation.value = CreationState(isOpen = true, loadingMeta = proj != null)
        if (proj == null) return
        viewModelScope.launch {
            val meta = source.meta(proj)
            val people = source.people(proj)
            _creation.update {
                it.copy(
                    loadingMeta = false,
                    meta = (meta as? JiraResult.Ok)?.value ?: JiraMeta(emptyList(), emptyList()),
                    people = (people as? JiraResult.Ok)?.value.orEmpty(),
                )
            }
        }
    }

    fun closeCreation() {
        _creation.value = CreationState()
    }

    fun create(next: NewIssue) {
        viewModelScope.launch {
            _creation.update { it.copy(sending = true) }
            when (val r = source.create(next)) {
                is JiraResult.Ok -> {
                    _creation.value = CreationState()
                    _notice.value = "${r.value} created"
                    load()
                }
                is JiraResult.Rejected -> {
                    _creation.update { it.copy(sending = false) }
                    _notice.value = r.reason
                }
                is JiraResult.Error -> {
                    _creation.update { it.copy(sending = false) }
                    _notice.value = r.reason
                }
            }
        }
    }

    // --- connect --------------------------------------------------------------

    fun connect(site: String, email: String, token: String, project: String?) {
        viewModelScope.launch {
            _busy.value = true
            when (val r = source.connect(site, email, token, project)) {
                is JiraResult.Ok -> {
                    this@BoardViewModel.project = project?.takeIf { it.isNotBlank() }
                    load()
                }
                is JiraResult.Rejected -> _state.value = BoardState.Error(r.reason)
                is JiraResult.Error -> _state.value = BoardState.Error(r.reason)
            }
            _busy.value = false
        }
    }

    fun consumeNotice() {
        _notice.value = null
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
internal fun bulkSummary(done: Int, failures: List<dev.servercontrolpanel.data.jira.BulkFailure>): String {
    if (failures.isEmpty()) return "$done moved"
    if (done == 0 && failures.size == 1) return "${failures[0].key}: ${failures[0].reason}"
    val names = failures.take(3).joinToString(", ") { it.key }
    val rest = if (failures.size > 3) " and ${failures.size - 3} more" else ""
    return "$done done; failed: $names$rest — ${failures[0].reason}"
}

// --- board transformations -----------------------------------------------------
//
// Deliberately pure: they are the half of the optimistic move that has to be
// exercisable with no network, no ViewModel and no Compose.

/** The board without a given card, wherever it happens to be. */
internal fun JiraBoard.withoutCard(key: String): JiraBoard =
    copy(columns = columns.map { c -> c.copy(cards = c.cards.filterNot { it.key == key }) })

/** The board with a card inserted at an exact position in a column. */
internal fun JiraBoard.withCardAt(card: JiraCard, column: String, position: Int): JiraBoard =
    copy(
        columns = columns.map { c ->
            if (c.label != column) {
                c
            } else {
                val destination = c.cards.toMutableList()
                destination.add(position.coerceIn(0, destination.size), card)
                c.copy(cards = destination)
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
internal fun JiraBoard.withCardMoved(key: String, toColumn: String): JiraBoard {
    val card = columns.firstNotNullOfOrNull { c -> c.cards.firstOrNull { it.key == key } } ?: return this
    return withoutCard(key).withCardAt(card.copy(status = toColumn), toColumn, 0)
}

/** The column labels, in board order. */
internal fun JiraBoard.labels(): List<String> = columns.map(JiraColumn::label)
