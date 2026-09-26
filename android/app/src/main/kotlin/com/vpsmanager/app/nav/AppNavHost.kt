package com.vpsmanager.app.nav

import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import com.vpsmanager.data.update.ResumePoint
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.vpsmanager.app.storage.StorageScreen
import com.vpsmanager.data.storage.AppStorage
import com.vpsmanager.data.storage.DeviceStorageAreas
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.vpsmanager.designsystem.ThemeMode
import com.vpsmanager.app.BuildConfig
import com.vpsmanager.app.DiagnosticsScreen
import com.vpsmanager.app.licenses.OssLicensesScreen
import com.vpsmanager.app.offline.OfflineBanner
import com.vpsmanager.core.shell.TerminalBridge
import com.vpsmanager.feature.auth.dashboard.ScrollToTopRequest
import com.vpsmanager.app.update.UpdateBanner
import com.vpsmanager.data.update.UpdateRecovery
import com.vpsmanager.data.update.UpdateState
import com.vpsmanager.feature.admin.AdminScreen
import com.vpsmanager.feature.auth.HomeScreen
import com.vpsmanager.feature.auth.security.SecurityScreen
import com.vpsmanager.feature.jira.JiraBoardRoute
import com.vpsmanager.feature.files.browse.FileBrowserScreen
import com.vpsmanager.feature.files.editor.FileEditorScreen
import com.vpsmanager.feature.notifications.fcm.NotificationDeepLink
import com.vpsmanager.feature.notifications.fcm.PushOnboarding
import com.vpsmanager.feature.notifications.prefs.NotificationPreferencesRoute
import com.vpsmanager.feature.terminal.ui.SessionListScreen
import com.vpsmanager.feature.terminal.ui.SessionListViewModel
import com.vpsmanager.feature.terminal.ui.TERMINAL_SESSION_NAME_ARG
import com.vpsmanager.feature.terminal.ui.TerminalRoute
import com.vpsmanager.feature.videocall.CallScreen
import com.vpsmanager.feature.videocall.RoomLobbyScreen
import com.vpsmanager.feature.whatsapp.WhatsAppRoute
import com.vpsmanager.sdui.registry.LocalUpdateRequest
import com.vpsmanager.sdui.debug.PayloadPreviewScreen
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import java.net.URLDecoder
import java.net.URLEncoder

/** Nested detail route below the Terminal list: one live session per session name. */
private fun terminalSessionRoute(name: String) = "terminal/$name"

/** Nav argument name carrying the joined room's id. */
private const val VIDEOCALL_ROOM_ID_ARG = "roomId"

/** Nested detail route below the call lobby: one active call per room. */
private fun videocallRoomRoute(roomId: String) = "chamada/$roomId"

/** Nav argument name carrying the tapped file's server path, URL-encoded (paths contain `/`). */
private const val FILE_EDITOR_PATH_ARG = "path"

/** Nested detail route below the file browser: one file editor per opened path. */
private fun fileEditorRoute(path: String) = "arquivos/edit/${URLEncoder.encode(path, "UTF-8")}"

/** Nav argument name carrying the opaque SDUI section id; see [AdminScreen]. */
private const val ADMIN_SECTION_ID_ARG = "sectionId"

/**
 * The single route every SDUI section renders through, so a new section only needs a
 * screen descriptor on the server, not an app release.
 */
internal fun adminSectionRoute(sectionId: String) = "admin/$sectionId"

/** Section that notification deep links open. */
internal const val SCHEDULER_SECTION_ID = "scheduler.jobs"

/** Debug-only route for [PayloadPreviewScreen], registered only under `BuildConfig.DEBUG`. */
private const val DEBUG_SDUI_PREVIEW_ROUTE = "debug/sdui-preview"

/**
 * Detail route for the diagnostic report. The update banner sends the user here when an
 * install fails, because the `PackageInstaller` message does not fit in a banner.
 */
internal const val DIAGNOSTICS_ROUTE = "diagnostico"
internal const val ROUTE_STORAGE = "armazenamento"

/**
 * A tapped notification's deep link, resolved to a concrete [AppNavHost] route.
 * [entityId] is carried opaquely and never interpolated into a route, so a hostile launch
 * Intent cannot steer navigation beyond the fixed [navRoute].
 */
internal data class ResolvedNotificationDeepLink(val navRoute: String, val entityId: String?)

/**
 * Maps the raw [NotificationDeepLink.EXTRA_ROUTE] value to one of [AppNavHost]'s own routes.
 * [route] comes from a launch Intent any app can send to the exported activity, so it is
 * untrusted: anything but the exact tokens the notification builder emits resolves to `null`,
 * which callers must treat as "navigate nowhere".
 */
