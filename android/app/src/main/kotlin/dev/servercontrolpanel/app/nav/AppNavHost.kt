package dev.servercontrolpanel.app.nav

import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import dev.servercontrolpanel.data.update.ResumePoint
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
import dev.servercontrolpanel.app.storage.StorageScreen
import dev.servercontrolpanel.data.storage.AppStorage
import dev.servercontrolpanel.data.storage.DeviceStorageAreas
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
import dev.servercontrolpanel.designsystem.ThemeMode
import dev.servercontrolpanel.app.BuildConfig
import dev.servercontrolpanel.app.DiagnosticsScreen
import dev.servercontrolpanel.app.licenses.OssLicensesScreen
import dev.servercontrolpanel.app.offline.OfflineBanner
import dev.servercontrolpanel.core.shell.TerminalBridge
import dev.servercontrolpanel.feature.auth.dashboard.ScrollToTopRequest
import dev.servercontrolpanel.app.update.UpdateBanner
import dev.servercontrolpanel.data.update.UpdateRecovery
import dev.servercontrolpanel.data.update.UpdateState
import dev.servercontrolpanel.feature.admin.AdminScreen
import dev.servercontrolpanel.feature.auth.HomeScreen
import dev.servercontrolpanel.feature.auth.security.SecurityScreen
import dev.servercontrolpanel.feature.jira.JiraBoardRoute
import dev.servercontrolpanel.feature.files.browse.FileBrowserScreen
import dev.servercontrolpanel.feature.files.editor.FileEditorScreen
import dev.servercontrolpanel.feature.notifications.fcm.NotificationDeepLink
import dev.servercontrolpanel.feature.notifications.fcm.PushOnboarding
import dev.servercontrolpanel.feature.notifications.prefs.NotificationPreferencesRoute
import dev.servercontrolpanel.feature.terminal.ui.SessionListScreen
import dev.servercontrolpanel.feature.terminal.ui.SessionListViewModel
import dev.servercontrolpanel.feature.terminal.ui.TERMINAL_SESSION_NAME_ARG
import dev.servercontrolpanel.feature.terminal.ui.TerminalRoute
import dev.servercontrolpanel.feature.videocall.CallScreen
import dev.servercontrolpanel.feature.videocall.RoomLobbyScreen
import dev.servercontrolpanel.feature.whatsapp.WhatsAppRoute
import dev.servercontrolpanel.sdui.registry.LocalUpdateRequest
import dev.servercontrolpanel.sdui.debug.PayloadPreviewScreen
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import java.net.URLDecoder
import java.net.URLEncoder

private fun terminalSessionRoute(name: String) = "terminal/$name"

private const val VIDEOCALL_ROOM_ID_ARG = "roomId"

private fun videocallRoomRoute(roomId: String) = "call/$roomId"

private const val FILE_EDITOR_PATH_ARG = "path"

private fun fileEditorRoute(path: String) = "files/edit/${URLEncoder.encode(path, "UTF-8")}"

private const val ADMIN_SECTION_ID_ARG = "sectionId"

internal fun adminSectionRoute(sectionId: String) = "admin/$sectionId"

internal const val SCHEDULER_SECTION_ID = "scheduler.jobs"

private const val DEBUG_SDUI_PREVIEW_ROUTE = "debug/sdui-preview"

internal const val DIAGNOSTICS_ROUTE = "diagnostics"
internal const val ROUTE_STORAGE = "storage"

internal data class ResolvedNotificationDeepLink(val navRoute: String, val entityId: String?)

internal fun resolveNotificationDeepLink(route: String?, entityId: String?): ResolvedNotificationDeepLink? =
    when (route) {
        NotificationDeepLink.ROUTE_DEPLOY_JOB, NotificationDeepLink.ROUTE_ALERT ->
            ResolvedNotificationDeepLink(navRoute = adminSectionRoute(SCHEDULER_SECTION_ID), entityId = entityId)
        else -> null
    }

internal fun resolveVideocallDeepLink(roomId: String?): ResolvedNotificationDeepLink? =
    roomId?.let { ResolvedNotificationDeepLink(navRoute = videocallRoomRoute(it), entityId = null) }

