package dev.servercontrolpanel.app.nav

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.DateRange
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.List
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Warning
import androidx.compose.ui.graphics.vector.ImageVector
import dev.servercontrolpanel.designsystem.PanelIcons

internal enum class ParentPage(
    val id: String,
    val title: String,
    val icon: ImageVector,
    val iconDescription: String,
) {
    Home("home", "Home", Icons.Filled.Home, "Home"),
    System("system", "System", PanelIcons.Cpu, "System"),
    Docker("docker", "Docker", PanelIcons.Box, "Docker"),
    Dev("dev", "Dev", PanelIcons.Terminal, "Development"),
    Security("security", "Security", PanelIcons.Shield, "Security"),
    Apps("apps", "Apps", PanelIcons.Chat, "Apps"),
    Operations("operations", "Operations", PanelIcons.Board, "Operations"),
    Settings("config", "Settings", Icons.Filled.Settings, "Settings"),
}

internal sealed interface ChildDestination {
    data class Native(val route: String) : ChildDestination
    data class Sdui(val sectionId: String) : ChildDestination
}

internal data class ChildPage(
    val title: String,
    val icon: ImageVector,
    val destination: ChildDestination,
)

internal val HIDDEN_SDUI_CHILDREN = setOf("jira.issues")

internal fun childrenOf(parent: ParentPage): List<ChildPage> = when (parent) {
    ParentPage.Home, ParentPage.Settings -> emptyList()

    ParentPage.System -> listOf(
        ChildPage("Metrics", PanelIcons.Speedometer, ChildDestination.Sdui("system.metrics")),
        ChildPage("History", PanelIcons.Chart, ChildDestination.Sdui("system.history")),
        ChildPage("Alerts", Icons.Filled.Warning, ChildDestination.Sdui("alerts.rules")),
        ChildPage("Processes", PanelIcons.Cpu, ChildDestination.Sdui("system.processes")),
        ChildPage("Ports", PanelIcons.Plug, ChildDestination.Sdui("system.ports")),
        ChildPage("Services", Icons.Filled.Build, ChildDestination.Sdui("system.systemd")),
        ChildPage("Files", PanelIcons.Folder, ChildDestination.Native(ROUTE_FILES)),
    )

    ParentPage.Docker -> listOf(
        ChildPage("Containers", PanelIcons.Box, ChildDestination.Sdui("docker.containers")),
        ChildPage("Compose", PanelIcons.Layers, ChildDestination.Sdui("docker.compose")),
        ChildPage("Images", PanelIcons.Disk, ChildDestination.Sdui("docker.images")),
        ChildPage("Volumes", PanelIcons.Disk, ChildDestination.Sdui("docker.volumes")),
        ChildPage("Networks", PanelIcons.Globe, ChildDestination.Sdui("docker.networks")),
        ChildPage("Cleanup", PanelIcons.Swap, ChildDestination.Sdui("docker.prune")),
    )

    ParentPage.Dev -> listOf(
        ChildPage("Terminal", PanelIcons.Terminal, ChildDestination.Native(ROUTE_TERMINAL)),
        ChildPage("AI models", Icons.Filled.Settings, ChildDestination.Sdui("ai.settings")),
    )

    ParentPage.Security -> listOf(
        ChildPage("Audit log", Icons.Filled.List, ChildDestination.Sdui("security.audit")),
        ChildPage("Users", Icons.Filled.Person, ChildDestination.Sdui("security.users")),
        ChildPage("Vault", PanelIcons.Key, ChildDestination.Sdui("security.secrets")),
        ChildPage("Sessions", PanelIcons.ActiveSession, ChildDestination.Sdui("security.sessions")),
        ChildPage("Firewall", PanelIcons.Shield, ChildDestination.Sdui("security.ufw")),
        ChildPage("DNS", PanelIcons.Globe, ChildDestination.Sdui("security.adguard")),
        ChildPage("Devices", PanelIcons.Phone, ChildDestination.Sdui("security.devices")),
        ChildPage("Network usage", PanelIcons.Chart, ChildDestination.Sdui("security.savings")),
        ChildPage("This device", Icons.Filled.Lock, ChildDestination.Native(ROUTE_SECURITY)),
    )

    ParentPage.Apps -> listOf(
        ChildPage("WhatsApp", PanelIcons.Chat, ChildDestination.Native(ROUTE_WHATSAPP)),
        ChildPage("Call", PanelIcons.Videocam, ChildDestination.Native(ROUTE_CALL)),
    )

    ParentPage.Operations -> listOf(
        ChildPage("Tasks", PanelIcons.Board, ChildDestination.Native(ROUTE_JIRA)),
        ChildPage("Job queue", Icons.Filled.List, ChildDestination.Sdui("queue.jobs")),
        ChildPage("Scheduler", Icons.Filled.DateRange, ChildDestination.Sdui("scheduler.jobs")),
        ChildPage("Deploy", PanelIcons.Delivery, ChildDestination.Sdui("deploy.apps")),
    )
}

internal fun parentOfSection(sectionId: String): ParentPage = when (sectionId.substringBefore('.')) {
    "system", "alerts" -> ParentPage.System
    "docker" -> ParentPage.Docker
    "security" -> ParentPage.Security
    "ai" -> ParentPage.Dev
    else -> ParentPage.Operations
}

internal val UNKNOWN_SECTION_ICON: ImageVector = Icons.Filled.Info

internal val NOTIFICATIONS_ICON: ImageVector = Icons.Filled.Notifications
