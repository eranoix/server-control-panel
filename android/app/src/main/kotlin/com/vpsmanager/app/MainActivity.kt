package com.vpsmanager.app

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.fragment.app.FragmentActivity
import android.view.WindowManager
import com.vpsmanager.data.security.SecurityPreferences
import com.vpsmanager.feature.auth.security.AppGate
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import com.vpsmanager.app.nav.AppNavHost
import com.vpsmanager.app.nav.DeepLinkConsumptionState
import com.vpsmanager.app.nav.resolveNotificationDeepLink
import com.vpsmanager.app.nav.resolveVideocallDeepLink
import com.vpsmanager.data.auth.AppSession
import com.vpsmanager.data.auth.SessionState
import com.vpsmanager.data.update.ResumePoint
import com.vpsmanager.data.update.UpdateDiagnostics
import com.vpsmanager.data.update.UpdateRecovery
import com.vpsmanager.data.update.unknownSourcesSettingsIntent
import com.vpsmanager.designsystem.ThemePreference
import com.vpsmanager.designsystem.VpsManagerTheme
import com.vpsmanager.feature.auth.AuthGateScreen
import com.vpsmanager.feature.notifications.fcm.NotificationDeepLink
import com.vpsmanager.feature.videocall.call.EXTRA_ROOM_ID
import com.vpsmanager.feature.videocall.pip.FloatingWindow
import android.content.res.Configuration
import kotlinx.coroutines.launch

/**
 * Single activity for the app. Draws edge-to-edge unconditionally — minSdk is
 * 34, so there is no pre-API-34 fallback branch to maintain.
 *
 * Gates [AppNavHost] behind [AuthGateScreen] until the device has the TWO
 * things every screen behind the NavHost needs: a server address AND a
 * session. See [decideLaunchDestination] for why one boolean alone was
 * not enough.
 *
 * [AuthGateScreen] owns everything that happens before the session
 * (pairing by QR, configuring the server by hand, signing in with
 * passkey/password) — this Activity does not navigate between those
 * steps, it only observes
 * [com.vpsmanager.data.auth.SessionManager.state].
 *
 * Also the sole consumer of a tapped notification's deep link extras:
 * [NotificationDeepLink.EXTRA_ROUTE]/[NotificationDeepLink.EXTRA_ENTITY_ID] arrive either on the
 * launch Intent read in [onCreate] (cold start — app was not running) or via [onNewIntent] (warm
 * start — the activity was already alive, by far the more common case since notifications are
 * usually tapped while the app is installed and has run before). [deepLinkState] makes sure a
 * matched route is only turned into a navigation event once per tap: without it, re-reading the
 * same launch Intent on the `onCreate` a screen rotation triggers would also re-navigate, jumping
 * back to the notification's screen on every rotation.
 *
 * The same launch Intent carries [EXTRA_ROOM_ID] instead when it comes from
 * [com.vpsmanager.feature.videocall.call.VpsmConnection.onAnswer] — a call answered
 * from the lock screen. [consumeDeepLink] resolves whichever of the two is present through the
 * same single-consumption [pendingDeepLinkRoute] path.
 */
/*
 * FragmentActivity and not ComponentActivity: BiometricPrompt requires a
 * FragmentActivity to host the system dialog. FragmentActivity EXTENDS
 * ComponentActivity, so everything that already worked here keeps working
 * — and without it the app lock simply would not exist.
 */
class MainActivity : FragmentActivity() {

    private lateinit var deepLinkState: DeepLinkConsumptionState

    /**
     * The chosen appearance (light/dark/system). Built in `onCreate`, BEFORE
     * any `setContent`: the read is synchronous on purpose so that the FIRST
     * frame already comes out in the right theme. See [ThemePreference] for
     * why this preference — and only this one — does not use DataStore.
     */
    private lateinit var themePreference: ThemePreference

    /** The screen the person was on when they asked for the update. See [ResumePoint]. */
    private val resumePoint by lazy { ResumePoint(applicationContext) }
    private var pendingDeepLinkRoute by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // `enableEdgeToEdge()` decides the contrast of the system bar icons by
        // looking at the SYSTEM mode, once only. When the owner's choice goes
        // against the device that comes out wrong — what corrects it, on every
        // composition of the theme, is `VpsManagerTheme` itself.
        enableEdgeToEdge()
        themePreference = ThemePreference.get(applicationContext)
        deepLinkState = DeepLinkConsumptionState(
            consumed = savedInstanceState?.getBoolean(STATE_DEEP_LINK_CONSUMED) ?: false,
        )
        consumeDeepLink(intent)
        val serverConfigRepository = (application as VpsManagerApplication).serverConfigRepository

