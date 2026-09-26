package dev.servercontrolpanel.feature.jira

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
import dev.servercontrolpanel.data.jira.JiraCard
import dev.servercontrolpanel.data.jira.JiraColumn
import dev.servercontrolpanel.data.jira.JiraBoard
import kotlinx.coroutines.delay

/** Test tags shared by the UI and the tests. */
internal const val TAG_BOARD = "jira-quadro"
internal const val TAG_DRAGGING = "jira-cartao-arrastando"

/** How many columns fit on screen at once. */
private const val VISIBLE_COLUMNS = 3

/**
 * The Jira kanban board.
 *
 * Three narrow columns are shown at once (about 125 dp each on a 411 dp screen) so the board
 * shows where work piles up; hence the compact [BoardCard]. While dragging, the drop target is
 * the highlighted column under the finger; edge scrolling only matters with more than three.
 *
 * There is no pull-to-refresh because it would compete with dragging a card vertically.
 */
@Composable
fun JiraBoardRoute(
    modifier: Modifier = Modifier,
    vm: BoardViewModel = viewModel(),
) {
    val state by vm.state.collectAsStateWithLifecycle()
    val issue by vm.issue.collectAsStateWithLifecycle()
    val creation by vm.creation.collectAsStateWithLifecycle()
    val notice by vm.notice.collectAsStateWithLifecycle()
    val selection by vm.selection.collectAsStateWithLifecycle()
    val selecting by vm.selecting.collectAsStateWithLifecycle()
    val busy by vm.busy.collectAsStateWithLifecycle()

    val notices = remember { SnackbarHostState() }
    LaunchedEffect(notice) {
        val text = notice ?: return@LaunchedEffect
        notices.showSnackbar(text)
        vm.consumeNotice()
    }

    Scaffold(
        modifier = modifier.fillMaxSize(),
        snackbarHost = { SnackbarHost(notices) },
        floatingActionButton = {
            if (state is BoardState.Ready && !selecting) {
                FloatingActionButton(onClick = vm::openCreation) {
                    Icon(Icons.Filled.Add, contentDescription = "Create issue")
                }
            }
        },
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            when (val e = state) {
                is BoardState.Loading -> Centered { CircularProgressIndicator() }

                is BoardState.Disconnected -> ConnectionScreen(
                    busy = busy,
                    onConnect = vm::connect,
                )

                is BoardState.Error -> Centered {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(e.message, style = MaterialTheme.typography.bodyMedium)
                        Spacer(Modifier.height(12.dp))
                        Button(onClick = vm::load) { Text("Try again") }
                    }
                }

                is BoardState.Ready -> Board(
                    board = e.board,
                    slice = vm.currentSlice,
                    selecting = selecting,
                    selection = selection,
                    busy = busy,
                    vm = vm,
                )
            }
        }
    }

    IssueSheet(
        state = issue,
        onClose = vm::closeIssue,
        onMove = { key, column -> vm.move(key, column) },
        onComment = vm::comment,
        onAssignToMe = vm::assignToMe,
        onUnassign = { key -> vm.assign(key, null, null) },
    )

    if (creation.isOpen) {
        CreationSheet(
            state = creation,
            project = vm.currentSlice.project.orEmpty(),
            onClose = vm::closeCreation,
            onCreate = vm::create,
        )
    }
}

