package com.vpsmanager.feature.auth

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.vpsmanager.data.dashboard.DashboardSnapshot
import com.vpsmanager.data.dashboard.DashboardTarget
import com.vpsmanager.data.dashboard.HealthEntry
import com.vpsmanager.data.dashboard.ResourceSignal
import com.vpsmanager.data.dashboard.Severity
import com.vpsmanager.designsystem.StatusColorPair
import com.vpsmanager.designsystem.vpsmStatusColors

/** The theme colours for one severity. */
@Composable
internal fun colorsFor(severity: Severity): StatusColorPair {
    val colors = vpsmStatusColors
    return when (severity) {
        Severity.OK -> colors.ok
        Severity.ATENCAO -> colors.warning
        Severity.CRITICO -> colors.critical
    }
}

/**
 * The severity label in TEXT, alongside the colour.
 *
 * Colour on its own is not accessible information — colour blindness, a screen
 * in the sun, a screen reader. The word carries the same meaning without
 * depending on the pigment.
 */
internal fun labelFor(severity: Severity): String = when (severity) {
    Severity.OK -> "ok"
    Severity.ATENCAO -> "WARNING"
    Severity.CRITICO -> "CRITICAL"
}

/**
 * The envelope of every card on the dashboard.
 *
 * Elevation by TONE, not by shadow: with six cards stacked up, a shadow on all
 * of them becomes noise, and Material 3 reserves shadow for "protection against
 * the background or encouragement to interact". The card that asks for
 * attention gets its own coloured container, and it is the only one that stands
 * out from the rest.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun DashboardCard(
    title: String,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
    container: Color = MaterialTheme.colorScheme.surfaceContainer,
    content: @Composable ColumnScope.() -> Unit,
) {
    Card(
        modifier = modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = container),
        elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
    ) {
        Column(
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    text = title,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                )
                if (subtitle != null) {
                    Text(
                        text = subtitle,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            content()
        }
    }
}

/**
 * A row that leads somewhere.
 *
 * Every row of the dashboard is navigable on purpose: a dashboard that only
 * informs forces the person to go hunting once they have found out. The chevron
 * on the right is the only ornament, and it is there to say "this is a route,
 * not a label".
 */
@Composable
internal fun NavigableRow(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Box(modifier = Modifier.weight(1f)) { content() }
        Icon(
            imageVector = Icons.AutoMirrored.Filled.KeyboardArrowRight,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(20.dp),
        )
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. NEEDS ATTENTION — the only card that DISAPPEARS when it is empty
// ─────────────────────────────────────────────────────────────────────────────

/**
 * What is broken, worst first.
 *
 * It gathers into a single list the alerts the SERVER raised and the resource
 * thresholds the app crossed — because, for somebody opening the app in a
 * hurry, the question is "did something break?", and the answer does not change
 * according to who noticed.
 */
@Composable
internal fun AttentionCard(
    signals: List<ResourceSignal>,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    val worst = signals.maxOfOrNull { it.severity } ?: Severity.ATENCAO
    val colors = colorsFor(worst)
    Card(
        modifier = modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = colors.container),
        elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
    ) {
        Column(
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 14.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Icon(
                    imageVector = Icons.Filled.Warning,
                    contentDescription = null,
                    tint = colors.accent,
                    modifier = Modifier.size(22.dp),
                )
                Text(
                    text = atencaoTitulo(signals.size),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = colors.content,
                )
            }
            signals.forEach { signal ->
                NavigableRow(onClick = { onTarget(signal.target) }) {
                    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            Text(
                                text = signal.label,
                                style = MaterialTheme.typography.bodyLarge,
                                fontWeight = FontWeight.SemiBold,
                                color = colors.content,
                            )
                            if (signal.headline.isNotBlank()) {
                                Text(
                                    text = signal.headline,
                                    style = MaterialTheme.typography.bodyLarge,
                                    fontFamily = FontFamily.Monospace,
                                    fontWeight = FontWeight.Bold,
                                    color = colors.content,
                                )
                            }
                            SeverityTag(signal.severity)
                        }
                        Text(
                            text = signal.detail,
                            style = MaterialTheme.typography.bodySmall,
                            color = colors.content,
                        )
                    }
                }
            }
        }
    }
}

/** "1 thing needs attention" / "3 things need attention". */
internal fun atencaoTitulo(count: Int): String =
    if (count == 1) "1 thing needs attention" else "$count things need attention"