        // Default server baked into the build (BuildConfig.DEFAULT_SERVER_URL,
        // coming from the `vpsmanager.defaultServerUrl` property). This app is
        // built for ONE self-hosted server, so seeding the URL on first boot
        // avoids typing a hostname on the phone — which is exactly the friction
        // QR pairing exists to remove.
        //
        // It only seeds when nothing has been configured yet: `configure`
        // refuses to repoint an already-paired app (RepointBlocked), and this
        // path NEVER passes allowRepoint=true. Without the property the value is
        // empty and the behaviour is exactly what it was before — it lands on
        // the setup screen.
        Bootstrap.step("seed default server") {
            if (shouldSeedDefaultServer(serverConfigRepository.currentBaseUrl(), BuildConfig.DEFAULT_SERVER_URL)) {
                serverConfigRepository.configure(BuildConfig.DEFAULT_SERVER_URL)
            }
        }

        // Diagnostics: if the previous process died, or if any startup stage
        // failed, the FIRST screen is the report — not Home.
        // The operator has no adb; if the app does not tell what happened, nobody does.
        val crashAnterior = Bootstrap.lastCrash(this)
        if (shouldShowDiagnosticScreen(crashAnterior, Bootstrap.initFailures)) {
            setContent {
                val themeMode by themePreference.mode.collectAsStateWithLifecycle()
                VpsManagerTheme(themeMode = themeMode) {
                    DiagnosticsScreen(
                        initFailures = Bootstrap.initFailures.toList(),
                        lastCrash = crashAnterior,
                        onClear = {
                            Bootstrap.clearLastCrash(this)
                            Bootstrap.initFailures.clear()
                            recreate()
                        },
                    )
                }
            }
            return
        }

        // FLAG_SECURE according to the preference. It covers screenshots, screen
        // recording AND the thumbnail in Recents — which is the one Android
        // writes TO DISK when you leave the app, showing the last screen.
        val securityPrefs = SecurityPreferences(applicationContext)
        lifecycleScope.launch {
            securityPrefs.protectFromCapture.collect { protect ->
                if (protect) {
                    window.setFlags(
                        WindowManager.LayoutParams.FLAG_SECURE,
                        WindowManager.LayoutParams.FLAG_SECURE,
                    )
                } else {
                    window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
                }
            }
        }

        // The process session (AppSession) is the SAME one the network
        // interceptor reads and login fills in — which is why it is observed
        // here, not recreated: a 401 that drops the session on any screen
        // returns the operator to login through this same path, with no
        // explicit error navigation.
        val session = AppSession.get(applicationContext)
        val signOut = (application as VpsManagerApplication).signOutRepository
        val updates = (application as VpsManagerApplication).updateCoordinator

