package dev.servercontrolpanel.sdui.table

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.core.sdui.SduiActionRef
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.core.sdui.SduiTableColumn
import dev.servercontrolpanel.sdui.actionrunner.ActionInvocation
import dev.servercontrolpanel.sdui.actionrunner.ActionOutcome
import dev.servercontrolpanel.sdui.actionrunner.ActionRunner
import dev.servercontrolpanel.sdui.actionrunner.Confirmation
import dev.servercontrolpanel.sdui.confirm.ConfirmDestructiveComponent
import dev.servercontrolpanel.sdui.data.ComponentDataState
import dev.servercontrolpanel.sdui.data.rememberComponentDataState
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive

/**
 * Renders a [SduiComponent.Table] as a vertical list of row cards — each row
 * is its own two-line-per-column block (label above value) rather than a
 * horizontal grid. This is a deliberate phone-friendliness choice: with no
 * horizontal grid, there is never a reason for this component (or the screen
 * hosting it) to scroll sideways — the `LazyColumn` in
 * [dev.servercontrolpanel.sdui.SduiScreen] that hosts this component only scrolls
 * vertically, and the rows here are emitted in a plain `Column` precisely so
 * as not to nest a second vertical scroll inside it (see the comment in the
 * body). A future wide-table layout, if ever needed, would need
 * its own horizontal scroll container that stops at this component's bounds;
 * this one does not need it.
 *
 * A row action dispatches through [actionRunner] exactly like
 * [dev.servercontrolpanel.sdui.action.ActionComponent]: [confirmations] decides
 * whether the tap opens [ConfirmDestructiveComponent] first, the request
 * always carries the row's own `id` under `params["id"]` (the shape every
 * scheduler row action on the server reads — see `scheduler_actions.go`'s
 * `handleSchedulerJobRunNow`/`handleSchedulerJobDelete`), and the outcome is
 * bubbled to [onOutcome] unchanged. [actionRunner] is nullable so a preview
 * screen with no live runner still renders the table read-only, matching
 * every other mutation-capable component in this module.
 *
 * Neither [ActionOutcome.Patched] nor [ActionOutcome.Invalidated] is visible
 * here on its own — [ActionRunner] already applied either one to
 * [dev.servercontrolpanel.sdui.actionrunner.ScreenState], but this composable's own
 * [rememberComponentDataState] call fetches independently of that cache (see
 * that function's own doc comment). [refreshKey] is the caller's way of
 * forcing this table to re-pull its rows once such an outcome has landed —
 * without it, "run now" would need a full screen reload to become visible.
 */
@Composable
fun TableComponent(
    component: SduiComponent.Table,
    actionRunner: ActionRunner? = null,
    confirmations: Map<String, SduiComponent.ConfirmDestructive> = emptyMap(),
    onOutcome: (ActionOutcome) -> Unit = {},
    refreshKey: Any? = null,
) {
    var pendingAction by remember(component.id) { mutableStateOf<PendingRowAction?>(null) }
    val scope = rememberCoroutineScope()

    fun dispatch(actionId: String, row: JsonObject, confirmation: Confirmation?) {
        val runner = actionRunner ?: return
        val invocation = rowActionInvocation(actionId, row, confirmation, confirmations) ?: return
        scope.launch {
            val outcome = runner.run(invocation)
            onOutcome(outcome)
        }
    }

    val onRowAction: (String, JsonObject) -> Unit = { actionId, row ->
        val declaration = confirmations[actionId]
        if (declaration != null) {
            pendingAction = PendingRowAction(declaration, actionId, row)
        } else {
            dispatch(actionId, row, null)
        }
    }

    when (val state = rememberComponentDataState(component.rowsSource, refreshKey = refreshKey).value) {
        is ComponentDataState.Loading -> LoadingBlock()
        is ComponentDataState.Error -> ErrorBlock(state.reason)
        is ComponentDataState.Empty -> EmptyBlock(component.emptyState?.text ?: "Nothing to show.")
        is ComponentDataState.Data -> {
            // Column, NEVER LazyColumn: this component is rendered INSIDE an
            // item of [dev.servercontrolpanel.sdui.SduiScreen]'s LazyColumn, which
            // gives the child an infinite maximum height. A second vertical
            // scroll in there is forbidden by Compose and took the whole app
            // down with IllegalStateException("Vertically scrollable component
            // was measured with an infinity maximum height constraints") as
            // soon as the Admin screen managed to load the descriptor.
            //
            // No laziness is lost: `state.rows` is already the entire list
            // materialised in memory (a single JSON response from
            // `rows_source`), so the inner LazyColumn was only recycling
            // views — and the outer LazyColumn still provides the laziness
            // that matters, which is about the screen's list of COMPONENTS.
            // A single scroll axis is also better on a phone: two nested
            // vertical scrolls hijack the user's gesture.
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                state.rows.forEach { row ->
                    TableRow(row = row, columns = component.columns, rowActions = component.rowActions, onRowAction = onRowAction)
                }
            }
        }
    }

    pendingAction?.let { pending ->
        ConfirmDestructiveComponent(
            descriptor = pending.declaration,
            onConfirm = { confirmation ->
                pendingAction = null
                dispatch(pending.actionId, pending.row, confirmation)
            },
            onDismiss = { pendingAction = null },
        )
    }
}