private fun NavHostController.goToWorkScreen(route: String) {
    navigate(route) {
        graph.startDestinationRoute?.let { start ->
            popUpTo(start) { saveState = true }
        }
        launchSingleTop = true
        restoreState = true
    }
}

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
    themeMode: ThemeMode = ThemeMode.DEFAULT,
    onThemeModeChange: (ThemeMode) -> Unit = {},
    navController: NavHostController = rememberNavController(),
) {
    val drawerState = rememberDrawerState(initialValue = DrawerValue.Closed)
    val scope = rememberCoroutineScope()

    LaunchedEffect(pendingDeepLinkRoute) {
        val route = pendingDeepLinkRoute ?: return@LaunchedEffect
        navController.currentBackStackEntryFlow.first()
        navController.navigate(route) { launchSingleTop = true }
        onDeepLinkConsumed()
    }

    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route

    val context = LocalContext.current
    val resumePoint = remember(context) { ResumePoint(context.applicationContext) }
    val onRefresh: () -> Unit = {
        resumePoint.save(currentRoute)
        onUpdateClick()
    }
    val currentDestination = AppDestination.entries.firstOrNull { it.matches(currentRoute) }

    ModalNavigationDrawer(
        drawerState = drawerState,
        drawerContent = {
            AppDrawerSheet(
                currentRoute = currentRoute,
                onDestinationSelected = { destination ->
                    scope.launch { drawerState.close() }
                    if (destination == AppDestination.Home) ScrollToTopRequest.request()
                    navController.goToWorkScreen(destination.navigationTarget)
                },
                onSignOut = {
                    scope.launch { drawerState.close() }
                    onSignOut()
                },
                onShortcut = { route ->
                    scope.launch { drawerState.close() }
                    navController.goToWorkScreen(route)
                },
            )
        },
    ) {
        Scaffold(
            topBar = {
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
                                backStackEntry?.let { entry ->
                                    TopLevelActions(destination = currentDestination, entry = entry)
                                }
                            },
                        )
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
            PushOnboarding()
            ConnectTerminalBridge(navController)
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
                            onSwitchSession = { name ->
                                navController.navigate(terminalSessionRoute(name)) {
                                    popUpTo(ROUTE_TERMINAL)
                                    launchSingleTop = true
                                }
                            },
                        )
                    }
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
                        route = "call/{$VIDEOCALL_ROOM_ID_ARG}",
                        arguments = listOf(navArgument(VIDEOCALL_ROOM_ID_ARG) { type = NavType.StringType }),
                    ) { entry ->
                        val roomId = checkNotNull(entry.arguments?.getString(VIDEOCALL_ROOM_ID_ARG)) {
                            "call/{$VIDEOCALL_ROOM_ID_ARG} route requires a '$VIDEOCALL_ROOM_ID_ARG' argument"
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
                        route = "files/edit/{$FILE_EDITOR_PATH_ARG}",
                        arguments = listOf(navArgument(FILE_EDITOR_PATH_ARG) { type = NavType.StringType }),
                    ) { entry ->
                        val encodedPath = checkNotNull(entry.arguments?.getString(FILE_EDITOR_PATH_ARG)) {
                            "files/edit route requires a '$FILE_EDITOR_PATH_ARG' argument"
                        }
                        FileEditorScreen(
                            path = URLDecoder.decode(encodedPath, "UTF-8"),
                            onBack = { navController.popBackStack() },
                        )
                    }
                    composable(ROUTE_LICENSES) { OssLicensesScreen() }

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
                    composable(ROUTE_SECURITY) { SecurityScreen() }

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

                    if (BuildConfig.DEBUG) {
                        composable(DEBUG_SDUI_PREVIEW_ROUTE) { PayloadPreviewScreen() }
                    }
                }
            }
        }
    }
}

internal const val REFRESH_ACTION_LABEL = "Refresh"

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

internal const val BACK_DESCRIPTION = "Back"

@Composable
private fun RowScope.TopLevelActions(destination: AppDestination, entry: NavBackStackEntry) {
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

internal const val ROUTE_SECURITY = "security"