        setContent {
            // This StateFlow's initial value ALREADY is what is on disk, so
            // there is no intermediate frame with the wrong theme — no opening
            // light and turning dark.
            val themeMode by themePreference.mode.collectAsStateWithLifecycle()
            VpsManagerTheme(themeMode = themeMode) {
                // THE DOOR comes before everything: when the lock is on, the
                // content is never even composed — and composing Home means
                // fetching data from the server.
                AppGate(
                    activity = this@MainActivity,
                    preferences = securityPrefs,
                ) {
                val sessionState by session.state.collectAsStateWithLifecycle()
                val hasServer = serverConfigRepository.currentBaseUrl() != null
                val scope = rememberCoroutineScope()
                when (decideLaunchDestination(hasServer, sessionState is SessionState.SignedIn)) {
                    LaunchDestination.Home -> {
                        val updateState by updates.state.collectAsStateWithLifecycle()
                        // One check at launch, on top of WorkManager's periodic one:
                        // its minimum period is 15 minutes and the system stretches
                        // it at will, so a device that spends days without network
                        // could go a long time without knowing there is a new
                        // version. The cost is a small JSON, and nothing is
                        // downloaded here.
                        LaunchedEffect(sessionState) {
                            if (sessionState is SessionState.SignedIn) updates.check()
                        }
                        // Re-read on every update state change: that is exactly
                        // when a new failure may have been written.
                        val updateDiagnostics = remember(updateState) {
                            UpdateDiagnostics.read(this@MainActivity)
                        }
                        AppNavHost(
                            pendingDeepLinkRoute = pendingDeepLinkRoute,
                            onDeepLinkConsumed = { pendingDeepLinkRoute = null },
                            // Signing out does not navigate: it drops the session, and
                            // the `when` above — which already observes `session.state`
                            // — swaps the NavHost for the sign-in screen on its own.
                            // Same path as an unrecoverable 401, one mechanism only.
                            onSignOut = { scope.launch { signOut.signOut() } },
                            updateState = updateState,
                            onUpdateClick = updates::start,
                            onUpdateCancel = updates::cancel,
                            onUpdateRecovery = { recovery ->
                                openUpdateExit(recovery, serverConfigRepository.currentBaseUrl())
                            },
                            updateDiagnostics = updateDiagnostics,
                            onClearUpdateDiagnostics = { UpdateDiagnostics.clear(this@MainActivity) },
                            installedVersion = BuildConfig.VERSION_NAME,
                            // `checkAndUpdate` already starts the download when it
                            // finds a new version: the tap on the button was the
                            // authorisation, and Android's confirmation is still the
                            // final gate. See its KDoc.
                            onCheckForUpdate = { scope.launch { updates.checkAndUpdate() } },
                            themeMode = themeMode,
                            onThemeModeChange = themePreference::set,
                        )
                    }
                    LaunchDestination.AuthGate -> AuthGateScreen(
                        serverConfigRepository = serverConfigRepository,
                    )
                }
            }
            }
        }
    }

    /**
     * Opens the way out that the update banner offered.
     *
     * Each one is an EXACT destination, not a generic screen where the owner
     * would have to hunt for what they need: this app's own toggle (and not
     * the list of every app), the storage screen (and not "Settings"), the
     * server's own install page (and not a browser search). A way out that
     * requires searching is not a way out.
     *
     * [UpdateRecovery.SHOW_DIAGNOSTICS] does not reach here: it is navigation
     * inside the app, resolved by `AppNavHost` without leaving for anywhere.
     */
    private fun openUpdateExit(recovery: UpdateRecovery, baseUrl: String?) {
        val intent = when (recovery) {
            UpdateRecovery.ALLOW_UNKNOWN_SOURCES -> unknownSourcesSettingsIntent(this)
            UpdateRecovery.FREE_SPACE -> Intent(Settings.ACTION_INTERNAL_STORAGE_SETTINGS)
            UpdateRecovery.USE_BROWSER ->
                baseUrl?.let { Intent(Intent.ACTION_VIEW, Uri.parse("$it/android/install")) }
            UpdateRecovery.SHOW_DIAGNOSTICS, UpdateRecovery.NONE -> null
        } ?: return
        try {
            startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            // No screen for that destination on this device (a stripped ROM, no
            // browser). It gets recorded where the owner can read it.
            UpdateDiagnostics.record(this, "Update: there is no screen to open ($recovery): ${e.message}")
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        deepLinkState.reset()
        consumeDeepLink(intent)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putBoolean(STATE_DEEP_LINK_CONSUMED, deepLinkState.consumed)
    }

    /**
     * Resolves [intent]'s notification deep link extras, if any, and — the first time this
     * particular Intent is offered — arms [pendingDeepLinkRoute] for [AppNavHost] to navigate to.
     */
    private fun consumeDeepLink(intent: Intent) {
        val resolved = resolveNotificationDeepLink(
            route = intent.getStringExtra(NotificationDeepLink.EXTRA_ROUTE),
            entityId = intent.getStringExtra(NotificationDeepLink.EXTRA_ENTITY_ID),
        ) ?: resolveVideocallDeepLink(intent.getStringExtra(EXTRA_ROOM_ID))

        // COMING BACK FROM AN UPDATE.
        //
        // It enters through the SAME path as a deep link, on purpose. Both
        // say the same thing — "open on this screen, once only" — and
        // `deepLinkState` already guarantees the "once only": without it,
        // a screen rotation would reopen the same route, and that is the
        // defect it exists to close. A second mechanism would have to
        // learn that all over again.
        //
        // The notification WINS over the saved route when both exist:
        // whoever tapped a notification has just said where they want to
        // go, and that is more recent than where they were before updating.
        val route = resolved?.navRoute ?: resumePoint.consume()
        pendingDeepLinkRoute = deepLinkState.consumeOnce(route)
    }

    private companion object {
        const val STATE_DEEP_LINK_CONSUMED = "vpsm_deep_link_consumed"
    }

    /**
     * The screen entered or left the floating window.
     *
     * The system is the one that knows: besides the gesture that leaves
     * the app, the person can expand the little window back, and on that
     * path no code of ours is called first. Publishing here is what lets
     * the call screen redraw itself — a 200 dp window has no room for
     * 48 dp buttons or participant names.
     *
     * It lives on the Activity and not in a feature because the callback
     * is the Activity's: it is the only point of the app that Android
     * notifies.
     */
    override fun onPictureInPictureModeChanged(
        isInPictureInPictureMode: Boolean,
        newConfig: Configuration,
    ) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        FloatingWindow.modeChanged(isInPictureInPictureMode)
    }
}