internal fun resolveNotificationDeepLink(route: String?, entityId: String?): ResolvedNotificationDeepLink? =
    when (route) {
        NotificationDeepLink.ROUTE_DEPLOY_JOB, NotificationDeepLink.ROUTE_ALERT ->
            ResolvedNotificationDeepLink(navRoute = adminSectionRoute(SCHEDULER_SECTION_ID), entityId = entityId)
        else -> null
    }

/**
 * Resolves the room id of a call answered from the lock screen
 * ([com.vpsmanager.feature.videocall.call.VpsmConnection.onAnswer]) through the same
 * single-consumption deep link path as notifications. `roomId` is untrusted and only ever
 * placed in this one fixed route shape.
 */
internal fun resolveVideocallDeepLink(roomId: String?): ResolvedNotificationDeepLink? =
    roomId?.let { ResolvedNotificationDeepLink(navRoute = videocallRoomRoute(it), entityId = null) }

/**
 * The single way to navigate to a work screen from the shell (drawer, dock shortcut, parent grid).
 * `popUpTo(start)` pops any open terminal first; otherwise two terminal destinations stay on the
 * stack, each replaying the scrollback over the other. `saveState`/`restoreState` keep each
 * screen's position when coming back.
 */
private fun NavHostController.goToWorkScreen(route: String) {
    navigate(route) {
        graph.startDestinationRoute?.let { start ->
            popUpTo(start) { saveState = true }
        }
        launchSingleTop = true
        restoreState = true
    }
}

