package com.vpsmanager.data.update

import android.app.PendingIntent
import android.content.Context
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.icu.util.ULocale
import android.net.Uri
import android.provider.Settings
import androidx.core.content.FileProvider
import java.io.File
import java.io.IOException
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.first

/** A `PackageInstaller` result, already unwrapped from the Intent. */
data class InstallStatusEvent(
    val sessionId: Int,
    val phase: String,
    val status: Int,
    val message: String?,
)

/**
 * Bridge between the `BroadcastReceiver` (which the system calls, possibly in
 * a fresh process) and the coroutine driving the update.
 *
 * `replay` is deliberately not zero: between requesting pre-approval and
 * starting to collect the result there is a window of microseconds, and a
 * `SharedFlow` without replay would lose a result that arrived in it. Events
 * are filtered by `sessionId` + `phase`, so a replay from an old session is
 * simply ignored.
 */
object InstallStatusBus {
    private val _events = MutableSharedFlow<InstallStatusEvent>(replay = 8, extraBufferCapacity = 16)
    val events: SharedFlow<InstallStatusEvent> = _events

    fun publish(event: InstallStatusEvent) {
        _events.tryEmit(event)
    }
}

/** Desfecho de [ApkInstaller.requestPreapproval]. */
sealed interface PreapprovalOutcome {
    /** The owner confirmed BEFORE the download. Go ahead and fetch. */
    data object Approved : PreapprovalOutcome

    /** The owner declined. Download nothing. */
    data object Declined : PreapprovalOutcome

    /**
     * The device could not manage to ask beforehand. Carry on without
     * pre-approval: the system's ordinary dialog will appear at `commit`.
     * Losing the early question degrades comfort; aborting over it would
     * degrade function.
     */
    data class Unsupported(val detail: String) : PreapprovalOutcome
}

/** Desfecho de [ApkInstaller.commit]. */
sealed interface InstallOutcome {
    /**
     * The system has taken the APK. From here on the process may be killed at
     * any moment — that is how an app replaces itself.
     */
    data object Committed : InstallOutcome

    /**
     * [blocked] is the Android 16 Advanced Protection case (and enterprise
     * policy): not an error in the file or the network, but the device
     * refusing to install from outside the store. Retrying does not help, and
     * the screen has to say so instead of offering "try again".
     */
    data class Failed(val message: String, val blocked: Boolean) : InstallOutcome
}

/**
 * Installs an APK with `PackageInstaller`.
 *
 * ### Why not `FileProvider` + `ACTION_INSTALL_PACKAGE`
 * That path was deprecated in API 29 and never could say WHY it failed — the
 * result was a mute "app not installed". `PackageInstaller` returns
 * `EXTRA_STATUS_MESSAGE`, which is the only useful sentence for anyone without
 * `adb`.
 *
 * ### Why the update stopped going through the system dialog
 * The earlier decision was the opposite — always a dialog, always the same —
 * out of fear of the `SilentUpdatePolicy` throttle, which pushes the install
 * back to the dialog when two silent updates of the same package follow each
 * other within seconds. The reasoning was good for consistency and wrong for
 * this device: the throttle counts SECONDS, and two versions of this app ship
 * hours apart. In practice it never fires.
 *
 * What weighed more: on the owner's device, the system dialog is exactly where
 * Samsung's Auto Blocker interrupts the update. A consistent dialog that never
 * completes is worth less than an update that happens.
 *
 * Consent still exists, just elsewhere: the authorisation is the tap on
 * "Update" inside the app. It is the system that stops asking again.
 *
 * This only holds with a **known install origin** — see
 * [origemDeInstalacaoConhecida].
 *
 * ### Why [requestPreapproval] comes BEFORE the download
 * On the owner's connection, downloading 10 MB only to then ask "may I
 * install?" is the wrong order. `requestUserPreapproval` (API 34; `minSdk` is
 * 34, so no version guard is needed) asks first.
 */
