package dev.servercontrolpanel.app

import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.fragment.app.FragmentActivity
import android.view.WindowManager
import dev.servercontrolpanel.data.security.SecurityPreferences
import dev.servercontrolpanel.feature.auth.security.AppGate
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
import dev.servercontrolpanel.app.nav.AppNavHost
import dev.servercontrolpanel.app.nav.DeepLinkConsumptionState
import dev.servercontrolpanel.app.nav.resolveNotificationDeepLink
import dev.servercontrolpanel.app.nav.resolveVideocallDeepLink
import dev.servercontrolpanel.data.auth.AppSession
import dev.servercontrolpanel.data.auth.SessionState
import dev.servercontrolpanel.data.update.ResumePoint
import dev.servercontrolpanel.data.update.UpdateDiagnostics
import dev.servercontrolpanel.data.update.UpdateRecovery
import dev.servercontrolpanel.data.update.unknownSourcesSettingsIntent
import dev.servercontrolpanel.designsystem.ThemePreference
import dev.servercontrolpanel.designsystem.PanelTheme
import dev.servercontrolpanel.feature.auth.AuthGateScreen
import dev.servercontrolpanel.feature.notifications.fcm.NotificationDeepLink
import dev.servercontrolpanel.feature.videocall.call.EXTRA_ROOM_ID
import dev.servercontrolpanel.feature.videocall.pip.FloatingWindow
import android.content.res.Configuration
import kotlinx.coroutines.launch

class MainActivity : FragmentActivity() {

    private lateinit var deepLinkState: DeepLinkConsumptionState

    private lateinit var themePreference: ThemePreference

    private val resumePoint by lazy { ResumePoint(applicationContext) }
    private var pendingDeepLinkRoute by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        themePreference = ThemePreference.get(applicationContext)
        deepLinkState = DeepLinkConsumptionState(
            consumed = savedInstanceState?.getBoolean(STATE_DEEP_LINK_CONSUMED) ?: false,
        )
        consumeDeepLink(intent)
        val serverConfigRepository = (application as PanelApplication).serverConfigRepository

        Bootstrap.step("seed default server") {
            if (shouldSeedDefaultServer(serverConfigRepository.currentBaseUrl(), BuildConfig.DEFAULT_SERVER_URL)) {
                serverConfigRepository.configure(BuildConfig.DEFAULT_SERVER_URL)
            }
        }

        val crashAnterior = Bootstrap.lastCrash(this)
        if (shouldShowDiagnosticScreen(crashAnterior, Bootstrap.initFailures)) {
            setContent {
                val themeMode by themePreference.mode.collectAsStateWithLifecycle()
                PanelTheme(themeMode = themeMode) {
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

        val session = AppSession.get(applicationContext)
        val signOut = (application as PanelApplication).signOutRepository
        val updates = (application as PanelApplication).updateCoordinator

        setContent {
            val themeMode by themePreference.mode.collectAsStateWithLifecycle()
            PanelTheme(themeMode = themeMode) {
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
                        LaunchedEffect(sessionState) {
                            if (sessionState is SessionState.SignedIn) updates.check()
                        }
                        val updateDiagnostics = remember(updateState) {
                            UpdateDiagnostics.read(this@MainActivity)
                        }
                        AppNavHost(
                            pendingDeepLinkRoute = pendingDeepLinkRoute,
                            onDeepLinkConsumed = { pendingDeepLinkRoute = null },
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

    private fun consumeDeepLink(intent: Intent) {
        val resolved = resolveNotificationDeepLink(
            route = intent.getStringExtra(NotificationDeepLink.EXTRA_ROUTE),
            entityId = intent.getStringExtra(NotificationDeepLink.EXTRA_ENTITY_ID),
        ) ?: resolveVideocallDeepLink(intent.getStringExtra(EXTRA_ROOM_ID))

        val route = resolved?.navRoute ?: resumePoint.consume()
        pendingDeepLinkRoute = deepLinkState.consumeOnce(route)
    }

    private companion object {
        const val STATE_DEEP_LINK_CONSUMED = "panel_deep_link_consumed"
    }

    override fun onPictureInPictureModeChanged(
        isInPictureInPictureMode: Boolean,
        newConfig: Configuration,
    ) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        FloatingWindow.modeChanged(isInPictureInPictureMode)
    }
}
