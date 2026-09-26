package com.vpsmanager.app.nav

import androidx.compose.ui.graphics.vector.ImageVector

// --- leaf-screen routes -------------------------------------------------------
//
// Constants and not literals scattered around: they show up in three
// places (the registration in [AppNavHost], the map of parent pages and
// the tests), and one of them typed wrong by hand would only appear as
// "nothing happens when you tap".

internal const val ROUTE_TERMINAL = "terminal"
internal const val ROUTE_FILES = "arquivos"
internal const val ROUTE_JIRA = "jira"
internal const val ROUTE_WHATSAPP = "whatsapp"
internal const val ROUTE_CALL = "chamada"
internal const val ROUTE_NOTIFICATIONS = "notificacoes"
internal const val ROUTE_LICENSES = "licencas"
internal const val ROUTE_SETTINGS = "configuracoes"

/** The route of a parent page's grid. */
internal fun parentRoute(id: String) = "mae/$id"

/**
 * The drawer's destinations: the panel's PARENT pages, and only those.
 *
 * ## What changed, and why
 *
 * The drawer listed nine loose screens (Terminal, Admin, Files, Jira,
 * Call, WhatsApp, Notifications, Home, Licences) while Administration
 * dumped thirty blocks into a single grid. That was two taxonomies for the
 * same product — and neither of them matched the web panel's. The symptom
 * was Jira showing up in two places at once.
 *
 * Now the drawer has the web's seven parents plus Settings, and each
 * parent opens a grid with its own children (see `MapaDoPainel.kt`). That
 * shortens the drawer, takes the leftover group headers out of it, and —
 * what matters most — gives every new screen a predictable place.
 *
 * [icon]/[iconDescription]: every icon carries its own description, so
 * that TalkBack announces "System" and not "unlabelled image".
 */
internal enum class AppDestination(
    val parent: ParentPage,
) {
    Home(ParentPage.Home),
    System(ParentPage.System),
    Docker(ParentPage.Docker),
    Dev(ParentPage.Dev),
    Security(ParentPage.Security),
    Apps(ParentPage.Apps),
    Operations(ParentPage.Operations),
    Settings(ParentPage.Settings),
    ;

    val label: String get() = parent.title
    val icon: ImageVector get() = parent.icon
    val iconDescription: String get() = parent.iconDescription

    /**
     * This destination's route.
     *
     * [Home] and [Settings] are real screens, not grids: the first is
     * the panel for whoever has just signed in, the second is the device's
     * list of settings. The rest open the grid of their children.
     */
    val route: String
        get() = when (this) {
            Home -> "home"
            Settings -> ROUTE_SETTINGS
            else -> parentRoute(parent.id)
        }

    /** The route the item actually navigates to. */
    val navigationTarget: String get() = route

    /**
     * Whether [currentRoute] belongs to this destination.
     *
     * A parent matches its own grid, and not the child screens: someone inside
     * "Containers" is on a Docker screen, but the drawer highlighted on Docker
     * while the header says "Containers" would have the drawer claiming two
     * different things at the same time.
     */
    fun matches(currentRoute: String?): Boolean = currentRoute == route
}
