package dev.servercontrolpanel.app.nav

import androidx.compose.ui.graphics.vector.ImageVector

internal const val ROUTE_TERMINAL = "terminal"
internal const val ROUTE_FILES = "files"
internal const val ROUTE_JIRA = "jira"
internal const val ROUTE_WHATSAPP = "whatsapp"
internal const val ROUTE_CALL = "call"
internal const val ROUTE_NOTIFICATIONS = "notifications"
internal const val ROUTE_LICENSES = "licenses"
internal const val ROUTE_SETTINGS = "settings"

internal fun parentRoute(id: String) = "mae/$id"

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

    val route: String
        get() = when (this) {
            Home -> "home"
            Settings -> ROUTE_SETTINGS
            else -> parentRoute(parent.id)
        }

    val navigationTarget: String get() = route

    fun matches(currentRoute: String?): Boolean = currentRoute == route
}
