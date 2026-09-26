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
import com.vpsmanager.core.shell.ComandosDaPonte
import com.vpsmanager.core.shell.PonteComOTerminal
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
 * (see [AlertasVistos]). What the app CAN do, and no competitor does, is take
 * the question to the place that answers any of them: the alert becomes a
 * command in the terminal. "Disk at 94%" becomes a sorted `du` on the mount
 * point — the next thing the person was going to type anyway.
 */
@Composable
internal fun CaixaDeAlertas(
    alertas: List<OpsAlert>,
    vistos: Set<String>,
    aoMarcarVisto: (OpsAlert) -> Unit,
    aoDesmarcarTudo: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val pendentes = alertas.filterNot { AlertasVistos.chaveDe(it) in vistos }

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
            if (alertas.size > pendentes.size) {
                TextButton(onClick = aoDesmarcarTudo) { Text(text = "Show seen") }
            }
        }

        if (pendentes.isEmpty()) {
            SilencioBom(havia = alertas.isNotEmpty())
        } else {
            pendentes.forEach { alerta ->
                LinhaDeAlerta(alerta = alerta, aoMarcarVisto = { aoMarcarVisto(alerta) })
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
private fun SilencioBom(havia: Boolean) {
    val cores = vpsmStatusColors
    Card(
        colors = CardDefaults.cardColors(containerColor = cores.ok.container),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text(
                text = if (havia) "All seen" else "Nothing firing",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = cores.ok.accent,
            )
            Text(
                text = if (havia) {
                    "The alerts are still active on the server — you marked them as seen on this device."
                } else {
                    "No alert rule is firing on the server right now."
                },
                style = MaterialTheme.typography.bodySmall,
                color = cores.ok.content,
                textAlign = TextAlign.Center,
            )
        }
    }
}

@Composable
private fun LinhaDeAlerta(alerta: OpsAlert, aoMarcarVisto: () -> Unit) {
    val cores = vpsmStatusColors
    val critico = alerta.severity.lowercase() in setOf("critical", "crit", "critico", "crítico", "page")
    val par = if (critico) cores.critical else cores.warning

    Card(
        colors = CardDefaults.cardColors(containerColor = par.container, contentColor = par.content),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                // A text label alongside the colour: colour alone is no good
                // for a colour-blind eye, for sun on the screen or for a
                // screen reader — the same rule the Home screen already follows.
                text = if (critico) "CRITICAL · ${alerta.name}" else "WARNING · ${alerta.name}",
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
            )
            Text(
                text = "${numero(alerta.currentValue)}${sufixo(alerta)} " +
                    "(threshold ${numero(alerta.threshold)}${sufixo(alerta)}) · ${alerta.state}",
                style = MaterialTheme.typography.bodyMedium,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                TextButton(
                    onClick = {
                        val cmd = ComandosDaPonte.journalDaUnidade(alerta.name)
                        PonteComOTerminal.mandar(cmd.comando, cmd.origem)
                    },
                ) { Text(text = "View in terminal") }
                TextButton(onClick = aoMarcarVisto) {
                    // "Seen on this device" and never "Acknowledge":
                    // acknowledging is shared state, and this is not. See
                    // AlertasVistos.
                    Text(text = "Seen on this device")
                }
            }
        }
    }
}

private fun sufixo(alerta: OpsAlert): String =
    alerta.unit?.takeIf { it.isNotBlank() }?.let { " $it" } ?: ""

/** `92.0` becomes "92"; `0.5` stays "0,5". A pointless decimal zero only steals width. */
private fun numero(valor: Double): String =
    if (valor == Math.round(valor).toDouble()) Math.round(valor).toString() else "%.1f".format(valor)
