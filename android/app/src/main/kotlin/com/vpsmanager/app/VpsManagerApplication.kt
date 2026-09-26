package com.vpsmanager.app

import android.app.Application
import android.content.ComponentName
import android.util.Log
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import com.vpsmanager.data.auth.AppSession
import com.vpsmanager.data.auth.BffSessionRefresher
import com.vpsmanager.data.auth.KeystoreTokenStore
import com.vpsmanager.data.auth.SessionManager
import com.vpsmanager.data.auth.SessionNetworking
import com.vpsmanager.data.auth.SignOutRepository
import com.vpsmanager.data.auth.SignOutSource
import com.vpsmanager.data.config.EncryptedServerConfigStore
import com.vpsmanager.data.config.ServerConfigRepository
import com.vpsmanager.data.events.MobileEventsClient
import com.vpsmanager.data.events.MobileEventsRepository
import com.vpsmanager.data.events.MobileEventsSocket
import com.vpsmanager.data.terminal.defaultTerminalWsBaseUrl
import com.vpsmanager.data.offline.CacheDeLeitura
import com.vpsmanager.data.offline.FilaDeEnvio
import com.vpsmanager.data.offline.RedeDoAparelho
import com.vpsmanager.data.update.UpdateCoordinator
import com.vpsmanager.data.update.createUpdateCoordinator
import com.vpsmanager.data.videocall.IncomingCallDispatcher
import com.vpsmanager.app.armazenamento.ManutencaoWorker
import com.vpsmanager.app.update.UpdateCheckWorker
import com.vpsmanager.feature.files.transfer.TransferMaintenance
import com.vpsmanager.feature.notifications.fcm.FirebaseBootstrap
import com.vpsmanager.feature.notifications.fcm.NotificationChannels
import com.vpsmanager.feature.videocall.call.PhoneAccountRegistrar
import com.vpsmanager.feature.videocall.call.TelecomIncomingCallHandler
import com.vpsmanager.feature.videocall.call.VpsmConnectionService
import kotlinx.coroutines.CoroutineExceptionHandler
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

private const val APP_SCOPE_LOG_TAG = "VpsmAppScope"

/**
 * The [CoroutineScope] every fire-and-forget background task started from [VpsManagerApplication]
 * runs on (currently just [VpsManagerApplication.sweepAbandonedTransfers]). A [SupervisorJob]
 * alone is not enough isolation here: it stops a failing child from cancelling its siblings, but
 * an uncaught exception from a `launch` with no [CoroutineExceptionHandler] in its context still
 * propagates to the *thread's* uncaught-exception handler -- which, on a real device, terminates
 * the process. That defeats the entire point of [Bootstrap.step]'s isolation: every OTHER launch
 * step degrades gracefully and the app keeps running, but this one instead escapes into the crash
 * path, wiping out whatever screen the user was on. The handler here gives this scope the exact
 * same isolation semantics as [Bootstrap.step] -- log it, record it in [Bootstrap.initFailures]
 * (so [MainActivity]'s diagnostic screen surfaces it next launch, same as any other init failure),
 * and never let it reach the thread's default handler.
 */
internal fun appCoroutineScope(): CoroutineScope {
    val exceptionHandler = CoroutineExceptionHandler { _, throwable ->
        Log.e(APP_SCOPE_LOG_TAG, "falha nao tratada numa coroutine de fundo do app", throwable)
        Bootstrap.initFailures += "appScope: ${throwable.javaClass.simpleName}: ${throwable.message}"
    }
    return CoroutineScope(SupervisorJob() + Dispatchers.Default + exceptionHandler)
}

/**
 * Application entry point. Dependency wiring for most repositories is still
 * done at the call site (see [com.vpsmanager.feature.auth.HomeViewModel])
 * until a real DI graph is introduced in a later phase — [mobileEventsClient]
 * is the one deliberate exception, because it must be a single instance for
 * the whole app's lifetime, not per-screen like a ViewModel.
 *
 * [ProcessLifecycleOwner] registration below is the ONLY place
 * [MobileEventsSocket.start]/[MobileEventsSocket.stop] are called:
 * `onStart`/`onStop` fire on the whole app's foreground/background
 * transition (not any single Activity's lifecycle), so the socket survives
 * configuration changes and in-app navigation, and closes only when the app
 * itself leaves the foreground. No feature module may call `start()`/`stop()`
 * directly.
 */
