package dev.servercontrolpanel.feature.auth.dashboard

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.DateRange
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Settings
import androidx.compose.ui.graphics.vector.ImageVector
import dev.servercontrolpanel.designsystem.PanelIcons

/**
 * A dashboard tile's icon, shown beside (never instead of) its label, so it
 * needs no screen reader description.
 *
 * Matched by tile id (`cpu`, `memoria`, `disco:/mnt/x`, aggregates); disk ids
 * match by prefix since there is one tile per mount point.
 */
internal fun tileIcon(id: String): ImageVector = when {
    id == "cpu" || id == "steal" || id == "iowait" -> PanelIcons.Speedometer
    id == "memoria" -> PanelIcons.Memory
    id == "swap" -> PanelIcons.Swap
    id.startsWith("disco") -> PanelIcons.Disk
    id == "rede" -> PanelIcons.Swap
    id == TILE_QUEUE -> Icons.Filled.List
    id == TILE_HEALTH -> PanelIcons.Health
    id == TILE_DEPLOYS -> PanelIcons.Delivery
    id == TILE_SCHEDULED -> Icons.Filled.DateRange
    id == TILE_UPTIME -> PanelIcons.Stopwatch
    id.startsWith("alerta:") -> Icons.Filled.Build
    else -> Icons.Filled.Settings
}
