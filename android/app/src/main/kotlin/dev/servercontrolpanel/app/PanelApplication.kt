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

internal fun appCoroutineScope(): CoroutineScope {
    val exceptionHandler = CoroutineExceptionHandler { _, throwable ->
        Log.e(APP_SCOPE_LOG_TAG, "unhandled failure in an app background coroutine", throwable)
        Bootstrap.initFailures += "appScope: ${throwable.javaClass.simpleName}: ${throwable.message}"
    }
    return CoroutineScope(SupervisorJob() + Dispatchers.Default + exceptionHandler)
}

class PanelApplication : Application() {

    private val appScope = appCoroutineScope()

    val serverConfigRepository: ServerConfigRepository by lazy {
        ServerConfigRepository(EncryptedServerConfigStore(applicationContext))
    }

    private val sessionManager: SessionManager by lazy {
        SessionManager(
            tokenStore = KeystoreTokenStore(applicationContext),
            refresher = BffSessionRefresher(serverConfigRepository),
        )
    }

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
            wsBaseUrl = defaultTerminalWsBaseUrl(),
        )
    }

    val mobileEventsClient: MobileEventsClient by lazy { MobileEventsClient(mobileEventsSocket, appScope) }

    val updateCoordinator: UpdateCoordinator by lazy {
        createUpdateCoordinator(
            context = applicationContext,
            serverConfigRepository = serverConfigRepository,
            scope = appScope,
        )
    }

    override fun onCreate() {
        super.onCreate()
        Bootstrap.installCrashReporter(this)

        Bootstrap.step("notification channels") {
            NotificationChannels.ensureChannels(this)
        }
        val registrar = PhoneAccountRegistrar(this, ComponentName(this, PanelConnectionService::class.java))
        Bootstrap.step("phone account registration (Telecom)") {
            registrar.ensureRegistered()
        }
        Bootstrap.step("incoming call handler") {
            IncomingCallDispatcher.handler = TelecomIncomingCallHandler(this, registrar)
        }
        Bootstrap.step("server config (keystore)") {
            serverConfigRepository.publishLegacyBasePathSeam()
        }
        Bootstrap.step("session (token guard + auth interceptor)") {
            AppSession.install(sessionManager)
            SessionNetworking.install(sessionManager)
        }
        Bootstrap.step("offline (read cache + network state)") {
            ReadCache.install(this)
            DeviceNetwork.install(this)
            Outbox.install(this)
        }
        Bootstrap.step("native push (Firebase, if provisioned)") {
            FirebaseBootstrap.install(this)
        }
        Bootstrap.step("transfer cleanup (WorkManager)") {
            sweepAbandonedTransfers()
        }
        Bootstrap.step("periodic update check (WorkManager)") {
            UpdateCheckWorker.enqueue(this)
        }
        Bootstrap.step("storage maintenance (WorkManager)") {
            MaintenanceWorker.enqueue(this)
        }
        ProcessLifecycleOwner.get().lifecycle.addObserver(
            object : DefaultLifecycleObserver {
                override fun onStart(owner: LifecycleOwner) {
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

    internal fun sweepAbandonedTransfers(): Job =
        appScope.launch(Dispatchers.IO) {
            TransferMaintenance.sweepAbandonedTransfers(this@PanelApplication)
        }
}