class VpsManagerApplication : Application() {

    private val appScope = appCoroutineScope()

    /**
     * The single source of truth for which server this app talks to
     * (`MainActivity`'s first-run gate, the QR pairing flow, and every
     * repository that needs the base URL all go through this one
     * instance) — never a per-repository `System.getProperty` read.
     */
    val serverConfigRepository: ServerConfigRepository by lazy {
        ServerConfigRepository(EncryptedServerConfigStore(applicationContext))
    }

    /**
     * The process session. Built HERE (and registered in [AppSession]) so it
     * shares the same [serverConfigRepository] as the rest of the app —
     * `AppSession.get(context)` only builds one on its own when nobody
     * installed one before.
     */
    private val sessionManager: SessionManager by lazy {
        SessionManager(
            tokenStore = KeystoreTokenStore(applicationContext),
            refresher = BffSessionRefresher(serverConfigRepository),
        )
    }

    /**
     * The navigation drawer's "Sign out". Shares the SAME [sessionManager]
     * and the same [serverConfigRepository] as the rest of the app — signing
     * out has to drop the session the screens read, not a copy.
     */
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
            // Same origin-derivation the terminal socket uses (`ws(s)://` scheme swapped in
            // from the generated client's own HTTP base) — there is only one BFF host, so
            // reusing it here keeps both sockets pointed at the same server by construction.
            wsBaseUrl = defaultTerminalWsBaseUrl(),
        )
    }

    /** The single app-wide subscribe/unsubscribe entry point every screen's ViewModel uses. */
    val mobileEventsClient: MobileEventsClient by lazy { MobileEventsClient(mobileEventsSocket, appScope) }

    /**
     * The app's own incremental update channel.
     *
     * It lives here, and not in a ViewModel, for two reasons that add up: the
     * download has to survive navigation between screens (a `viewModelScope`
     * would die on leaving the screen and kill the download halfway), and the
     * state is ONE for the whole app — the banner belongs to the shell, not to
     * any screen. Same reason [mobileEventsClient] is a single instance and
     * not per-screen.
     *
     * It shares the SAME [serverConfigRepository] as the rest of the app:
     * checking for an update has to talk to the server the owner configured,
     * not to a copy.
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
        // FIRST THING: without this, a crash here dies mute — and the operator has
        // no adb. See Bootstrap.
        Bootstrap.installCrashReporter(this)

        // Each stage below is isolated. None is required for the first screen to
        // open, and several depend on things a manufacturer may refuse (a
        // self-managed phone account, WorkManager, channels). Letting any one of
        // them take the whole app down at boot is the worst possible trade: it
        // swaps a degraded feature for an app that does not open.

        // Must run before any FCM message can possibly arrive — see NotificationChannels'
        // own doc for the STACK.md gotcha this ordering avoids.
        Bootstrap.step("notification channels") {
            NotificationChannels.ensureChannels(this)
        }
        // Registers the one production IncomingCallHandler before any FCM message
        // can possibly arrive, same ordering requirement as NotificationChannels above — without
        // this, VpsFirebaseMessagingService.dispatchIncomingCall finds IncomingCallDispatcher.handler
        // null and drops the ring (see that object's own doc).
        // Two SEPARATE steps on purpose. If registering the phone account fails
        // (some manufacturers refuse a self-managed ConnectionService), the
        // handler still has to be installed: without it,
        // VpsFirebaseMessagingService.dispatchIncomingCall does not find
        // IncomingCallDispatcher.handler and drops the ring — an incoming call
        // simply never rings, for the rest of the process's life, with a single
        // log line as the only sign. Degrading the native call interface is
        // acceptable; going mute is not.
        val registrar = PhoneAccountRegistrar(this, ComponentName(this, VpsmConnectionService::class.java))
        Bootstrap.step("phone account registration (Telecom)") {
            registrar.ensureRegistered()
        }
        Bootstrap.step("incoming call handler") {
            IncomingCallDispatcher.handler = TelecomIncomingCallHandler(this, registrar)
        }
        // Re-publish the persisted server config into ApiClient.BASE_URL_KEY on every
        // process start — see ServerConfigRepository.publishLegacyBasePathSeam. A no-op
        // (does not touch the property) until the device has actually been configured.
        Bootstrap.step("server config (keystore)") {
            serverConfigRepository.publishLegacyBasePathSeam()
        }
        // CRITICAL ORDER: SessionNetworking.install adds the Authorization
        // interceptor to ApiClient.builder, and ApiClient.defaultClient is a
        // `by lazy { builder.build() }` — once any *Api touches that client,
        // touching the builder has no effect any more. That is why this stage
        // comes BEFORE anything that does networking (the events socket, the
        // transfer sweep) and after publishing the basePath, which the token
        // refresh needs in order to know who to talk to.
        Bootstrap.step("session (token guard + auth interceptor)") {
            AppSession.install(sessionManager)
            SessionNetworking.install(sessionManager)
        }
        // The SAME critical window as the step above, and for the same reason:
        // the cache is installed on `ApiClient.builder`, and `defaultClient` is a
        // `by lazy { builder.build() }`. Once any *Api touches that client, this
        // becomes a silent no-op — and the symptom would be the app still dying
        // without internet, with no error pointing here.
        Bootstrap.step("offline (read cache + network state)") {
            CacheDeLeitura.instalar(this)
            RedeDoAparelho.instalar(this)
            // The queue is read from disk here: an action queued yesterday has to
            // reappear today, and WorkManager only delivers it if somebody rewires
            // the work after the process dies.
            FilaDeEnvio.instalar(this)
        }
        // Native push: brings Firebase up if — and only if — the console's
        // google-services.json is in assets/. Without it, this step does nothing
        // and records the instruction, the same way the server degrades when
        // `fcm_service_account` is not in the vault. See FirebaseBootstrap.
        Bootstrap.step("native push (Firebase, if provisioned)") {
            FirebaseBootstrap.instalar(this)
        }
        Bootstrap.step("transfer cleanup (WorkManager)") {
            sweepAbandonedTransfers()
        }
        // AFTER the session stage: the manifest check is authenticated, and
        // without the interceptor installed it would go out with no
        // Authorization and come back 401. The worker only CHECKS (a small
        // JSON) — what downloads is the tap on the banner.
        Bootstrap.step("periodic update check (WorkManager)") {
            UpdateCheckWorker.enqueue(this)
        }
        // Daily storage cleanup. Enqueueing is cheap (one write to WorkManager's
        // database); what runs it is the system, when the device is comfortable.
        // See ManutencaoWorker about the KEEP.
        Bootstrap.step("storage maintenance (WorkManager)") {
            ManutencaoWorker.enfileirar(this)
        }
        ProcessLifecycleOwner.get().lifecycle.addObserver(
            object : DefaultLifecycleObserver {
                override fun onStart(owner: LifecycleOwner) {
                    // Never dial out before a server is configured — an unconfigured
                    // device has no real host to connect to (see defaultTerminalWsBaseUrl).
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
     * Deferral: a transfer cancelled while the process was dead (or
     * force-stopped before WorkManager ever reported a terminal state the app
     * observed) never runs [TransferViewModel][com.vpsmanager.feature.files.transfer.TransferViewModel]'s
     * own cancel-time cleanup. Sweeping once per process start catches those
     * -- cheap when there is nothing to clean (the common case) and bounded
     * by however many transfers this device has ever started. Delegates to
     * [TransferMaintenance] (feature-files) rather than depending on
     * `androidx.work` here directly -- `:app` only depends on `:feature-files`
     * as `implementation`, which intentionally does not leak that dependency.
     *
     * Returns the launched [Job] (`internal` visibility) so tests can `join()` it instead of
     * racing a fire-and-forget coroutine — production call sites ignore the return value.
     */
    internal fun sweepAbandonedTransfers(): Job =
        appScope.launch(Dispatchers.IO) {
            TransferMaintenance.sweepAbandonedTransfers(this@VpsManagerApplication)
        }
}