class ApkInstaller(context: Context) : ApkInstallerPort {

    private val appContext = context.applicationContext
    private val installer: PackageInstaller
        get() = appContext.packageManager.packageInstaller

    /** False while "install unknown apps" is off for this app. */
    override fun canInstallFromUnknownSources(): Boolean = appContext.packageManager.canRequestPackageInstalls()

    /**
     * Does Android know WHO installed this app?
     *
     * ## The defect this question exists to answer
     *
     * The owner's device got stuck in a loop: eight consecutive attempts to
     * update, all ending in
     *
     * ```
     * INSTALL_FAILED_ABORTED: Self update is blocked by unknown source package
     * ```
     *
     * The message names BOTH conditions, and both were true here:
     *
     * 1. **"self update"** — the session declared
     *    [PackageInstaller.SessionParams.setAppPackageName] with its own
     *    package, which marks it as an update of the app itself;
     * 2. **"unknown source package"** — the app was sideloaded (a downloaded
     *    APK, or `adb install`), so the recorded installer is `null`.
     *    Confirmed in `dumpsys package`: `installerPackageName=null`.
     *
     * Android refuses the combination: an app with no known provenance may not
     * silently update itself. And (2) cannot be fixed from the side — the
     * recorded installer is only set with `INSTALL_PACKAGES`, a privileged
     * permission an ordinary app never has.
     *
     * So condition (1) goes away instead: when the origin is unknown, the
     * session does NOT declare itself an update of its own package. It becomes
     * an ordinary install, which goes through the "install unknown apps"
     * dialog — the same one the owner already sees when installing the APK by
     * hand, and which they have to approve either way.
     *
     * The price is losing pre-approval (it REQUIRES `setAppPackageName`): the
     * question now happens at the end, after the download, instead of before.
     * One more megabyte of download is cheap; an app that never updates is not.
     */
    override fun origemDeInstalacaoConhecida(): Boolean = try {
        appContext.packageManager
            .getInstallSourceInfo(appContext.packageName)
            .installingPackageName != null
    } catch (e: PackageManager.NameNotFoundException) {
        false
    }

