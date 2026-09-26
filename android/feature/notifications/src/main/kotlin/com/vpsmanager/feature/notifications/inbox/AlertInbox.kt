package com.vpsmanager.feature.notifications.inbox

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
import com.vpsmanager.core.shell.BridgeCommands
import com.vpsmanager.core.shell.TerminalBridge
import com.vpsmanager.data.ops.OpsAlert
import com.vpsmanager.designsystem.vpsmStatusColors

/**
 * The alerts inbox: triage, not reading.
 *
 * ## Why triage and not a list
 *
 * A list of alerts is read top to bottom and changes nothing. A triage inbox
 * has one question per item — *does this still matter?* — and **one** primary
 * action to answer it. Everything that is not that action stays out of the
 * way, because the moment this gets read is the worst possible moment to
 * choose between five buttons.
 *
 * ## The empty state is the most important screen in this component
 *
 * It is the state one WANTS to see. Which is why it celebrates instead of
 * merely stating: a grey "No alerts", wearing the same face as a list that
 * failed, throws away the only piece of good news an operations panel has to
 * give.
 *
 * ## The primary action is the terminal, not "acknowledge"
 *
 * Acknowledging for real is shared state, and the server has no route for it
 * (see [SeenAlerts]). What the app CAN do, and no competitor does, is take
 * the question to the place that answers any of them: the alert becomes a
 * command in the terminal. "Disk at 94%" becomes a sorted `du` on the mount
 * point — the next thing the person was going to type anyway.
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
            // The button only exists when something is silenced — otherwise
            // it is a control that does nothing, and an inert control teaches
            // people to ignore the whole bar.
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

/**
 * The empty state one wants to see.
 *
 * It tells two silences apart: *nothing fired* and *you have already looked at
 * everything*. They are different states — the second means there is something
 * pending that the person chose to silence, and hiding it would make the
 * screen lie by omission.
 */
@Composable
private fun AllQuiet(hadAlerts: Boolean) {
    val statusColors = vpsmStatusColors
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
    val statusColors = vpsmStatusColors
    val critical = alert.severity.lowercase() in setOf("critical", "crit", "critico", "crítico", "page")
    val par = if (critical) statusColors.critical else statusColors.warning

    Card(
        colors = CardDefaults.cardColors(containerColor = par.container, contentColor = par.content),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                // A text label alongside the colour: colour alone is no good
                // for a colour-blind eye, for sun on the screen or for a
                // screen reader — the same rule the Home screen already follows.
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
                    // "Seen on this device" and never "Acknowledge":
                    // acknowledging is shared state, and this is not. See
                    // SeenAlerts.
                    Text(text = "Seen on this device")
                }
            }
        }
    }
}

private fun suffix(alert: OpsAlert): String =
    alert.unit?.takeIf { it.isNotBlank() }?.let { " $it" } ?: ""

/** `92.0` becomes "92"; `0.5` stays "0,5". A pointless decimal zero only steals width. */
private fun number(value: Double): String =
    if (value == Math.round(value).toDouble()) Math.round(value).toString() else "%.1f".format(value)
