package dev.servercontrolpanel.feature.notifications.inbox

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.core.shell.BridgeCommands
import dev.servercontrolpanel.core.shell.TerminalBridge
import dev.servercontrolpanel.data.ops.OpsAlert
import dev.servercontrolpanel.designsystem.panelStatusColors

/**
 * Alert triage inbox: one primary action per alert. The empty state is shown as good news, not
 * as a grey list that looks like a failure.
 *
 * The primary action opens the alert as a terminal command, since a real acknowledge has no
 * server API (see [SeenAlerts]).
 */
@Composable
internal fun AlertInbox(
    alerts: List<OpsAlert>,
    seen: Set<String>,
    onMarkSeen: (OpsAlert) -> Unit,
    onUnmarkAll: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val pending = alerts.filterNot { SeenAlerts.keyOf(it) in seen }

    Column(modifier = modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "Firing now",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
            )
            // Only shown when something is silenced, so it is never an inert control.
            if (alerts.size > pending.size) {
                TextButton(onClick = onUnmarkAll) { Text(text = "Show seen") }
            }
        }

        if (pending.isEmpty()) {
            AllQuiet(hadAlerts = alerts.isNotEmpty())
        } else {
            pending.forEach { alert ->
                AlertRow(alert = alert, onMarkSeen = { onMarkSeen(alert) })
            }
        }
    }
}

/** Empty state that distinguishes "nothing firing" from "everything already marked seen". */
@Composable
private fun AllQuiet(hadAlerts: Boolean) {
    val statusColors = panelStatusColors
    Card(
        colors = CardDefaults.cardColors(containerColor = statusColors.ok.container),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = if (hadAlerts) "All seen" else "Nothing firing",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = statusColors.ok.accent,
            )
            Text(
                text = if (hadAlerts) {
                    "The alerts are still active on the server — you marked them as seen on this device."
                } else {
                    "No alert rule is firing on the server right now."
                },
                style = MaterialTheme.typography.bodySmall,
                color = statusColors.ok.content,
                textAlign = TextAlign.Center,
            )
        }
    }
}

@Composable
private fun AlertRow(alert: OpsAlert, onMarkSeen: () -> Unit) {
    val statusColors = panelStatusColors
    val critical = alert.severity.lowercase() in setOf("critical", "crit", "critico", "crítico", "page")
    val par = if (critical) statusColors.critical else statusColors.warning

    Card(
        colors = CardDefaults.cardColors(containerColor = par.container, contentColor = par.content),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                // Text label alongside colour, for colour blindness, glare and screen readers.
                text = if (critical) "CRITICAL · ${alert.name}" else "WARNING · ${alert.name}",
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
            )
            Text(
                text = "${number(alert.currentValue)}${suffix(alert)} " +
                    "(threshold ${number(alert.threshold)}${suffix(alert)}) · ${alert.state}",
                style = MaterialTheme.typography.bodyMedium,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                TextButton(
                    onClick = {
                        val cmd = BridgeCommands.unitJournal(alert.name)
                        TerminalBridge.send(cmd.command, cmd.origin)
                    },
                ) { Text(text = "View in terminal") }
                TextButton(onClick = onMarkSeen) {
                    // Never "Acknowledge": this is local, not shared state (see SeenAlerts).
                    Text(text = "Seen on this device")
                }
            }
        }
    }
}

private fun suffix(alert: OpsAlert): String =
    alert.unit?.takeIf { it.isNotBlank() }?.let { " $it" } ?: ""

/** Drops a pointless decimal zero (`92.0` becomes "92"); other values keep one decimal. */
private fun number(value: Double): String =
    if (value == Math.round(value).toDouble()) Math.round(value).toString() else "%.1f".format(value)