@Composable
private fun Centered(content: @Composable () -> Unit) {
    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { content() }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Board(
    board: JiraBoard,
    slice: BoardViewModel.Slice,
    selecting: Boolean,
    selection: Set<String>,
    busy: Boolean,
    vm: BoardViewModel,
) {
    val columns = board.columns
    val dragState = remember { DragState() }
    val scroll = rememberScrollState()
    val haptics = LocalHapticFeedback.current

    // Edge scrolling only when there are more columns than fit on screen.
    val edge = if (columns.size > VISIBLE_COLUMNS) dragState.edge() else null
    LaunchedEffect(edge) {
        if (edge == null) return@LaunchedEffect
        while (true) {
            delay(90)
            val step = when (edge) {
                DragEdge.Left -> -40f
                DragEdge.Right -> 40f
            }
            if (scroll.scrollBy(step) == 0f) break
        }
    }

    Box(
        modifier = Modifier
            .fillMaxSize()
            .testTag(TAG_BOARD)
            .onGloballyPositioned { dragState.rootWidth = it.size.width },
    ) {
        Column(modifier = Modifier.fillMaxSize()) {
            ControlsBar(board = board, slice = slice, selecting = selecting, vm = vm)

            if (selecting) {
                SelectionBar(
                    selectedCount = selection.size,
                    columns = columns.map(JiraColumn::label),
                    busy = busy,
                    onMove = vm::moveSelection,
                    onAssignToMe = vm::assignSelectionToMe,
                    onClear = vm::clearSelection,
                    onExit = vm::toggleSelectionMode,
                )
            }

            board.rejection?.let { reason ->
                // The controls stay visible so the filter that caused the refusal can be changed.
                Text(
                    text = reason,
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

            if (columns.isEmpty()) {
                Centered { Text("This board has no columns.") }
                return@Column
            }

            val target = dragState.targetColumn()

            BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
                // Width is sized so exactly three columns fit on screen.
                val gap = 6.dp
                val margin = 8.dp
                val width = (maxWidth - margin * 2 - gap * (VISIBLE_COLUMNS - 1)) / VISIBLE_COLUMNS

                Row(
                    modifier = Modifier
                        .fillMaxSize()
                        .horizontalScroll(scroll, enabled = !dragState.dragging)
                        .padding(horizontal = margin),
                    horizontalArrangement = Arrangement.spacedBy(gap),
                ) {
                    columns.forEach { column ->
                        BoardColumn(
                            column = column,
                            width = width,
                            dragState = dragState,
                            highlighted = target == column.label,
                            selecting = selecting,
                            selection = selection,
                            onOpen = vm::openIssue,
                            onSelect = vm::toggleSelection,
                            onPick = { haptics.performHapticFeedback(HapticFeedbackType.LongPress) },
                            onDrop = { card ->
                                // Target is the column under the finger on release; outside all columns, nothing moves.
                                dragState.targetColumn()?.let { vm.move(card.key, it) }
                            },
                        )
                    }
                }
            }
        }

        // The floating card is drawn at the root; inside a column it would be clipped to its bounds.
        dragState.card?.let { card ->
            val density = LocalDensity.current
            BoardCard(
                card = card,
                modifier = Modifier
                    .width(with(density) { dragState.size.width.toDp() })
                    .graphicsLayer {
                        translationX = dragState.originInRoot.x + dragState.offset.x
                        translationY = dragState.originInRoot.y + dragState.offset.y
                        scaleX = 1.06f
                        scaleY = 1.06f
                        shadowElevation = 18f
                        alpha = 0.97f
                    }
                    .testTag(TAG_DRAGGING),
            )
        }
    }
}

/**
 * One column: header with label and count, then the cards. [highlighted] marks the drop
 * target during a drag, since narrow columns make the target hard to judge.
 */
@Composable
private fun BoardColumn(
    column: JiraColumn,
    width: androidx.compose.ui.unit.Dp,
    dragState: DragState,
    highlighted: Boolean,
    selecting: Boolean,
    selection: Set<String>,
    onOpen: (String) -> Unit,
    onSelect: (String) -> Unit,
    onPick: () -> Unit,
    onDrop: (JiraCard) -> Unit,
) {
    Column(
        modifier = Modifier
            .width(width)
            .fillMaxHeight()
            .onGloballyPositioned {
                val x = it.positionInRoot().x
                dragState.registerColumn(column.label, x, x + it.size.width)
            }
            .clip(RoundedCornerShape(10.dp))
            .background(
                if (highlighted) {
                    MaterialTheme.colorScheme.secondaryContainer
                } else {
                    MaterialTheme.colorScheme.surfaceContainerLowest
                },
            )
            .border(
                width = if (highlighted) 2.dp else 0.dp,
                color = if (highlighted) MaterialTheme.colorScheme.primary else androidx.compose.ui.graphics.Color.Transparent,
                shape = RoundedCornerShape(10.dp),
            ),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = column.label,
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            Spacer(Modifier.width(4.dp))
            Text(
                text = column.cards.size.toString(),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        HorizontalDivider()

        if (column.cards.isEmpty()) {
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
            items(column.cards, key = { it.key }) { card ->
                val shown = dragState.card?.key == card.key
                BoardCard(
                    card = card,
                    selecting = selecting,
                    selected = card.key in selection,
                    onSelect = { onSelect(card.key) },
                    modifier = Modifier
                        // Keep the original faded in place so the column does not reflow mid-drag.
                        .alpha(if (shown) 0.25f else 1f)
                        .draggable(
                            state = dragState,
                            card = card,
                            column = column.label,
                            // In multi-select, taps select instead of dragging.
                            enabled = !selecting,
                            onPick = onPick,
                            onDrop = onDrop,
                        )
                        .clickable(
                            onClickLabel = if (selecting) "Select" else "Open issue",
                            onClick = {
                                if (selecting) onSelect(card.key) else onOpen(card.key)
                            },
                        ),
                )
            }
        }
    }
}

/** The bar holding the project, the filters and search. */
@Composable
private fun ControlsBar(
    board: JiraBoard,
    slice: BoardViewModel.Slice,
    selecting: Boolean,
    vm: BoardViewModel,
) {
    var searching by remember { mutableStateOf(false) }
    var searchText by remember { mutableStateOf(slice.query) }
    var projectMenu by remember { mutableStateOf(false) }
    var moreMenu by remember { mutableStateOf(false) }

    Column(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box {
                AssistChip(
                    onClick = { projectMenu = true },
                    label = {
                        Text(
                            text = board.project?.takeIf { it.isNotBlank() } ?: "Project",
                            maxLines = 1,
                        )
                    },
                )
                DropdownMenu(expanded = projectMenu, onDismissRequest = { projectMenu = false }) {
                    if (board.projects.isEmpty()) {
                        DropdownMenuItem(
                            text = { Text("No projects visible to this account") },
                            onClick = { projectMenu = false },
                        )
                    }
                    board.projects.forEach { p ->
                        DropdownMenuItem(
                            text = { Text("${p.key} — ${p.name}", maxLines = 1) },
                            onClick = {
                                projectMenu = false
                                vm.switchProject(p.key)
                            },
                        )
                    }
                }
            }

            // Filters and their labels come from the server so they stay in sync with the web
            // panel. They share the project row to save vertical space.
            Row(
                modifier = Modifier
                    .weight(1f)
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = 6.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                board.filters.forEach { f ->
                    FilterChip(
                        selected = board.filter == f.key,
                        onClick = { vm.switchFilter(f.key) },
                        label = { Text(f.label, maxLines = 1) },
                    )
                }
            }

            IconButton(onClick = { searching = !searching }) {
                Icon(Icons.Filled.Search, contentDescription = "Search issues")
            }
            Box {
                IconButton(onClick = { moreMenu = true }) {
                    Icon(Icons.Filled.MoreVert, contentDescription = "More board options")
                }
                DropdownMenu(expanded = moreMenu, onDismissRequest = { moreMenu = false }) {
                    DropdownMenuItem(
                        text = { Text("Refresh") },
                        leadingIcon = { Icon(Icons.Filled.Refresh, contentDescription = null) },
                        onClick = {
                            moreMenu = false
                            vm.load()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(if (selecting) "Exit selection" else "Select multiple") },
                        onClick = {
                            moreMenu = false
                            vm.toggleSelectionMode()
                        },
                    )
                    HorizontalDivider()
                    listOf(
                        "" to "Jira order",
                        "updated:desc" to "Recently updated first",
                        "key:asc" to "By key",
                        "name:asc" to "By summary",
                        "type:asc" to "By type",
                    ).forEach { (criterion, label) ->
                        DropdownMenuItem(
                            text = { Text(label) },
                            leadingIcon = {
                                if (slice.order == criterion) {
                                    Icon(Icons.Filled.Check, contentDescription = null)
                                }
                            },
                            onClick = {
                                moreMenu = false
                                vm.sortBy(criterion)
                            },
                        )
                    }
                    HorizontalDivider()
                    listOf(
                        0 to "Show all done",
                        7 to "Done in the last 7 days",
                        30 to "Done in the last 30 days",
                    ).forEach { (days, label) ->
                        DropdownMenuItem(
                            text = { Text(label) },
                            leadingIcon = {
                                if (slice.hideDoneAfter == days) {
                                    Icon(Icons.Filled.Check, contentDescription = null)
                                }
                            },
                            onClick = {
                                moreMenu = false
                                vm.hideDoneAfter(days)
                            },
                        )
                    }
                }
            }
        }

        if (searching) {
            OutlinedTextField(
                value = searchText,
                onValueChange = { searchText = it },
                label = { Text("Search by key, summary, label or person") },
                singleLine = true,
                trailingIcon = {
                    IconButton(onClick = {
                        searchText = ""
                        vm.search("")
                    }) {
                        Icon(Icons.Filled.Close, contentDescription = "Clear search")
                    }
                },
                modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 4.dp),
            )
            Row(modifier = Modifier.padding(horizontal = 12.dp)) {
                TextButton(onClick = { vm.search(searchText) }) { Text("Search") }
            }
        }
    }
}

/** The bar that appears while multi-select is on. */
@Composable
private fun SelectionBar(
    selectedCount: Int,
    columns: List<String>,
    busy: Boolean,
    onMove: (String) -> Unit,
    onAssignToMe: () -> Unit,
    onClear: () -> Unit,
    onExit: () -> Unit,
) {
    var moveMenu by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.secondaryContainer)
            .padding(horizontal = 12.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = if (selectedCount == 0) "None selected" else "$selectedCount selected",
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSecondaryContainer,
        )
        Spacer(Modifier.weight(1f))
        if (busy) {
            CircularProgressIndicator(modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
        }
        Box {
            TextButton(onClick = { moveMenu = true }, enabled = selectedCount > 0 && !busy) {
                Text("Move")
            }
            DropdownMenu(expanded = moveMenu, onDismissRequest = { moveMenu = false }) {
                columns.forEach { label ->
                    DropdownMenuItem(
                        text = { Text(label) },
                        onClick = {
                            moveMenu = false
                            onMove(label)
                        },
                    )
                }
            }
        }
        TextButton(onClick = onAssignToMe, enabled = selectedCount > 0 && !busy) {
            Text("Assign to me")
        }
        IconButton(onClick = if (selectedCount > 0) onClear else onExit) {
            Icon(
                Icons.Filled.Close,
                contentDescription = if (selectedCount > 0) "Clear selection" else "Exit selection",
            )
        }
    }
}
