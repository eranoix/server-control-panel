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

/** Nested detail route below [AppDestination.Terminal] — one live session per session name. */
private fun terminalSessionRoute(name: String) = "terminal/$name"

/** Nav argument name carrying the joined room's id. */
private const val VIDEOCALL_ROOM_ID_ARG = "roomId"

/** Nested detail route below [AppDestination.Videocall] — one active call per room. */
private fun videocallRoomRoute(roomId: String) = "chamada/$roomId"

/** Nav argument name carrying the tapped file's server path, URL-encoded (paths contain `/`). */
private const val FILE_EDITOR_PATH_ARG = "path"

/** Nested detail route below [AppDestination.Files] — one file editor per opened path. */
private fun fileEditorRoute(path: String) = "arquivos/edit/${URLEncoder.encode(path, "UTF-8")}"

/** Nav argument name carrying the opaque SDUI section id — see [AdminScreen]. */
private const val ADMIN_SECTION_ID_ARG = "sectionId"

/**
 * The one route every SDUI-described section — present or future — renders
 * through. This is the concrete answer to forward compatibility ("a new section appears
 * with no app release"): a new `sectionId` needs no new destination here,
 * only a screen descriptor on the server.
 */
internal fun adminSectionRoute(sectionId: String) = "admin/$sectionId"

/** The only section wired today. First to remove once the section list itself becomes server-driven. */
internal const val SCHEDULER_SECTION_ID = "scheduler.jobs"

/** Debug-only route for [PayloadPreviewScreen] — registered only under `BuildConfig.DEBUG`. */
private const val DEBUG_SDUI_PREVIEW_ROUTE = "debug/sdui-preview"

/**
 * Detail route (not a drawer destination) for the diagnostic report.
 *
 * The same screen [com.vpsmanager.app.MainActivity] shows in place of the
 * app when boot failed, now also reachable WITH the app working — this is
 * where the update banner sends you when the install fails, because
 * `PackageInstaller`'s message does not fit in a banner and is the only
 * sentence that explains an "app not installed" to someone with no adb.
 */
internal const val DIAGNOSTICS_ROUTE = "diagnostico"
internal const val ROUTE_STORAGE = "armazenamento"

/**
 * A tapped notification's deep link, already resolved to a concrete [AppNavHost] route
 * [entityId] travels alongside the route for whichever destination first needs to key
 * off it — nothing reads it yet, since the one screen either known route resolves to today
 * (`admin/$SCHEDULER_SECTION_ID`) already lists every job/alert without a per-item argument. It
 * is carried opaquely, never interpolated into a route, a section id, or anything else a hostile
 * launch Intent could use to steer navigation beyond the fixed [navRoute].
 */
internal data class ResolvedNotificationDeepLink(val navRoute: String, val entityId: String?)

/**
 * Maps [NotificationDeepLink.EXTRA_ROUTE]'s raw value to one of [AppNavHost]'s own routes —
 * never the raw extra string itself. [route] arrives on a launch Intent, which any app can send
 * to this (exported) activity with arbitrary extras, so it is untrusted input: anything other
 * than the exact tokens the notification builder emits (`ActionableNotificationBuilder`, in
 * `:feature-notifications`) resolves to `null`. Callers must treat `null` as "navigate nowhere,
 * stay on the current/default screen" — a stale app version, a crafted Intent, and a future route
 * token this build predates are all indistinguishable and all equally harmless.
 */
internal fun resolveNotificationDeepLink(route: String?, entityId: String?): ResolvedNotificationDeepLink? =
    when (route) {
        NotificationDeepLink.ROUTE_DEPLOY_JOB, NotificationDeepLink.ROUTE_ALERT ->
            ResolvedNotificationDeepLink(navRoute = adminSectionRoute(SCHEDULER_SECTION_ID), entityId = entityId)
        else -> null
    }

/**
 * A ringing/answered Telecom call's room id, carried on the launch Intent
 * [com.vpsmanager.feature.videocall.call.VpsmConnection.onAnswer] sends when the user answers from
 * the lock screen. Resolved through the same single-consumption [pendingDeepLinkRoute]
 * path a tapped notification's route is, so answering a call and tapping a notification
 * converge on one mechanism instead of two. `roomId` is opaque, never interpolated into anything
 * beyond this one fixed route shape — same untrusted-input treatment as [resolveNotificationDeepLink].
 */
