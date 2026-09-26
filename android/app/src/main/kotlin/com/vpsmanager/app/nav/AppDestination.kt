package com.vpsmanager.app.nav

import androidx.compose.ui.graphics.vector.ImageVector

// Leaf screen routes. Constants because they are used by the NavHost, the parent page map and
// the tests; a typo would only show up as a tap that does nothing.

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
 * The drawer's destinations: the web panel's parent pages plus Settings, so app and web share
 * one taxonomy. Each parent opens a grid of its children (see `PanelMap.kt`).
 * Every icon has its own description so TalkBack announces the name, not "unlabelled image".
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

    /** This destination's route. [Home] and [Settings] are real screens; the rest open their grid. */
    val route: String
        get() = when (this) {
            Home -> "home"
            Settings -> ROUTE_SETTINGS
            else -> parentRoute(parent.id)
        }

    /** The route the item actually navigates to. */
    val navigationTarget: String get() = route

    /**
     * Whether [currentRoute] belongs to this destination. A parent matches only its own grid,
     * never its child screens, so the drawer highlight never contradicts the header.
     */
    fun matches(currentRoute: String?): Boolean = currentRoute == route
}