/**
 * The app's navigable shell: a [ModalNavigationDrawer] with the grouped destinations (see
 * [AppDrawerSheet]) and the [NavHost] that swaps the content. A drawer is used because eight
 * destinations do not fit a Material 3 bottom bar.
 *
 * One header per screen: the shell's [TopAppBar] (hamburger, never "back") appears only on
 * drawer destinations. Detail screens bring their own bar with "back" (see [ChildScreen]) or
 * are full-screen, so headers never stack.
 *
 * Window insets are applied exactly once, on the [NavHost]'s modifier.
 *
 * [pendingDeepLinkRoute] is a route already resolved by [resolveNotificationDeepLink] or
 * [resolveVideocallDeepLink]; it is navigated once and then [onDeepLinkConsumed] is called so
 * the owner clears it, otherwise every recomposition would navigate again.
 *
 * [onSignOut] ends the session for real (see `MainActivity`); it is a parameter so UI tests
 * can observe it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AppNavHost(
    pendingDeepLinkRoute: String? = null,
    onDeepLinkConsumed: () -> Unit = {},
    onSignOut: () -> Unit = {},
    updateState: UpdateState = UpdateState.Idle,
    onUpdateClick: () -> Unit = {},
    onUpdateCancel: () -> Unit = {},
    onUpdateRecovery: (UpdateRecovery) -> Unit = {},
    updateDiagnostics: String? = null,
    onClearUpdateDiagnostics: () -> Unit = {},
    installedVersion: String? = null,
    onCheckForUpdate: () -> Unit = {},
    // Appearance is a device preference: `MainActivity` owns its persistence.
    themeMode: ThemeMode = ThemeMode.DEFAULT,
    onThemeModeChange: (ThemeMode) -> Unit = {},
    navController: NavHostController = rememberNavController(),
) {
    val drawerState = rememberDrawerState(initialValue = DrawerValue.Closed)
    val scope = rememberCoroutineScope()

    LaunchedEffect(pendingDeepLinkRoute) {
        val route = pendingDeepLinkRoute ?: return@LaunchedEffect
        // Wait for the graph: on first composition this runs before the NavHost sets it, and
        // navigating would throw "Navigation graph has not been set". The first emission of
        // `currentBackStackEntryFlow` means the graph exists.
        navController.currentBackStackEntryFlow.first()
        navController.navigate(route) { launchSingleTop = true }
        onDeepLinkConsumed()
    }

    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route

    // Updating kills the process, so the current route is saved to reopen it afterwards.
    // It is recorded here because the update coordinator knows nothing about navigation.
    val context = LocalContext.current
    val resumePoint = remember(context) { ResumePoint(context.applicationContext) }
    val onRefresh: () -> Unit = {
        resumePoint.save(currentRoute)
        onUpdateClick()
    }
    // Each parent has a concrete route, so the route alone identifies the destination.
    val currentDestination = AppDestination.entries.firstOrNull { it.matches(currentRoute) }

    ModalNavigationDrawer(
        drawerState = drawerState,
        // Edge gesture left on (the default) so the drawer opens with a thumb.
        drawerContent = {
            AppDrawerSheet(
                currentRoute = currentRoute,
                onDestinationSelected = { destination ->
                    // Close before navigating so the drawer is not left open over the new screen.
                    scope.launch { drawerState.close() }
                    // Reselecting Home scrolls it to the top; otherwise `launchSingleTop`
                    // swallows the tap and the button seems broken.
                    if (destination == AppDestination.Home) ScrollToTopRequest.request()
                    navController.goToWorkScreen(destination.navigationTarget)
                },
                onSignOut = {
                    scope.launch { drawerState.close() }
                    onSignOut()
                },
                onShortcut = { route ->
                    scope.launch { drawerState.close() }
                    // Same options as the drawer; see goToWorkScreen.
                    navController.goToWorkScreen(route)
                },
            )
        },
    ) {
        Scaffold(
            topBar = {
                // Banners live in the `topBar` slot so the Scaffold's `innerPadding` already
                // accounts for their height. They are hidden on detail screens (terminal,
                // call, editor), where every dp belongs to the content.
                if (currentDestination != null) {
                    Column {
                        TopAppBar(
                            title = {
                                Text(
                                    text = currentDestination.label,
                                    maxLines = 1,
                                    overflow = TextOverflow.Clip,
                                )
                            },
                            navigationIcon = {
                                IconButton(onClick = { scope.launch { drawerState.open() } }) {
                                    Icon(
                                        imageVector = Icons.Filled.Menu,
                                        contentDescription = OPEN_DRAWER_DESCRIPTION,
                                    )
                                }
                            },
                            actions = {
                                // Same entry `currentDestination` came from, so actions and content stay in sync.
                                backStackEntry?.let { entry ->
                                    TopLevelActions(destination = currentDestination, entry = entry)
                                }
                            },
                        )
                        // Offline notice goes above the update banner: stale data matters more
                        // than an update that cannot be downloaded right now.
                        OfflineBanner()
                        UpdateBanner(
                            state = updateState,
                            onUpdateClick = onRefresh,
                            onCancelClick = onUpdateCancel,
                            onRecoveryClick = { recovery ->
                                if (recovery == UpdateRecovery.SHOW_DIAGNOSTICS) {
                                    navController.navigate(DIAGNOSTICS_ROUTE) { launchSingleTop = true }
                                } else {
                                    onUpdateRecovery(recovery)
                                }
                            },
                        )
                    }
                }
            },
        ) { innerPadding ->
            // Post-login onboarding (notification permission, battery exemption). It lives in
            // the shell because it belongs to no screen and this is the first point where the
            // user is known to be signed in.
            PushOnboarding()
            // Features never know routes, so the shell tells the terminal bridge how to open
            // the terminal.
            ConnectTerminalBridge(navController)
            // `NeedsUpdateCard` sits deep in server-described payloads, so the update action is
            // passed as a composition local instead of through every SDUI signature.
            CompositionLocalProvider(LocalUpdateRequest provides onRefresh) {
                NavHost(
                    navController = navController,
                    startDestination = AppDestination.Home.route,
                    modifier = Modifier
                        .padding(innerPadding)
                        .consumeWindowInsets(innerPadding)
                        .imePadding(),
                ) {
                    composable(AppDestination.Home.route) {
                        // Home emits destinations, never routes; mapping them to routes lives here.
                        HomeScreen(
                            onOpenSection = { sectionId ->
                                navController.navigate(adminSectionRoute(sectionId)) { launchSingleTop = true }
                            },
                            onOpenTerminal = {
                                navController.navigate(ROUTE_TERMINAL) { launchSingleTop = true }
                            },
                            onOpenSecurity = {
                                navController.navigate(ROUTE_SECURITY) { launchSingleTop = true }
                            },
                            onOpenDiagnostics = {
                                navController.navigate(DIAGNOSTICS_ROUTE) { launchSingleTop = true }
                            },
                        )
                    }
                    // One concrete route per parent. `launchSingleTop` and `restoreState`
                    // compare by destination, not by argument, so a single parameterised
                    // route would make the drawer unable to switch parents. It also gives each
                    // parent its own saved state.
                    ParentPage.entries
                        .filter { childrenOf(it).isNotEmpty() }
                        .forEach { parent ->
                            composable(parentRoute(parent.id)) {
                                ParentScreen(
                                    parent = parent,
                                    onOpenNative = { route ->
                                        navController.goToWorkScreen(route)
                                    },
                                    onOpenSdui = { sectionId ->
                                        navController.navigate(adminSectionRoute(sectionId)) {
                                            launchSingleTop = true
                                        }
                                    },
                                )
                            }
                        }

                    composable(ROUTE_SETTINGS) {
                        SettingsScreen(
                            themeMode = themeMode,
                            onThemeModeChange = onThemeModeChange,
                            installedVersion = installedVersion,
                            onCheckForUpdate = onCheckForUpdate,
                            onOpenNotifications = {
                                navController.navigate(ROUTE_NOTIFICATIONS) { launchSingleTop = true }
                            },
                            onOpenSecurity = {
                                navController.navigate(ROUTE_SECURITY) { launchSingleTop = true }
                            },
                            onOpenLicenses = {
                                navController.navigate(ROUTE_LICENSES) { launchSingleTop = true }
                            },
                            onOpenDiagnostics = {
                                navController.navigate(DIAGNOSTICS_ROUTE) { launchSingleTop = true }
                            },
                            onOpenStorage = {
                                navController.navigate(ROUTE_STORAGE) { launchSingleTop = true }
                            },
                        )
                    }

                    composable(ROUTE_TERMINAL) { entry ->
                        val contentViewModel: SessionListViewModel = viewModel(viewModelStoreOwner = entry)
                        ChildScreen(
                            title = TERMINAL_SHORTCUT_LABEL,
                            onBack = { navController.popBackStack() },
                            actions = {
                                TextButton(onClick = contentViewModel::refresh) { Text(REFRESH_ACTION_LABEL) }
                            },
                        ) {
                        SessionListScreen(
                            viewModel = contentViewModel,
                            // `launchSingleTop` prevents a second terminal for the same session,
                            // whose fresh attach would replay the scrollback over the first.
                            onSessionSelected = { name ->
                                navController.navigate(terminalSessionRoute(name)) { launchSingleTop = true }
                            },
                        )
                        }
                    }
                    composable(
                        route = "terminal/{$TERMINAL_SESSION_NAME_ARG}",
                        arguments = listOf(navArgument(TERMINAL_SESSION_NAME_ARG) { type = NavType.StringType }),
                    ) {
                        TerminalRoute(
                            onBack = { navController.popBackStack() },
                            // Switching sessions replaces the destination instead of stacking
                            // terminals, each holding a live grid in memory.
                            onSwitchSession = { name ->
                                navController.navigate(terminalSessionRoute(name)) {
                                    popUpTo(ROUTE_TERMINAL)
                                    launchSingleTop = true
                                }
                            },
                        )
                    }
                    // Every SDUI section renders here; see adminSectionRoute.
                    composable(
                        route = "admin/{sectionId}",
                        arguments = listOf(navArgument(ADMIN_SECTION_ID_ARG) { type = NavType.StringType }),
                    ) { entry ->
                        val sectionId = checkNotNull(entry.arguments?.getString(ADMIN_SECTION_ID_ARG)) {
                            "admin/{$ADMIN_SECTION_ID_ARG} route requires a '$ADMIN_SECTION_ID_ARG' argument"
                        }
                        AdminScreen(sectionId = sectionId)
                    }
                    composable(ROUTE_NOTIFICATIONS) {
                        ChildScreen(
                            title = "Notifications",
                            onBack = { navController.popBackStack() },
                        ) {
                            NotificationPreferencesRoute()
                        }
                    }
                    composable(ROUTE_CALL) {
                        RoomLobbyScreen(
                            onRoomSelected = { roomId -> navController.navigate(videocallRoomRoute(roomId)) },
                        )
                    }
                    composable(
                        route = "chamada/{$VIDEOCALL_ROOM_ID_ARG}",
                        arguments = listOf(navArgument(VIDEOCALL_ROOM_ID_ARG) { type = NavType.StringType }),
                    ) { entry ->
                        val roomId = checkNotNull(entry.arguments?.getString(VIDEOCALL_ROOM_ID_ARG)) {
                            "chamada/{$VIDEOCALL_ROOM_ID_ARG} route requires a '$VIDEOCALL_ROOM_ID_ARG' argument"
                        }
                        CallScreen(roomId = roomId, onLeaveCall = { navController.popBackStack() })
                    }
                    composable(ROUTE_JIRA) { JiraBoardRoute() }
                    composable(ROUTE_WHATSAPP) { WhatsAppRoute() }
                    composable(ROUTE_FILES) {
                        FileBrowserScreen(
                            onOpenFile = { path -> navController.navigate(fileEditorRoute(path)) },
                        )
                    }
                    composable(
                        route = "arquivos/edit/{$FILE_EDITOR_PATH_ARG}",
                        arguments = listOf(navArgument(FILE_EDITOR_PATH_ARG) { type = NavType.StringType }),
                    ) { entry ->
                        val encodedPath = checkNotNull(entry.arguments?.getString(FILE_EDITOR_PATH_ARG)) {
                            "arquivos/edit route requires a '$FILE_EDITOR_PATH_ARG' argument"
                        }
                        FileEditorScreen(
                            path = URLDecoder.decode(encodedPath, "UTF-8"),
                            onBack = { navController.popBackStack() },
                        )
                    }
                    composable(ROUTE_LICENSES) { OssLicensesScreen() }

                    // Measuring reads directories, so it runs on open and after each cleanup,
                    // never per frame. A keyed `remember` because the value is the screen.
                    composable(ROUTE_STORAGE) {
                        val context = LocalContext.current
                        var pass by remember { mutableIntStateOf(0) }
                        var last by remember { mutableStateOf<AppStorage.CleanupResult?>(null) }
                        val usages = remember(pass) { DeviceStorageAreas.measure(context) }
                        ChildScreen(
                            title = "Storage",
                            onBack = { navController.popBackStack() },
                        ) {
                            StorageScreen(
                                usages = usages,
                                lastResult = last,
                                onFreeSpace = {
                                    last = DeviceStorageAreas.clearAllRebuildable(context)
                                    pass++
                                },
                            )
                        }
                    }
                    // Not in the drawer: reached from Home's session card and from Settings.
                    composable(ROUTE_SECURITY) { SecurityScreen() }

                    // Detail screen, target of the update banner's "Diagnostics" button.
                    composable(DIAGNOSTICS_ROUTE) {
                        DiagnosticsScreen(
                            initFailures = emptyList(),
                            lastCrash = null,
                            updateFailures = updateDiagnostics,
                            onClear = {
                                onClearUpdateDiagnostics()
                                navController.popBackStack()
                            },
                        )
                    }

                    // Debug-only payload inspector. BuildConfig.DEBUG is a compile-time
                    // constant, so release builds never register this route.
                    if (BuildConfig.DEBUG) {
                        composable(DEBUG_SDUI_PREVIEW_ROUTE) { PayloadPreviewScreen() }
                    }
                }
            }
        }
    }
}

/** Label of the action that reloads the current screen. The UI and the test read it from here. */
internal const val REFRESH_ACTION_LABEL = "Refresh"