internal fun resolveVideocallDeepLink(roomId: String?): ResolvedNotificationDeepLink? =
    roomId?.let { ResolvedNotificationDeepLink(navRoute = videocallRoomRoute(it), entityId = null) }

/**
 * How you navigate to a WORK screen from the shell.
 *
 * One single place, and that is the fix for a real defect. The drawer used
 * these options; the dock shortcut and the parent's grid, created later,
 * navigated with `launchSingleTop` alone. The difference is not cosmetic:
 *
 * `popUpTo(start)` POPS the terminal destination that was already open
 * before opening the new one. Without it, reaching the terminal by one
 * path and then by the other left TWO `terminal/{nome}` alive on the
 * stack, each with its own ViewModel, each asking for the scrollback
 * replay and painting over the other. The symptom on the device was every
 * line appearing twice, overlapping — and it started happening ALWAYS once
 * the shortcut became the natural way to the terminal.
 *
 * `saveState`/`restoreState` complete the pair: the screen you left keeps
 * its position, and coming back to it gives you where you were, instead of
 * starting over.
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
 * The app's navigable shell: a [ModalNavigationDrawer] with the eight
 * grouped destinations (see [AppDrawerSheet]), opened by the
 * [TopAppBar]'s hamburger or by the edge-swipe gesture, and the [NavHost]
 * that swaps the content.
 *
 * ## Why a drawer and not a `NavigationBar`
 * See [AppDrawerSheet]'s KDoc: eight destinations do not fit in a Material
 * 3 bottom bar without wrapping and clipping every label. The drawer
 * solves the squeeze WITHOUT hiding any destination behind a "More".
 *
 * ## One header per screen
 * This shell's [TopAppBar] appears only when the current route is an
 * [AppDestination] — that is, a drawer destination. The detail screens
 * (`terminal/{nome}`, `chamada/{sala}`, `arquivos/edit/{path}`) bring
 * their own bar with their own "back", or are full-screen (the call);
 * stacking the shell's bar on top of them would double the header and
 * steal height from whoever needs it most (the terminal). Since a detail
 * is never a drawer destination, the hamburger never becomes unreachable:
 * one level back is enough.
 *
 * The converse holds too, and it is what [TopLevelActions] exists to
 * sustain: **no top-level screen brings a bar of its own**. Before this,
 * Terminal and Notifications did — and the result was literally two
 * stacked headers ("Terminal" from the shell on top of the screen's own
 * "Terminal sessions"), with a "Back" that did a `popBackStack` from a
 * root, that is, from nowhere. A top-level bar has a hamburger, never
 * "back"; a detail bar has "back", never a hamburger.
 *
 * Window insets (IME padding, consuming the [Scaffold]'s content padding)
 * are applied exactly once, on the [NavHost]'s modifier — nothing below
 * reapplies them.
 *
 * [pendingDeepLinkRoute], when non-null, is a route already resolved by
 * [resolveNotificationDeepLink] (a tapped notification) or by
 * [resolveVideocallDeepLink] (a call answered on the lock screen). It is
 * navigated once and [onDeepLinkConsumed] is called so the owner can clear
 * it; a route left set between recompositions would re-navigate on every
 * recomposition.
 *
 * [onSignOut] is the real end of session (revoke on the BFF + wipe the
 * tokens) — see `MainActivity`. It stays a parameter, rather than being
 * built in here, so the UI test can observe that clicking "Sign out"
 * really does drop the session.
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
    // The appearance is a DEVICE preference, not a screen's: it comes in
    // through the shell, with a default, so `MainActivity` stays the only
    // owner of the persistence and a UI test can drive the choice by hand.
    themeMode: ThemeMode = ThemeMode.DEFAULT,
    onThemeModeChange: (ThemeMode) -> Unit = {},
    navController: NavHostController = rememberNavController(),
) {
    val drawerState = rememberDrawerState(initialValue = DrawerValue.Closed)
    val scope = rememberCoroutineScope()

    LaunchedEffect(pendingDeepLinkRoute) {
        val route = pendingDeepLinkRoute ?: return@LaunchedEffect
        // WAITS FOR THE GRAPH TO EXIST before navigating. This effect is
        // declared above the [NavHost] and, on the first composition, it arrives
        // first: navigating at that instant throws "Navigation graph has not been
        // set" and the tapped notification opens Home instead of its own screen.
        // The first emission of `currentBackStackEntryFlow` is exactly the signal
        // that the graph is installed and a current destination already exists.
        navController.currentBackStackEntryFlow.first()
        navController.navigate(route) { launchSingleTop = true }
        onDeepLinkConsumed()
    }

    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route

    // Updating KILLS the process — Android replaces the package and takes down
    // whatever was running. Without this, whoever taps "Update" while looking
    // at Deploys comes back on Home and retraces the path on every version.
    //
    // The route is recorded HERE, and not in the update coordinator, because
    // here is where it exists: the coordinator knows nothing about navigation,
    // and should not.
    val context = LocalContext.current
    val resumePoint = remember(context) { ResumePoint(context.applicationContext) }
    val onRefresh: () -> Unit = {
        resumePoint.save(currentRoute)
        onUpdateClick()
    }
    // With a concrete route per parent (see the grid registrations, below),
    // the current route identifies the destination on its own. The previous
    // version read the id from the ARGUMENT to work around the `mae/{maeId}`
    // pattern — a patch over the same defect that trapped the person inside
    // one parent.
    val currentDestination = AppDestination.entries.firstOrNull { it.matches(currentRoute) }

    ModalNavigationDrawer(
        drawerState = drawerState,
        // Edge gesture deliberately on (the default): it is the way to open the
        // drawer with a thumb, without stretching up to the top of the screen.
        drawerContent = {
            AppDrawerSheet(
                currentRoute = currentRoute,
                onDestinationSelected = { destination ->
                    // Closes BEFORE navigating: the drawer left open on top of
                    // the new screen is this component's classic defect.
                    scope.launch { drawerState.close() }
                    // TAPPING THE DESTINATION YOU ARE ALREADY ON HAS TO DO SOMETHING.
                    //
                    // Without this the tap is swallowed by `launchSingleTop`, and
                    // with `restoreState` the page comes back at the position it
                    // was left at — which, for whoever tapped "Home", is the button
                    // not working. Android's convention for reselection is to go
                    // back to the top, and that is what Home does on this request.
                    if (destination == AppDestination.Home) ScrollToTopRequest.request()
                    navController.goToWorkScreen(destination.navigationTarget)
                },
                onSignOut = {
                    scope.launch { drawerState.close() }
                    onSignOut()
                },
                onShortcut = { route ->
                    scope.launch { drawerState.close() }
                    // The SAME options as the drawer — see goToWorkScreen.
                    // Leaving this out here is what left two terminals alive on
                    // the stack, painting one over the other.
                    navController.goToWorkScreen(route)
                },
            )
        },
    ) {
        Scaffold(
            topBar = {
                // The update banner lives INSIDE the `topBar` slot, in a Column
                // with the bar. It is the only place that survives every drawer
                // destination without being recomposed by navigation, and the
                // Scaffold measures the whole slot — so the `innerPadding` that
                // reaches the NavHost already discounts the banner's height,
                // with no manual adjustment. Putting it inside the content would
                // make every screen discount that height on its own.
                //
                // It disappears on the detail screens by the SAME test that
                // already hides the bar (`currentDestination == null` is exactly
                // terminal, call and file editor): there every dp of height
                // belongs to the content, and an update banner on top of a call
                // in progress is an interruption, not a notice.
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
                                // `backStackEntry` is the SAME entry that
                                // `currentDestination` was derived from, so action and
                                // content never fall out of sync.
                                backStackEntry?.let { entry ->
                                    TopLevelActions(destination = currentDestination, entry = entry)
                                }
                            },
                        )
                        // Above the update banner: with no network, knowing the
                        // screen's data is stale is worth more than knowing a new
                        // version exists — which, besides, cannot be downloaded now.
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
            // `NeedsUpdateCard` (:sdui) sits arbitrarily deep in the tree of
            // a server-described payload — threading an `onUpdateClick`
            // through `SduiScreen` -> `RenderComponent` -> the registry would
            // change four public signatures just to carry one lambda. The
            // module already solves this with a composition local
            // (`LocalActionRunner`), and this follows the same convention.
            // The post-login onboarding moment: ask for POST_NOTIFICATIONS and
            // offer the battery exemption. It lives HERE, and not inside a
            // destination, because it belongs to no screen — and because this
            // is the first point where the person is already known to be
            // authenticated. Without this call the app declared the
            // notification permission and never asked for it; see
            // PushOnboarding.
            PushOnboarding()
            // THE BRIDGE TO THE TERMINAL. Any screen can ask "send this to
            // the terminal"; the only one that knows what "the terminal"
            // means in terms of a ROUTE is this shell, and the project's rule
            // is that no feature knows about routes. So the shell registers
            // HOW the terminal is opened here, and the bridge carries the
            // command.
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
                        // The Home panel emits a DESTINATION, never a route: every
                        // server-described section still comes in through the same
                        // parameterised route `admin/{sectionId}`, and the translation
                        // lives here, with the rest of the navigation.
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
                    // ONE ROUTE PER PARENT, and this is the root fix.
                    //
                    // Before there was a single parameterised registration
                    // (`mae/{maeId}`), and the six parents were ONE destination
                    // to Navigation. The symptom: once inside a parent, you
                    // could no longer leave it through the drawer — only by
                    // pressing back first.
                    //
                    // The cause is the drawer's two navigation options, which
                    // compare by DESTINATION and not by argument:
                    //
                    //   • `launchSingleTop` sees that the top of the stack is
                    //     already `mae/{maeId}` and swallows the tap;
                    //   • `restoreState` restores THAT destination's saved
                    //     state — which is the parent you were already in.
                    //
                    // No adjustment to the click fixes this, because the wrong
                    // equality happens inside Navigation. Concrete routes
                    // (`mae/docker`, `mae/sistema`…) make each parent a real
                    // destination, and then the two options start doing what
                    // they promise. As a bonus, each parent gets its own
                    // bucket of saved state — Docker's search and scroll stop
                    // leaking into System.
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
                            // `launchSingleTop` is NOT cosmetic here. Without
                            // it, opening the same session twice stacked TWO
                            // `terminal/{nome}` destinations, each with its own
                            // TerminalViewModel and its own `everConnected =
                            // false` — that is, a second FRESH attach, which asks
                            // for the scrollback replay again and paints it over
                            // what was already on screen, while the first one's
                            // socket stayed alive (nothing disconnects on
                            // ON_STOP). It was an independent path to the same
                            // duplication the owner reported, and the two
                            // navigation lines just above already carried the
                            // marker; this one had been left behind.
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
                            // Switching sessions REPLACES the destination instead
                            // of stacking: without this, fifteen switches would
                            // leave fifteen terminals on the stack, each with a
                            // live grid in memory, and the back arrow would walk
                            // through all of them before reaching the list.
                            onSwitchSession = { name ->
                                navController.navigate(terminalSessionRoute(name)) {
                                    popUpTo(ROUTE_TERMINAL)
                                    launchSingleTop = true
                                }
                            },
                        )
                    }
                    // Every present/future SDUI section renders through this single
                    // parameterized destination — the concrete form of forward compat ("a new
                    // section appears with no app release"): a new sectionId is a
                    // server-side screen descriptor, never a new route here.
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

                    // Measuring costs reading directories, so it happens when the
                    // screen opens and after each cleanup — never per frame. It is
                    // a keyed `remember`, and not `LaunchedEffect`, because the
                    // value IS the screen: without it there is nothing to draw.
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
                    // Route with NO entry in the drawer: reached from Home's
                    // Session card. See `onOpenSecurity`'s KDoc.
                    composable(ROUTE_SECURITY) { SecurityScreen() }

                    // A detail screen: it brings its own "back" and so does not
                    // appear in the drawer. It is the destination of the update
                    // banner's "Diagnostics" button.
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

                    // Debug-only payload inspector — never part of the release nav
                    // graph. BuildConfig.DEBUG is a compile-time constant,
                    // so a release build never even compiles this branch's route into
                    // the graph; PayloadPreviewScreen also self-gates as a second
                    // layer, but no release code path reaches it either way.
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
 * The current top-level screen's actions, rendered in the app's only bar
 * — the shell's.
 *
 * ## How the screen's action gets here without mutable global state
 * This bar is composed OUTSIDE the [NavHost], so it cannot reach anything
 * the destination's `composable { }` created. The link is not a shared
 * action registry (mutable state one screen writes and the bar reads —
 * the obvious way out, and the one that leaves two sources of truth); it
 * is the destination's own ViewModel, fetched from its
 * [NavBackStackEntry]'s `ViewModelStore`.
 *
 * `viewModel(viewModelStoreOwner = entry)` resolves by the SAME key the
 * screen uses when it calls `viewModel()` inside itself (the owner is the
 * same entry, and the default key comes from the class), so bar and
 * content share ONE instance: the "Refresh" here is the `refresh` of the
 * ViewModel that drew the list, not a second copy. Nothing is hoisted by
 * hand, nothing lives outside the composition tree, and the lifecycle is
 * still the navigation entry's — leaving the destination discards the
 * ViewModel and the action with it.
 *
 * It is the same pattern the project already uses to hoist state (see
 * `NotificationPreferencesRoute`: whoever hosts the route owns the
 * ViewModel and passes callbacks down), only applied to the piece of the
 * screen that lives in the bar.
 *
 * The `when` is exhaustive on purpose: a new destination forces a
 * decision, here, about whether it has a bar action — instead of silently
 * having none.
 */
