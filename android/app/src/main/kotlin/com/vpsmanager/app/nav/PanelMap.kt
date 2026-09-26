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
 * The panel's map: the parent pages and each one's children.
 *
 * ## Why it exists, and why it copies the web literally
 *
 * The drawer listed EIGHT loose screens and Administration dumped thirty
 * blocks into a single grid — two different taxonomies for the same
 * product, and neither of them matching the web panel's. The result was
 * Jira showing up in two places (a "Jira · Integrations" block in the grid
 * and a "Jira" destination in the drawer), which is the underlying
 * symptom: without a single taxonomy, every new screen picks its own home.
 *
 * The taxonomy here is the SAME as the web panel's, taken from `PAGE_REMAP`
 * (`00-shell.js`): seven parent pages — Home, System, Docker, Dev,
 * Security, Apps, Operations — plus Settings. Whoever uses both finds the
 * same thing in the same place, and whoever writes a new screen does not
 * have to invent: there is already a parent for it.
 *
 * ## Where Jira lives, and why the duplicate went away
 *
 * On the web, the Jira board is `operations/tarefas`. Here too: it is a
 * child of Operations, and points at the NATIVE screen. The SDUI section
 * `jira.issues` (the old table) stops being offered by
 * [HIDDEN_SDUI_CHILDREN] — the server keeps serving it to the apps that do
 * not have the native board, and this one simply does not list it.
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
 * A child: where it leads.
 *
 * [Native] is a screen of this app (an [AppNavHost] route); [Sdui] is a
 * section described by the server. The distinction exists in the
 * destination, never in the design — whoever looks at the grid sees
 * identical blocks, because to whoever operates it makes no difference at
 * all on which side the screen was written.
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
 * SDUI sections the app does NOT list, because it has a screen of its own
 * for them.
 *
 * `jira.issues` is the issue table the server serves to app versions older
 * than the native board. Keeping it on offer here would put two Jiras side
 * by side — a good one and a threadbare one — and the person would
 * discover the difference by trial and error.
 */
internal val HIDDEN_SDUI_CHILDREN = setOf("jira.issues")

/**
 * The children of each parent, in the order they appear.
 *
 * The order within a group is the web panel's, and not alphabetical: the
 * first of each parent is its most used one, which is the one the thumb
 * reaches first.
 *
 * An SDUI child the server does not offer (by permission or by version)
 * disappears from the grid — whoever builds the screen crosses this list
 * with the real catalogue. That way a new section on the server does NOT
 * require a new app version to be reachable: it shows up under the parent
 * whose prefix it carries.
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
        // On the web this is called "Tasks", and it is the Jira board. The name
        // that shows is the web's; the destination is the native screen.
        ChildPage("Tasks", VpsmIcons.Board, ChildDestination.Native(ROUTE_JIRA)),
        ChildPage("Job queue", Icons.Filled.List, ChildDestination.Sdui("queue.jobs")),
        ChildPage("Scheduler", Icons.Filled.DateRange, ChildDestination.Sdui("scheduler.jobs")),
        ChildPage("Deploy", VpsmIcons.Delivery, ChildDestination.Sdui("deploy.apps")),
    )
}

/**
 * The parent an SDUI section belongs to, by the PREFIX of its id.
 *
 * This is what makes a new section from the server show up with no new app
 * version — the whole SDUI promise. `docker.anything_at_all` lands in
 * Docker even if this version has never heard of it; whatever matches no
 * known prefix lands in Operations, which is where the long tail of
 * administration lives.
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
