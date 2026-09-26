package dev.servercontrolpanel.app

import android.app.Application
import android.content.ComponentName
import android.util.Log
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import dev.servercontrolpanel.data.auth.AppSession
import dev.servercontrolpanel.data.auth.BffSessionRefresher
import dev.servercontrolpanel.data.auth.KeystoreTokenStore
import dev.servercontrolpanel.data.auth.SessionManager
import dev.servercontrolpanel.data.auth.SessionNetworking
import dev.servercontrolpanel.data.auth.SignOutRepository
import dev.servercontrolpanel.data.auth.SignOutSource
import dev.servercontrolpanel.data.config.EncryptedServerConfigStore
import dev.servercontrolpanel.data.config.ServerConfigRepository
import dev.servercontrolpanel.data.events.MobileEventsClient
import dev.servercontrolpanel.data.events.MobileEventsRepository
import dev.servercontrolpanel.data.events.MobileEventsSocket
import dev.servercontrolpanel.data.terminal.defaultTerminalWsBaseUrl
import dev.servercontrolpanel.data.offline.ReadCache
import dev.servercontrolpanel.data.offline.Outbox
import dev.servercontrolpanel.data.offline.DeviceNetwork
import dev.servercontrolpanel.data.update.UpdateCoordinator
import dev.servercontrolpanel.data.update.createUpdateCoordinator
import dev.servercontrolpanel.data.videocall.IncomingCallDispatcher
import dev.servercontrolpanel.app.storage.MaintenanceWorker
import dev.servercontrolpanel.app.update.UpdateCheckWorker
import dev.servercontrolpanel.feature.files.transfer.TransferMaintenance
import dev.servercontrolpanel.feature.notifications.fcm.FirebaseBootstrap
import dev.servercontrolpanel.feature.notifications.fcm.NotificationChannels
import dev.servercontrolpanel.feature.videocall.call.PhoneAccountRegistrar
import dev.servercontrolpanel.feature.videocall.call.TelecomIncomingCallHandler
import dev.servercontrolpanel.feature.videocall.call.PanelConnectionService
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

private const val APP_SCOPE_LOG_TAG = "PanelAppScope"

/**
 * The scope for fire-and-forget background tasks started from [PanelApplication].
 * [SupervisorJob] alone would still let an uncaught exception reach the thread's handler and
 * kill the process; the [CoroutineExceptionHandler] gives it the same isolation as
 * [Bootstrap.step]: log, record in [Bootstrap.initFailures], and keep running.
 */
internal fun appCoroutineScope(): CoroutineScope {
    val exceptionHandler = CoroutineExceptionHandler { _, throwable ->
        Log.e(APP_SCOPE_LOG_TAG, "unhandled failure in an app background coroutine", throwable)
        Bootstrap.initFailures += "appScope: ${throwable.javaClass.simpleName}: ${throwable.message}"
    }
    return CoroutineScope(SupervisorJob() + Dispatchers.Default + exceptionHandler)
}

/**
 * Application entry point. Most repositories are wired at the call site; app-wide singletons
 * such as [mobileEventsClient] live here.
 *
 * The [ProcessLifecycleOwner] observer is the only place [MobileEventsSocket.start] and
 * [MobileEventsSocket.stop] are called, so the socket survives configuration changes and
 * navigation and closes only when the whole app leaves the foreground. Feature modules must
 * never call them.
 */
class PanelApplication : Application() {

    private val appScope = appCoroutineScope()

    /** The single source of truth for which server this app talks to. */
    val serverConfigRepository: ServerConfigRepository by lazy {
        ServerConfigRepository(EncryptedServerConfigStore(applicationContext))
    }

    /**
     * The process session, built here and registered in [AppSession] so it shares
     * [serverConfigRepository] with the rest of the app.
     */
    private val sessionManager: SessionManager by lazy {
        SessionManager(
            tokenStore = KeystoreTokenStore(applicationContext),
            refresher = BffSessionRefresher(serverConfigRepository),
        )
    }

    /** "Sign out", sharing [sessionManager] so it drops the session the screens actually read. */
    val signOutRepository: SignOutSource by lazy {
        SignOutRepository(
            session = sessionManager,
            serverConfigRepository = serverConfigRepository,
        )
    }

    private val mobileEventsSocket: MobileEventsSocket by lazy {
        MobileEventsSocket(
            ticketSource = MobileEventsRepository(),
            scope = appScope,
            // Same origin as the terminal socket, so both always point at the same server.
            wsBaseUrl = defaultTerminalWsBaseUrl(),
        )
    }