/**
 * The shell of a CHILD screen: a bar with back, a title and its actions.
 *
 * It was born out of the parent-page reorganisation. Before, Terminal and
 * Notifications were drawer destinations and got the shell's bar — with
 * the hamburger and with the "Refresh" action the shell drew for them.
 * When they became children they lost both at once: no header and no way
 * back to the parent's grid, and the Terminal's "Refresh" simply
 * vanished.
 *
 * The rule the project already followed still holds, and now there is a
 * single place that implements it: **a top-level bar has a hamburger and
 * never "back"; a child's bar has "back" and never a hamburger.**
 * Stacking the two was the two-header defect, and that is what this
 * component prevents by construction — it only exists below a drawer
 * destination.
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

@Composable
private fun RowScope.TopLevelActions(destination: AppDestination, entry: NavBackStackEntry) {
    // No parent has a bar action today, and that is a consequence of the
    // reorganisation itself: a parent is a GRID of shortcuts, not a screen
    // with content to reload. The actions live on the child screens — the
    // Terminal brings its own "Refresh" in the session list, the Jira board
    // brings filters, search and refresh in its own control bar.
    //
    // The `when` is still exhaustive on purpose: a new parent forces a
    // decision, here, about whether it has a bar action — instead of silently
    // having none.
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
 * Teaches [TerminalBridge] how to open the Terminal, for as long as
 * this shell lives.
 *
 * ## Why it is tied to the composition and not to the process
 *
 * The bridge holds a function, and that function captures this shell's
 * [NavHostController]. A registration that outlived the shell would be a
 * leak dressed up as a feature: the next request would navigate on a dead
 * controller — no error, no screen, just a tap that does nothing. The
 * [DisposableEffect] undoes the registration along with the shell.
 *
 * ## Where it leads
 *
 * To the session LIST, never straight to a session. Two reasons: the
 * bridge does not know (and should not know) which session the person
 * wants the command in — they may have nine open —, and the list is also
 * the only place where one is CREATED. That settles the case the model
 * calls its own error: there being no live session. The right answer
 * there is to offer to create one, and that is exactly what the list
 * already does; refusing the command would be the app saying "no" to
 * something it knows how to do.
 *
 * The command stays pending until the session opens — the consumer is
 * `TerminalViewModel`, after the primer.
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
