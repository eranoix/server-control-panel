package dev.servercontrolpanel.feature.auth

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
import dev.servercontrolpanel.data.dashboard.DashboardSnapshot
import dev.servercontrolpanel.data.dashboard.DashboardTarget
import dev.servercontrolpanel.data.dashboard.HealthEntry
import dev.servercontrolpanel.data.dashboard.ResourceSignal
import dev.servercontrolpanel.data.dashboard.Severity
import dev.servercontrolpanel.designsystem.StatusColorPair
import dev.servercontrolpanel.designsystem.panelStatusColors

@Composable
internal fun colorsFor(severity: Severity): StatusColorPair {
    val colors = panelStatusColors
    return when (severity) {
        Severity.OK -> colors.ok
        Severity.WARNING -> colors.warning
        Severity.CRITICAL -> colors.critical
    }
}

internal fun labelFor(severity: Severity): String = when (severity) {
    Severity.OK -> "ok"
    Severity.WARNING -> "WARNING"
    Severity.CRITICAL -> "CRITICAL"
}

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

@Composable
internal fun AttentionCard(
    signals: List<ResourceSignal>,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    val worst = signals.maxOfOrNull { it.severity } ?: Severity.WARNING
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
                    text = attentionTitle(signals.size),
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

internal fun attentionTitle(count: Int): String =
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

@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun HealthCard(
    entries: List<HealthEntry>,
    quietLine: String?,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    var showHealthy by remember { mutableStateOf(false) }
    val offNominal = entries.filter { it.severity != Severity.OK }
    val healthy = entries.filter { it.severity == Severity.OK }
    val summary = when {
        entries.isEmpty() -> "no subsystems reported"
        offNominal.isEmpty() -> "${healthy.size} subsystems · all ok"
        else -> "${healthy.size} ok · ${offNominal.size} out of normal"
    }

    DashboardCard(
        title = "Server health",
        subtitle = summary,
        modifier = modifier,
    ) {
        if (quietLine != null) {
            Text(
                text = quietLine,
                style = MaterialTheme.typography.bodyMedium,
                color = panelStatusColors.ok.accent,
            )
        }
        offNominal.forEach { entry ->
            NavigableRow(onClick = { onTarget(DashboardTarget.SERVICES) }) {
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
        if (healthy.isNotEmpty()) {
            OutlinedButton(onClick = { showHealthy = !showHealthy }) {
                Icon(
                    imageVector = if (showHealthy) Icons.Filled.KeyboardArrowUp else Icons.Filled.KeyboardArrowDown,
                    contentDescription = null,
                    modifier = Modifier.size(18.dp),
                )
                Spacer(Modifier.width(6.dp))
                Text(
                    text = if (showHealthy) {
                        "hide the ${healthy.size} healthy ones"
                    } else {
                        "show the ${healthy.size} healthy ones"
                    },
                    style = MaterialTheme.typography.labelLarge,
                )
            }
            if (showHealthy) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    healthy.forEach { entry ->
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

@Composable
internal fun ResourcesCard(
    signals: List<ResourceSignal>,
    onTarget: (DashboardTarget) -> Unit,
    modifier: Modifier = Modifier,
) {
    val worstCount = signals.count { it.severity != Severity.OK }
    DashboardCard(
        title = "Resources",
        subtitle = if (worstCount == 0) "all within thresholds" else "$worstCount over threshold",
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
    val highlight = signal.severity != Severity.OK
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .background(
                color = if (highlight) colors.container else Color.Transparent,
                shape = RoundedCornerShape(8.dp),
            )
            .padding(horizontal = if (highlight) 10.dp else 0.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Box(
            modifier = Modifier
                .width(3.dp)
                .height(28.dp)
                .background(
                    color = if (highlight) colors.accent else MaterialTheme.colorScheme.outlineVariant,
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
                    fontWeight = if (highlight) FontWeight.SemiBold else FontWeight.Normal,
                    color = if (highlight) colors.content else MaterialTheme.colorScheme.onSurface,
                )
                if (highlight) SeverityTag(signal.severity)
            }
            Text(
                text = signal.detail,
                style = MaterialTheme.typography.bodySmall,
                color = if (highlight) colors.content else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (signal.headline.isNotBlank()) {
            Text(
                text = signal.headline,
                style = MaterialTheme.typography.titleMedium,
                fontFamily = FontFamily.Monospace,
                fontWeight = FontWeight.Bold,
                color = if (highlight) colors.accent else MaterialTheme.colorScheme.onSurface,
            )
        }
    }
}

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

@Composable
internal fun SessionCard(
    snapshot: DashboardSnapshot,
    driftText: String?,
    modifier: Modifier = Modifier,
    onOpenSecurity: () -> Unit = {},
    onOpenDiagnostics: () -> Unit = {},
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
                color = panelStatusColors.warning.accent,
            )
        }
        Row {
            TextButton(onClick = onOpenSecurity) {
                Text("Lock and screenshots")
            }
            TextButton(onClick = onOpenDiagnostics) {
                Text("Diagnostics")
            }
        }
    }
}