@Composable
private fun SeverityTag(severity: Severity) {
    if (severity == Severity.OK) return
    val colors = colorsFor(severity)
    Text(
        text = labelFor(severity),
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.Bold,
        color = colors.accent,
        modifier = Modifier
            .background(colors.accent.copy(alpha = 0.14f), RoundedCornerShape(4.dp))
            .padding(horizontal = 6.dp, vertical = 2.dp),
    )
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. HEALTH — an aggregated rollup, not a sea of dots
// ─────────────────────────────────────────────────────────────────────────────

/**
 * The subsystems, aggregated.
 *
 * Nine green rows train the eye to skip the whole area — and on the day one of
 * them turns red it disappears along with the others. Hence: one summary
 * ("9 subsystems · all ok"), the ones that deviated always visible, and the
 * healthy ones behind a button that has to be asked for.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun HealthCard(
    entries: List<HealthEntry>,
    quietLine: String?,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    var mostrarSaudaveis by remember { mutableStateOf(false) }
    val desviados = entries.filter { it.severity != Severity.OK }
    val saudaveis = entries.filter { it.severity == Severity.OK }
    val resumo = when {
        entries.isEmpty() -> "no subsystems reported"
        desviados.isEmpty() -> "${saudaveis.size} subsystems · all ok"
        else -> "${saudaveis.size} ok · ${desviados.size} out of normal"
    }

    DashboardCard(
        title = "Server health",
        subtitle = resumo,
        modifier = modifier,
    ) {
        if (quietLine != null) {
            Text(
                text = quietLine,
                style = MaterialTheme.typography.bodyMedium,
                color = vpsmStatusColors.ok.accent,
            )
        }
        desviados.forEach { entry ->
            NavigableRow(onClick = { onTarget(DashboardTarget.SERVICOS) }) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Text(text = entry.name, style = MaterialTheme.typography.bodyLarge)
                    Text(
                        text = entry.status,
                        style = MaterialTheme.typography.bodyMedium,
                        fontFamily = FontFamily.Monospace,
                        color = colorsFor(entry.severity).accent,
                    )
                    SeverityTag(entry.severity)
                }
            }
        }
        if (saudaveis.isNotEmpty()) {
            OutlinedButton(onClick = { mostrarSaudaveis = !mostrarSaudaveis }) {
                Icon(
                    imageVector = if (mostrarSaudaveis) Icons.Filled.KeyboardArrowUp else Icons.Filled.KeyboardArrowDown,
                    contentDescription = null,
                    modifier = Modifier.size(18.dp),
                )
                Spacer(Modifier.width(6.dp))
                Text(
                    text = if (mostrarSaudaveis) {
                        "hide the ${saudaveis.size} healthy ones"
                    } else {
                        "show the ${saudaveis.size} healthy ones"
                    },
                    style = MaterialTheme.typography.labelLarge,
                )
            }
            if (mostrarSaudaveis) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    saudaveis.forEach { entry ->
                        Text(
                            text = "${entry.name} ${entry.status}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(vertical = 2.dp),
                        )
                    }
                }
            }
        }
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. THE "NOW" CARD IS GONE — at the owner's request
// ─────────────────────────────────────────────────────────────────────────────
//
// It showed the queue, the last deploy and the next scheduled one. The problem
// was not the content, it was the placement: three rows of permanent height on
// the FIRST screen to say, almost always, "queue idle" and "no deploy on
// record" — a fixed frame around the absence of news.
//
// Nothing lost any reach. The three destinations are still one tap away by two
// routes: the block grid on this very screen (`BlocosDoPainel` wires up QUEUE,
// DEPLOYS and SCHEDULED) and the child pages of Operations ("Job queue",
// "Scheduler", "Deploy").
//
// It is the same yardstick that earlier took the health card and the resources
// card out of here, and it is written just below, in `HomeScreen`: if this ever
// comes back, it has to come back as an EXCEPTION — showing up only when there
// is something happening, never as a permanent frame.

// ─────────────────────────────────────────────────────────────────────────────
// 4. RESOURCES — saturation, AFTER whatever broke
// ─────────────────────────────────────────────────────────────────────────────

/**
 * CPU, memory, swap, disks and network.
 *
 * It comes fourth on purpose, against the instinct of every VPS dashboard:
 * saturation is a DEBUGGING metric, not an alerting one — it explains *why*
 * after something has already fired. Putting these numbers at the top trains
 * the operator to look at numbers instead of looking at problems.
 *
 * What rises to the top is not the number: it is the fact that it crossed a
 * threshold. When that happens, the same signal appears on card no. 1 (as an
 * alert) AND here (as a number) — the two roles it plays.
 */
@Composable
internal fun ResourcesCard(
    signals: List<ResourceSignal>,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    val piores = signals.count { it.severity != Severity.OK }
    DashboardCard(
        title = "Resources",
        subtitle = if (piores == 0) "all within thresholds" else "$piores over threshold",
        modifier = modifier,
    ) {
        signals.forEach { signal ->
            ResourceRow(signal = signal, onClick = { onTarget(signal.target) })
        }
    }
}

@Composable
private fun ResourceRow(signal: ResourceSignal, onClick: () -> Unit) {
    val colors = colorsFor(signal.severity)
    val destaque = signal.severity != Severity.OK
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .background(
                color = if (destaque) colors.container else Color.Transparent,
                shape = RoundedCornerShape(8.dp),
            )
            .padding(horizontal = if (destaque) 10.dp else 0.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        // The bar on the left gives the peripheral reading: even without
        // reading, you can see how many rows are out of line. It is decoration
        // over information that is already in text — which is why it does not
        // speak to the screen reader.
        Box(
            modifier = Modifier
                .width(3.dp)
                .height(28.dp)
                .background(
                    color = if (destaque) colors.accent else MaterialTheme.colorScheme.outlineVariant,
                    shape = RoundedCornerShape(2.dp),
                )
                .clearAndSetSemantics { },
        )
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(1.dp)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = signal.label,
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = if (destaque) FontWeight.SemiBold else FontWeight.Normal,
                    color = if (destaque) colors.content else MaterialTheme.colorScheme.onSurface,
                )
                if (destaque) SeverityTag(signal.severity)
            }
            Text(
                text = signal.detail,
                style = MaterialTheme.typography.bodySmall,
                color = if (destaque) colors.content else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        // A signal with no single value (the network, whose value is a pair)
        // does not reserve the right-hand column: it disappears and the detail
        // uses the full width.
        if (signal.headline.isNotBlank()) {
            Text(
                text = signal.headline,
                style = MaterialTheme.typography.titleMedium,
                fontFamily = FontFamily.Monospace,
                fontWeight = FontWeight.Bold,
                color = if (destaque) colors.accent else MaterialTheme.colorScheme.onSurface,
            )
        }
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. QUICK ACTIONS — the "and then?" of every glance
// ─────────────────────────────────────────────────────────────────────────────

/** The four destinations the operator opens after looking at the dashboard. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun QuickActionsCard(
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    DashboardCard(title = "Quick actions", modifier = modifier) {
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            OutlinedButton(onClick = { onTarget(DashboardTarget.TERMINAL) }) { Text("Terminal") }
            OutlinedButton(onClick = { onTarget(DashboardTarget.DOCKER) }) { Text("Docker") }
            OutlinedButton(onClick = { onTarget(DashboardTarget.DEPLOYS) }) { Text("Deploys") }
            OutlinedButton(onClick = { onTarget(DashboardTarget.AUDITORIA) }) { Text("Audit log") }
        }
    }
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. SESSION — identity last
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Who I am and against which machine.
 *
 * It moved down to the footer because it is identity, not operations — the
 * operator does not open the app in a hurry to find out their own e-mail
 * address. What gained weight here is the server clock: a divergence between it
 * and the phone's explains half of the "but I just ran that".
 */
@Composable
internal fun SessionCard(
    snapshot: DashboardSnapshot,
    driftText: String?,
    modifier: Modifier = Modifier,
    onAbrirSeguranca: () -> Unit = {},
    onAbrirDiagnostico: () -> Unit = {},
) {
    val identity = snapshot.identity
    val system = snapshot.ops.system
    DashboardCard(
        title = "Session",
        subtitle = identity?.let { if (it.isAdmin) "${it.user} · admin" else it.user }
            ?: "identity unavailable",
        modifier = modifier,
    ) {
        if (identity != null) {
            Text(text = identity.email, style = MaterialTheme.typography.bodyMedium)
        } else {
            Text(
                text = "The server did not return this session's identity. The dashboard above is still valid.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (system != null) {
            Text(
                text = listOfNotNull(system.hostname, system.platform).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = "up for ${system.uptimeText} · server clock ${system.serverTime}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (driftText != null) {
            Text(
                text = driftText,
                style = MaterialTheme.typography.bodySmall,
                fontWeight = FontWeight.SemiBold,
                color = vpsmStatusColors.warning.accent,
            )
        }
        // DEVICE security sits next to this device's identity, which is what
        // this card is about. The label says what is to be found inside —
        // "Security" on its own does not distinguish the server's security from
        // the security of whoever is holding the phone.
        Row {
            TextButton(onClick = onAbrirSeguranca) {
                Text("Lock and screenshots")
            }
            // The diagnostics used to live ONLY behind an update failure —
            // and, in the failure that matters most (installation blocked),
            // the strip offers the browser rather than the diagnostics. That
            // is: the more they were needed, the less they were reachable.
            // Here they have a door of their own.
            TextButton(onClick = onAbrirDiagnostico) {
                Text("Diagnostics")
            }
        }
    }
}
