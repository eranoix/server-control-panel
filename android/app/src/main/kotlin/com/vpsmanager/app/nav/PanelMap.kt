package com.vpsmanager.app.nav

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
import com.vpsmanager.designsystem.VpsmIcons

/**
 * The panel's map: the parent pages and their children. It copies the web panel's taxonomy
 * (`PAGE_REMAP` in `00-shell.js`) so both find things in the same place and every new screen
 * has an obvious parent. The Jira board is a native child of Operations, as on the web.
 */
internal enum class ParentPage(
    val id: String,
    val title: String,
    val icon: ImageVector,
    val iconDescription: String,
) {
    Home("inicio", "Home", Icons.Filled.Home, "Home"),
    System("sistema", "System", VpsmIcons.Cpu, "System"),
    Docker("docker", "Docker", VpsmIcons.Box, "Docker"),
    Dev("dev", "Dev", VpsmIcons.Terminal, "Development"),
    Security("seguranca", "Security", VpsmIcons.Shield, "Security"),
    Apps("apps", "Apps", VpsmIcons.Chat, "Apps"),
    Operations("operacoes", "Operations", VpsmIcons.Board, "Operations"),
    Settings("config", "Settings", Icons.Filled.Settings, "Settings"),
}

/**
 * Where a child leads: [Native] is an [AppNavHost] route, [Sdui] a server-described section.
 * Both look identical in the grid.
 */
internal sealed interface ChildDestination {
    data class Native(val route: String) : ChildDestination
    data class Sdui(val sectionId: String) : ChildDestination
}

/** A child page, as it appears in the parent's grid. */
internal data class ChildPage(
    val title: String,
    val icon: ImageVector,
    val destination: ChildDestination,
)

/**
 * SDUI sections the app does not list because it has its own screen for them. `jira.issues`
 * is still served for older app versions without the native board.
 */
internal val HIDDEN_SDUI_CHILDREN = setOf("jira.issues")

/**
 * The children of each parent, in the web panel's order (most used first, not alphabetical).
 * The screen crosses this list with the server catalogue, so SDUI children the server does not
 * offer disappear, and new server sections appear under their prefix's parent.
 */
internal fun childrenOf(parent: ParentPage): List<ChildPage> = when (parent) {
    ParentPage.Home, ParentPage.Settings -> emptyList()

    ParentPage.System -> listOf(
        ChildPage("Metrics", VpsmIcons.Speedometer, ChildDestination.Sdui("system.metrics")),
        ChildPage("History", VpsmIcons.Chart, ChildDestination.Sdui("system.history")),
        ChildPage("Alerts", Icons.Filled.Warning, ChildDestination.Sdui("alerts.rules")),
        ChildPage("Processes", VpsmIcons.Cpu, ChildDestination.Sdui("system.processes")),
        ChildPage("Ports", VpsmIcons.Plug, ChildDestination.Sdui("system.ports")),
        ChildPage("Services", Icons.Filled.Build, ChildDestination.Sdui("system.systemd")),
        ChildPage("Files", VpsmIcons.Folder, ChildDestination.Native(ROUTE_FILES)),
    )

    ParentPage.Docker -> listOf(
        ChildPage("Containers", VpsmIcons.Box, ChildDestination.Sdui("docker.containers")),
        ChildPage("Compose", VpsmIcons.Layers, ChildDestination.Sdui("docker.compose")),
        ChildPage("Images", VpsmIcons.Disk, ChildDestination.Sdui("docker.images")),
        ChildPage("Volumes", VpsmIcons.Disk, ChildDestination.Sdui("docker.volumes")),
        ChildPage("Networks", VpsmIcons.Globe, ChildDestination.Sdui("docker.networks")),
        ChildPage("Cleanup", VpsmIcons.Swap, ChildDestination.Sdui("docker.prune")),
    )

    ParentPage.Dev -> listOf(
        ChildPage("Terminal", VpsmIcons.Terminal, ChildDestination.Native(ROUTE_TERMINAL)),
        ChildPage("AI models", Icons.Filled.Settings, ChildDestination.Sdui("ai.settings")),
    )

    ParentPage.Security -> listOf(
        ChildPage("Audit log", Icons.Filled.List, ChildDestination.Sdui("security.audit")),
        ChildPage("Users", Icons.Filled.Person, ChildDestination.Sdui("security.users")),
        ChildPage("Vault", VpsmIcons.Key, ChildDestination.Sdui("security.secrets")),
        ChildPage("Sessions", VpsmIcons.ActiveSession, ChildDestination.Sdui("security.sessions")),
        ChildPage("Firewall", VpsmIcons.Shield, ChildDestination.Sdui("security.ufw")),
        ChildPage("DNS", VpsmIcons.Globe, ChildDestination.Sdui("security.adguard")),
        ChildPage("Devices", VpsmIcons.Phone, ChildDestination.Sdui("security.devices")),
        ChildPage("Network usage", VpsmIcons.Chart, ChildDestination.Sdui("security.economia")),
        // From the DEVICE, not the server: app lock and protected screen.
        ChildPage("This device", Icons.Filled.Lock, ChildDestination.Native(ROUTE_SECURITY)),
    )

    ParentPage.Apps -> listOf(
        ChildPage("WhatsApp", VpsmIcons.Chat, ChildDestination.Native(ROUTE_WHATSAPP)),
        ChildPage("Call", VpsmIcons.Videocam, ChildDestination.Native(ROUTE_CALL)),
    )

    ParentPage.Operations -> listOf(
        // Named "Tasks" as on the web; it opens the native Jira board.
        ChildPage("Tasks", VpsmIcons.Board, ChildDestination.Native(ROUTE_JIRA)),
        ChildPage("Job queue", Icons.Filled.List, ChildDestination.Sdui("queue.jobs")),
        ChildPage("Scheduler", Icons.Filled.DateRange, ChildDestination.Sdui("scheduler.jobs")),
        ChildPage("Deploy", VpsmIcons.Delivery, ChildDestination.Sdui("deploy.apps")),
    )
}

/**
 * The parent an SDUI section belongs to, by its id prefix, so unknown new sections still land
 * in the right place. Unknown prefixes go to Operations.
 */
internal fun parentOfSection(sectionId: String): ParentPage = when (sectionId.substringBefore('.')) {
    "system", "alerts" -> ParentPage.System
    "docker" -> ParentPage.Docker
    "security" -> ParentPage.Security
    "ai" -> ParentPage.Dev
    else -> ParentPage.Operations
}

/** Icon for a section this app does not know by name. */
internal val UNKNOWN_SECTION_ICON: ImageVector = Icons.Filled.Info

/** The notification icon, used on the Settings page. */
internal val NOTIFICATIONS_ICON: ImageVector = Icons.Filled.Notifications