/**
 * The frame of a child screen: a bar with "back", a title and its actions. A top-level bar has
 * a hamburger and never "back"; a child bar has "back" and never a hamburger.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ChildScreen(
    title: String,
    onBack: () -> Unit,
    actions: @Composable RowScope.() -> Unit = {},
    content: @Composable () -> Unit,
) {
    Column(modifier = Modifier.fillMaxSize()) {
        TopAppBar(
            title = { Text(text = title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
            navigationIcon = {
                IconButton(onClick = onBack) {
                    Icon(
                        imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                        contentDescription = BACK_DESCRIPTION,
                    )
                }
            },
            actions = actions,
        )
        content()
    }
}

/** Description of the back button on child screens. The UI and the test read it from here. */
internal const val BACK_DESCRIPTION = "Back"

/**
 * Actions of the current top-level screen, drawn in the shell bar. A screen's action should come
 * from its own ViewModel via `viewModel(viewModelStoreOwner = entry)`, which resolves the same
 * instance the screen uses.
 */
@Composable
private fun RowScope.TopLevelActions(destination: AppDestination, entry: NavBackStackEntry) {
    // No parent has a bar action: parents are grids of shortcuts. The `when` stays exhaustive
    // so a new parent forces a decision here.
    when (destination) {
        AppDestination.Home,
        AppDestination.System,
        AppDestination.Docker,
        AppDestination.Dev,
        AppDestination.Security,
        AppDestination.Apps,
        AppDestination.Operations,
        AppDestination.Settings,
        -> Unit
    }
}

/**
 * Tells [TerminalBridge] how to open the Terminal while this shell lives. The registration
 * captures the [NavHostController], so [DisposableEffect] removes it with the shell to avoid
 * navigating on a dead controller.
 *
 * It opens the session list, not a session: the bridge cannot know which session the user
 * wants, and the list is where a session is created when none exists. The pending command is
 * consumed by `TerminalViewModel` once a session opens.
 */
@Composable
private fun ConnectTerminalBridge(navController: NavHostController) {
    val controller by rememberUpdatedState(navController)
    DisposableEffect(Unit) {
        TerminalBridge.onRequestTerminal = {
            controller.navigate(ROUTE_TERMINAL) { launchSingleTop = true }
        }
        onDispose { TerminalBridge.onRequestTerminal = null }
    }
}

/** Route of the device security screen. */
internal const val ROUTE_SECURITY = "seguranca"
