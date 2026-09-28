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

private fun valueFor(row: JsonObject, column: SduiTableColumn): String =
    row[column.key]?.jsonPrimitive?.contentOrNull.orEmpty()

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