private data class PendingRowAction(
    val declaration: SduiComponent.ConfirmDestructive,
    val actionId: String,
    val row: JsonObject,
)

/**
 * Builds the [ActionInvocation] a row action dispatches, or `null` if [row]
 * carries no `id` (a malformed row this component refuses to act on rather
 * than guess at). Kept as a plain function, not inlined into the
 * `@Composable` above, so it is JVM-testable the same way
 * `dev.servercontrolpanel.sdui.actionrunner.confirmationFor`/`bindErrors` are —
 * see `TableRowActionTest`.
 *
 * `params["id"]` is the row's own `id` field: every scheduler row action on
 * the server (`internal/mobilebff/screens/scheduler_actions.go`'s
 * `handleSchedulerJobRunNow`/`handleSchedulerJobDelete`) reads exactly that
 * key to find the row being acted on. [destructive] is never inferred from
 * [actionId]'s text — only from whether [confirmations] declares a
 * `confirm_destructive` for it, the same rule
 * [dev.servercontrolpanel.sdui.action.ActionComponent] follows.
 */
internal fun rowActionInvocation(
    actionId: String,
    row: JsonObject,
    confirmation: Confirmation?,
    confirmations: Map<String, SduiComponent.ConfirmDestructive>,
): ActionInvocation? {
    val rowId = row["id"]?.jsonPrimitive?.contentOrNull ?: return null
    return ActionInvocation(
        actionId = actionId,
        params = mapOf("id" to rowId),
        confirmation = confirmation,
        destructive = confirmations.containsKey(actionId),
    )
}

@Composable
private fun TableRow(
    row: JsonObject,
    columns: List<SduiTableColumn>,
    rowActions: List<SduiActionRef>?,
    onRowAction: (actionId: String, row: JsonObject) -> Unit,
) {
    // THE ANATOMY OF A ROW, and why it changed.
    //
    // Each column used to become a label-above-value pair, stacked
    // vertically. A TWO-column table ("Stack: n8n" / "Status: running") took
    // up ~270 px — four lines of text for two facts — and five items already
    // filled the phone's whole screen. The operator called it "very poor", and
    // the diagnosis is geometric, not aesthetic: information per pixel was
    // tiny.
    //
    // The new design is that of any good mobile list (Gmail, Play Console,
    // Portainer on mobile):
    //
    //   ┌─────────────────────────────────────────────┐
    //   │ n8n                        [running]     ⋮  │  ← title + badge + actions
    //   │ Created 3d · Ports 5678 · Network bridge    │  ← meta, label·value
    //   └─────────────────────────────────────────────┘
    //
    // Three rules do the work:
    //
    // 1. The FIRST non-badge column becomes the TITLE, with no label. "Stack"
    //    above "n8n" tells you nothing the screen does not already say — the
    //    section header has already said this is a list of stacks.
    // 2. BADGE columns move up to the title line. A badge is the scanning read
    //    ("which one is down?") and has to sit on the same visual axis as the
    //    names, not hidden underneath. The "Status" label goes away too: the
    //    value `running` describes itself.
    // 3. THE REST becomes a meta line, with label and value SIDE BY SIDE,
    //    separated by a middle dot, wrapping when it has to (`FlowRow`).
    //
    // Measured result on those same two columns: ~270 px → ~64 px. Four times
    // as many rows per screen, with the same information.
    val badges = columns.filter { it.kind == "badge" }
    val others = columns.filter { it.kind != "badge" }
    val title = others.firstOrNull()
    val meta = if (title == null) others else others.drop(1)

    Card(modifier = Modifier.fillMaxWidth()) {
        Row(
            modifier = Modifier.padding(start = 14.dp, end = 4.dp, top = 10.dp, bottom = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                // Title and badges on the SAME line. The title yields space
                // (`weight`) so the badge is never pushed out — the title is
                // what gets an ellipsis, because a truncated name is still
                // recognisable and a cut-off badge is not.
                Row(verticalAlignment = Alignment.CenterVertically) {
                    if (title != null) {
                        Text(
                            text = valueFor(row, title),
                            style = MaterialTheme.typography.titleSmall,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f, fill = false),
                        )
                    }
                    badges.forEach { badge ->
                        Spacer(modifier = Modifier.width(8.dp))
                        BadgeValue(raw = valueFor(row, badge), badgeMap = badge.badgeMap)
                    }
                }
                if (meta.isNotEmpty()) {
                    MetaRow(row = row, columns = meta)
                }
            }
            if (!rowActions.isNullOrEmpty()) {
                RowActionsMenu(actions = rowActions, onAction = { actionId -> onRowAction(actionId, row) })
            }
        }
    }
}

