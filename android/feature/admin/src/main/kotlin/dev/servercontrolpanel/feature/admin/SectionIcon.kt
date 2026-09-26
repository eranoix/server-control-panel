package dev.servercontrolpanel.feature.admin

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.DateRange
import androidx.compose.material.icons.filled.Email
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Warning
import androidx.compose.ui.graphics.vector.ImageVector
import dev.servercontrolpanel.designsystem.PanelIcons

/**
 * The icon for an Administration section.
 *
 * Chosen from the server's section id (`group.name`), never the label, which
 * may be reworded. Matches go from most to least specific, so `docker.images`
 * hits "image" before "docker". Unknown sections get a generic cog, so new
 * server sections still work without an app release.
 */
internal fun sectionIcon(sectionId: String): ImageVector {
    val id = sectionId.lowercase()
    return when {
        // Docker, most specific first: image, container and compose each get a distinct icon.
        "image" in id -> PanelIcons.Layers
        "volume" in id -> PanelIcons.Disk
        "network" in id -> PanelIcons.Swap
        "compose" in id -> Icons.Filled.Build
        "prune" in id || "cleanup" in id -> Icons.Filled.Refresh
        "docker" in id || "container" in id -> PanelIcons.Box

        "metric" in id -> PanelIcons.Speedometer
        // Processes get their own icon, distinct from metrics.
        "process" in id -> PanelIcons.Cpu
        "memor" in id || "ram" in id -> PanelIcons.Memory
        "disk" in id || "storage" in id -> PanelIcons.Disk
        "systemd" in id || "unit" in id || "servic" in id -> Icons.Filled.Build
        // Ports get a plug, distinct from the network's arrows.
        "port" in id -> PanelIcons.Plug
        "histor" in id -> PanelIcons.Stopwatch

        // Security: a distinct icon per concern (shield for firewall, key for
        // secrets, person for accounts, phone for devices, monitor for sessions).
        "firewall" in id || "ufw" in id -> PanelIcons.Shield
        "secret" in id || "vault" in id -> PanelIcons.Key
        "user" in id -> Icons.Filled.Person
        "session" in id -> PanelIcons.ActiveSession
        "audit" in id -> Icons.Filled.Warning
        "device" in id -> PanelIcons.Phone
        "adguard" in id || "dns" in id -> PanelIcons.Globe
        "netusage" in id || "network usage" in id || "usage" in id -> PanelIcons.Chart
        "securit" in id -> Icons.Filled.Lock

        "deploy" in id || "delivery" in id -> PanelIcons.Delivery
        "queue" in id -> Icons.Filled.List
        "schedul" in id || "cron" in id -> Icons.Filled.DateRange
        "backup" in id -> PanelIcons.Disk
        "job" in id -> Icons.Filled.PlayArrow
        "alert" in id -> Icons.Filled.Notifications
        "health" in id -> PanelIcons.Health

        "whatsapp" in id || "chat" in id -> PanelIcons.Chat
        "mail" in id || "email" in id || "gmail" in id -> Icons.Filled.Email
        "jira" in id -> Icons.Filled.Info
        "terminal" in id || "shell" in id -> PanelIcons.Terminal
        "file" in id -> PanelIcons.Folder

        else -> Icons.Filled.Settings
    }
}