    /**
     * Hands the APK to the system installer through the standard install
     * screen.
     *
     * It is the last rung of the ladder: if the `PackageInstaller` session is
     * refused for any reason, this path still works, because it is exactly
     * what happens when you tap a downloaded `.apk`. The file has already had
     * its SHA-256 checked by `:patch-engine` before reaching here — the
     * [UpdateCoordinator]'s inviolable rule still holds.
     *
     * Returns false when nothing on the device can open an APK, which happens
     * on work profiles where policy blocks sideloading.
     */
    override fun abrirInstaladorDoSistema(apk: File): Boolean {
        val uri = try {
            FileProvider.getUriForFile(appContext, "${appContext.packageName}$SUFIXO_AUTORIDADE", apk)
        } catch (e: IllegalArgumentException) {
            return false
        }
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, "application/vnd.android.package-archive")
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_GRANT_READ_URI_PERMISSION)
        return try {
            appContext.startActivity(intent)
            true
        } catch (e: ActivityNotFoundException) {
            false
        }
    }

    /**
     * An Intent that opens this app's switch DIRECTLY, not the general list of
     * apps — the `package:` `Uri` is what makes that difference, and without it
     * the owner would have to hunt the app down in a list of dozens.
     */
    fun unknownSourcesSettingsIntent(): Intent =
        Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${appContext.packageName}"))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    /**
     * Creates the install session. [apkSizeBytes] goes into `setSize` so the
     * system reserves its space before we write.
     */
    override fun createSession(apkSizeBytes: Long, declararPacote: Boolean): Int? = try {
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            // Declaring the package restricts the session to installing ONLY
            // this app, and is mandatory for pre-approval. But it is also what
            // marks the session as a "self update" — and with an unknown
            // install origin Android aborts that combination. See
            // [origemDeInstalacaoConhecida].
            if (declararPacote) {
                setAppPackageName(appContext.packageName)
                // WITHOUT THE SYSTEM DIALOG.
                //
                // This is only requested on the DECLARED session, the only one
                // Android grants it on: it requires this app to be its own
                // recorded installer. Without that the request is ignored and
                // the install proceeds through the dialog — the rung below on
                // the ladder, which keeps working.
                //
                // Consent has not disappeared: it was given by the tap on
                // "Update". What disappears is the SECOND question, the
                // system's — and that is where Samsung's Auto Blocker steps in.
                setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
            }
            setSize(apkSizeBytes)
            setInstallReason(PackageManager.INSTALL_REASON_USER)
        }
        installer.createSession(params)
    } catch (e: IOException) {
        null
    } catch (e: SecurityException) {
        null
    }

    override fun abandon(sessionId: Int) {
        try {
            installer.abandonSession(sessionId)
        } catch (e: SecurityException) {
            // Session already closed by the system — nothing to undo.
        }
    }

    /**
     * Asks the owner BEFORE spending their data.
     *
     * ⚠️ [label] has to be EXACTLY the label of the app being installed. The
     * system compares the two and, if they differ, DESTROYS the session with
     * `INSTALL_FAILED_INTERNAL_ERROR: PreapprovalDetails { ... } inconsistent
     * with app label`. That is how the first version of this code broke: it
     * passed "VPS Manager 0.1.7", thinking the dialog would be more
     * informative, and the version at the end of the string was enough to
     * invalidate everything. No appending a version, size or suffix here.
     */
    override suspend fun requestPreapproval(sessionId: Int, label: CharSequence): PreapprovalOutcome {
        val details = try {
            PackageInstaller.PreapprovalDetails.Builder()
                .setPackageName(appContext.packageName)
                .setLabel(label)
                .setLocale(ULocale.forLanguageTag("pt-BR"))
                .build()
        } catch (e: IllegalArgumentException) {
            return PreapprovalOutcome.Unsupported(e.message ?: "pre-approval details rejected")
        }

        try {
            installer.openSession(sessionId).use { session ->
                session.requestUserPreapproval(details, statusIntentSender(sessionId, PHASE_PREAPPROVAL).intentSender)
            }
        } catch (e: IOException) {
            return PreapprovalOutcome.Unsupported(e.message ?: e.toString())
        } catch (e: SecurityException) {
            return PreapprovalOutcome.Unsupported(e.message ?: e.toString())
        } catch (e: IllegalArgumentException) {
            return PreapprovalOutcome.Unsupported(e.message ?: e.toString())
        } catch (e: IllegalStateException) {
            return PreapprovalOutcome.Unsupported(e.message ?: e.toString())
        }

        val event = InstallStatusBus.events.first { it.sessionId == sessionId && it.phase == PHASE_PREAPPROVAL }
        return when (event.status) {
            PackageInstaller.STATUS_SUCCESS -> PreapprovalOutcome.Approved
            PackageInstaller.STATUS_FAILURE_ABORTED -> PreapprovalOutcome.Declined
            else -> PreapprovalOutcome.Unsupported(event.message ?: "status ${event.status}")
        }
    }

    /**
     * Writes [apk] into the session and hands it to the system.
     *
     * The APK is NOT deleted here on failure: keeping it is what allows a retry
     * without downloading again.
     */
    override suspend fun commit(sessionId: Int, apk: File): InstallOutcome {
        try {
            installer.openSession(sessionId).use { session ->
                session.openWrite(APK_ENTRY_NAME, 0, apk.length()).use { out ->
                    apk.inputStream().use { input -> input.copyTo(out, COPY_BUFFER_BYTES) }
                    session.fsync(out)
                }
                session.commit(statusIntentSender(sessionId, PHASE_COMMIT).intentSender)
            }
        } catch (e: IOException) {
            return InstallOutcome.Failed("Could not prepare the installation: ${e.message}", blocked = false)
        } catch (e: SecurityException) {
            return InstallOutcome.Failed("The system rejected the install session: ${e.message}", blocked = true)
        } catch (e: IllegalStateException) {
            // Session already destroyed by the system (which is what happens
            // when pre-approval fails). Without this catch the exception
            // escaped the coroutine and the banner stayed stuck on
            // "Installing…" forever — the worst possible outcome, because it
            // says nothing and allows no retry.
            return InstallOutcome.Failed("The install session was no longer valid: ${e.message}", blocked = false)
        } catch (e: IllegalArgumentException) {
            // With targetSdk 35+ an IMMUTABLE PendingIntent makes commit throw
            // exactly this. The receiver has to be able to have the status
            // extras filled in — see statusIntentSender.
            return InstallOutcome.Failed("Installation configuration error: ${e.message}", blocked = false)
        }

        val event = InstallStatusBus.events.first { it.sessionId == sessionId && it.phase == PHASE_COMMIT }
        return when (event.status) {
            PackageInstaller.STATUS_SUCCESS -> InstallOutcome.Committed
            PackageInstaller.STATUS_FAILURE_BLOCKED -> InstallOutcome.Failed(
                event.message ?: "the installation was blocked by the device",
                blocked = true,
            )
            PackageInstaller.STATUS_FAILURE_ABORTED -> {
                // "Cancelled" was a lie half the time. The system aborts both
                // when the owner taps "cancel" and when it REFUSES the session
                // by policy — and in the second case its message is the only
                // clue about what to do. That is how the refused self-update
                // stayed invisible for eight attempts.
                val doSistema = event.message
                if (doSistema.isNullOrBlank()) {
                    InstallOutcome.Failed("Installation canceled.", blocked = false)
                } else {
                    InstallOutcome.Failed(doSistema, blocked = false)
                }
            }
            else -> InstallOutcome.Failed(
                event.message ?: "the installation failed (status ${event.status})",
                blocked = false,
            )
        }
    }

    /**
     * The `PendingIntent` MUST be mutable.
     *
     * It is the system that fills `EXTRA_STATUS`/`EXTRA_STATUS_MESSAGE` into
     * this Intent; an immutable `PendingIntent` cannot receive extras, and with
     * `targetSdk` 35+ `commit()` throws `IllegalArgumentException` in your
     * face. The trap is that lint recommends the immutable one — the advice is
     * right in general and wrong exactly here. The Intent is explicit
     * (`setPackage` plus class), so the mutable one is not an open door:
     * nobody but the system can address it.
     */
    private fun statusIntentSender(sessionId: Int, phase: String): PendingIntent {
        val intent = Intent(appContext, UpdateInstallReceiver::class.java)
            .setAction(UpdateInstallReceiver.ACTION_INSTALL_STATUS)
            .setPackage(appContext.packageName)
            .putExtra(UpdateInstallReceiver.EXTRA_PHASE, phase)
        return PendingIntent.getBroadcast(
            appContext,
            requestCodeFor(sessionId, phase),
            intent,
            PendingIntent.FLAG_MUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
    }

    private fun requestCodeFor(sessionId: Int, phase: String): Int =
        // Distinct codes per phase: a single one would make the second
        // phase's PendingIntent replace the first's and the two results blur.
        sessionId * 2 + if (phase == PHASE_PREAPPROVAL) 0 else 1

    companion object {
        /**
         * Suffix of the update FileProvider's authority. It has to match
         * `android:authorities` in the :data manifest — if they diverge,
         * `getUriForFile` throws IllegalArgumentException and the last rung of
         * the ladder vanishes without warning.
         */
        private const val SUFIXO_AUTORIDADE = ".atualizacao"

        const val PHASE_PREAPPROVAL = "preaprovacao"
        const val PHASE_COMMIT = "instalacao"
        private const val APK_ENTRY_NAME = "base.apk"
        private const val COPY_BUFFER_BYTES = 64 * 1024
    }
}