/**
 * The secondary columns on a single line, `label value` separated by a middle
 * dot — wrapping to the line below when they do not fit.
 *
 * The label is in [MaterialTheme.typography.labelSmall] with the secondary
 * colour and the value in `bodySmall` with the normal one: hierarchy comes
 * from weight and colour, on the same horizontal axis, instead of spending a
 * whole line per pair. Columns with an empty value disappear — a label
 * pointing at nothing is pure noise, and the previous version drew it anyway.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun MetaRow(row: JsonObject, columns: List<SduiTableColumn>) {
    val visible = columns.filter { valueFor(row, it).isNotBlank() }
    if (visible.isEmpty()) return

    FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        visible.forEachIndexed { index, column ->
            if (index > 0) {
                Text(
                    text = "·",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.outline,
                )
            }
            Text(
                text = column.label,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = valueFor(row, column),
                style = MaterialTheme.typography.bodySmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * The column's value, exactly as the server sent it — no date or number
 * formatting happens here, on purpose: `datetime` and the like already arrive
 * finished, and formatting on the client is how the two ends start to
 * disagree.
 */
private fun valueFor(row: JsonObject, column: SduiTableColumn): String =
    row[column.key]?.jsonPrimitive?.contentOrNull.orEmpty()

/**
 * The state badge. Coloured by the semantic TONE the server sends
 * (`badge_map`), never by text guessed at on the client — it is the server
 * that knows whether `exited` is a failure or a normal end.
 */
@Composable
private fun BadgeValue(raw: String, badgeMap: Map<String, String>?) {
    if (raw.isBlank()) return
    val tone = badgeMap?.get(raw)
    Surface(
        color = when (tone) {
            "success" -> MaterialTheme.colorScheme.primaryContainer
            "danger" -> MaterialTheme.colorScheme.errorContainer
            else -> MaterialTheme.colorScheme.secondaryContainer
        },
        contentColor = when (tone) {
            "success" -> MaterialTheme.colorScheme.onPrimaryContainer
            "danger" -> MaterialTheme.colorScheme.onErrorContainer
            else -> MaterialTheme.colorScheme.onSecondaryContainer
        },
        shape = MaterialTheme.shapes.small,
    ) {
        Text(
            text = raw,
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
            style = MaterialTheme.typography.labelSmall,
            maxLines = 1,
        )
    }
}

@Composable
private fun RowActionsMenu(actions: List<SduiActionRef>, onAction: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    Box {
        Text(
            text = "⋮",
            modifier = Modifier
                .padding(4.dp)
                .clickable { expanded = true },
        )
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            actions.forEach { action ->
                DropdownMenuItem(
                    text = { Text(action.label ?: action.actionId) },
                    onClick = {
                        expanded = false
                        onAction(action.actionId)
                    },
                )
            }
        }
    }
}

@Composable
private fun LoadingBlock() {
    Box(modifier = Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun ErrorBlock(reason: String) {
    Text(text = reason, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
}

@Composable
private fun EmptyBlock(text: String) {
    Text(text = text, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(16.dp))
}