    /** The single app-wide subscribe/unsubscribe entry point every screen's ViewModel uses. */
    val mobileEventsClient: MobileEventsClient by lazy { MobileEventsClient(mobileEventsSocket, appScope) }

    /**
     * The app's incremental self-update channel. It lives here, not in a ViewModel, because the
     * download must survive navigation and its state is shared by the whole shell.
     */
    val updateCoordinator: UpdateCoordinator by lazy {
        createUpdateCoordinator(
            context = applicationContext,
            serverConfigRepository = serverConfigRepository,
            scope = appScope,
        )
    }

    override fun onCreate() {
        super.onCreate()
        // First, so that any crash below is persisted. See Bootstrap.
        Bootstrap.installCrashReporter(this)

        // Each stage is isolated: none is required for the first screen, and several depend
        // on things a manufacturer may refuse.

        // Must run before any FCM message can arrive; see NotificationChannels.
        Bootstrap.step("notification channels") {
            NotificationChannels.ensureChannels(this)
        }
        // The incoming call handler must be installed before any FCM message arrives, or
        // incoming calls are dropped. It is a separate step so a refused phone account
        // registration (some manufacturers reject self-managed ConnectionService) does not
        // stop calls from ringing.
        val registrar = PhoneAccountRegistrar(this, ComponentName(this, PanelConnectionService::class.java))
        Bootstrap.step("phone account registration (Telecom)") {
            registrar.ensureRegistered()
        }
        Bootstrap.step("incoming call handler") {
            IncomingCallDispatcher.handler = TelecomIncomingCallHandler(this, registrar)
        }
        // Re-publish the persisted server config on every process start; a no-op until
        // the device is configured. See ServerConfigRepository.publishLegacyBasePathSeam.
        Bootstrap.step("server config (keystore)") {
            serverConfigRepository.publishLegacyBasePathSeam()
        }
        // Critical order: this adds the auth interceptor to ApiClient.builder, and
        // ApiClient.defaultClient is built lazily from it, so it must run before any
        // networking and after the base path is published (the token refresh needs it).
        Bootstrap.step("session (token guard + auth interceptor)") {
            AppSession.install(sessionManager)
            SessionNetworking.install(sessionManager)
        }
        // Same ordering constraint as above: the cache is installed on ApiClient.builder and
        // silently does nothing once the client has been built.
        Bootstrap.step("offline (read cache + network state)") {
            ReadCache.install(this)
            DeviceNetwork.install(this)
            // Reloads the persisted queue so actions queued before the process died are delivered.
            Outbox.install(this)
        }
        // Starts Firebase only if google-services.json is in assets/; otherwise it records
        // what is missing. See FirebaseBootstrap.
        Bootstrap.step("native push (Firebase, if provisioned)") {
            FirebaseBootstrap.install(this)
        }
        Bootstrap.step("transfer cleanup (WorkManager)") {
            sweepAbandonedTransfers()
        }
        // After the session stage: the check is authenticated and would get a 401 without
        // the interceptor.
        Bootstrap.step("periodic update check (WorkManager)") {
            UpdateCheckWorker.enqueue(this)
        }
        // Daily storage cleanup; see MaintenanceWorker.
        Bootstrap.step("storage maintenance (WorkManager)") {
            MaintenanceWorker.enqueue(this)
        }
        ProcessLifecycleOwner.get().lifecycle.addObserver(
            object : DefaultLifecycleObserver {
                override fun onStart(owner: LifecycleOwner) {
                    // Never connect before a server is configured.
                    if (serverConfigRepository.currentBaseUrl() != null) {
                        mobileEventsSocket.start()
                    }
                }

                override fun onStop(owner: LifecycleOwner) {
                    if (serverConfigRepository.currentBaseUrl() != null) {
                        mobileEventsSocket.stop()
                    }
                }
            },
        )
    }

    /**
     * Cleans up transfers cancelled while the process was dead, which never ran
     * [TransferViewModel][dev.servercontrolpanel.feature.files.transfer.TransferViewModel]'s own cleanup.
     * Runs once per process start through [TransferMaintenance].
     *
     * Returns the [Job] so tests can `join()` it; production ignores it.
     */
    internal fun sweepAbandonedTransfers(): Job =
        appScope.launch(Dispatchers.IO) {
            TransferMaintenance.sweepAbandonedTransfers(this@PanelApplication)
        }
}
