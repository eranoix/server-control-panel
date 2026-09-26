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
 * Bridge between the `BroadcastReceiver` (called by the system, possibly in a
 * fresh process) and the coroutine driving the update. `replay` is non-zero so a
 * result arriving before collection starts is not lost; events are filtered by
 * `sessionId` + `phase`, so replays from old sessions are ignored.
 */
object InstallStatusBus {
    private val _events = MutableSharedFlow<InstallStatusEvent>(replay = 8, extraBufferCapacity = 16)
    val events: SharedFlow<InstallStatusEvent> = _events

    fun publish(event: InstallStatusEvent) {
        _events.tryEmit(event)
    }
}

/** Outcome of [ApkInstaller.requestPreapproval]. */
sealed interface PreapprovalOutcome {
    /** The owner confirmed before the download. */
    data object Approved : PreapprovalOutcome

    /** The owner declined. Download nothing. */
    data object Declined : PreapprovalOutcome

    /**
     * The device could not ask beforehand. Carry on without pre-approval: the
     * system's regular dialog appears at `commit`.
     */
    data class Unsupported(val detail: String) : PreapprovalOutcome
}

/** Outcome of [ApkInstaller.commit]. */
sealed interface InstallOutcome {
    /** The system has taken the APK; the process may be killed at any moment from here on. */
    data object Committed : InstallOutcome

    /**
     * [blocked]: the device refuses installs from outside the store (Android 16
     * Advanced Protection or enterprise policy). Retrying does not help, so the UI
     * must not offer "try again".
     */
    data class Failed(val message: String, val blocked: Boolean) : InstallOutcome
}

/**
 * Installs an APK with `PackageInstaller`, which (unlike the deprecated
 * `ACTION_INSTALL_PACKAGE`) returns `EXTRA_STATUS_MESSAGE` explaining failures.
 *
 * Updates skip the system dialog when possible: consent is the tap on "Update"
 * in the app, and the system dialog is where Samsung's Auto Blocker interrupts
 * the update. `SilentUpdatePolicy` throttling only triggers for updates seconds
 * apart. This requires a known install origin, see [installSourceKnown].
 *
 * [requestPreapproval] runs BEFORE the download so the owner is not asked after
 * spending data (API 34, which is the `minSdk`).
 */
class ApkInstaller(context: Context) : ApkInstallerPort {

    private val appContext = context.applicationContext
    private val installer: PackageInstaller
        get() = appContext.packageManager.packageInstaller

    /** False while "install unknown apps" is off for this app. */
    override fun canInstallFromUnknownSources(): Boolean = appContext.packageManager.canRequestPackageInstalls()

    /**
     * Whether Android knows who installed this app.
     *
     * A sideloaded app (installer `null`) whose session declares its own package
     * as a "self update" fails with `INSTALL_FAILED_ABORTED: Self update is
     * blocked by unknown source package`. The installer record cannot be set
     * without the privileged `INSTALL_PACKAGES`, so in that case the session does
     * not declare the package and becomes a regular install through the
     * "install unknown apps" dialog. The cost is losing pre-approval, which
     * requires `setAppPackageName`.
     */
    override fun installSourceKnown(): Boolean = try {
        appContext.packageManager
            .getInstallSourceInfo(appContext.packageName)
            .installingPackageName != null
    } catch (e: PackageManager.NameNotFoundException) {
        false
    }

    /**
     * Hands the APK to the standard system install screen: the last fallback
     * when the `PackageInstaller` session is refused. The SHA-256 was already
     * checked by `:patch-engine`, so [UpdateCoordinator]'s rule still holds.
     *
     * Returns false when nothing can open an APK (e.g. work profiles that block
     * sideloading).
     */
    override fun openSystemInstaller(apk: File): Boolean {
        val uri = try {
            FileProvider.getUriForFile(appContext, "${appContext.packageName}$AUTHORITY_SUFFIX", apk)
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
     * Opens this app's own "install unknown apps" switch; the `package:` `Uri`
     * skips the general app list.
     */
    fun unknownSourcesSettingsIntent(): Intent =
        Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${appContext.packageName}"))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    /**
     * Creates the install session. [apkSizeBytes] goes into `setSize` so the
     * system reserves its space before we write.
     */
    override fun createSession(apkSizeBytes: Long, declarePackage: Boolean): Int? = try {
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            // Declaring the package is required for pre-approval but marks the
            // session as a "self update", which Android aborts when the install
            // origin is unknown. See [installSourceKnown].
            if (declarePackage) {
                setAppPackageName(appContext.packageName)
                // No system dialog. Android only honours this on a declared
                // session when this app is its own recorded installer; otherwise
                // it is ignored and the dialog appears. Consent was the tap on
                // "Update"; the dialog is where Samsung's Auto Blocker steps in.
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
     * [label] must be EXACTLY the installed app's label: if it differs the system
     * destroys the session with `INSTALL_FAILED_INTERNAL_ERROR: PreapprovalDetails
     * ... inconsistent with app label`. Never append a version or suffix.
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
     * Writes [apk] into the session and hands it to the system. The APK is kept
     * on failure so a retry needs no new download.
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
            // Session already destroyed by the system (e.g. after a failed
            // pre-approval). Without this catch the banner would stay stuck on
            // "Installing…" with no retry.
            return InstallOutcome.Failed("The install session was no longer valid: ${e.message}", blocked = false)
        } catch (e: IllegalArgumentException) {
            // With targetSdk 35+ an immutable PendingIntent makes commit throw
            // this; see statusIntentSender.
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
                // The system aborts both when the owner cancels and when it
                // refuses the session by policy; in the second case its message
                // is the only clue, so show it when present.
                val fromSystem = event.message
                if (fromSystem.isNullOrBlank()) {
                    InstallOutcome.Failed("Installation canceled.", blocked = false)
                } else {
                    InstallOutcome.Failed(fromSystem, blocked = false)
                }
            }
            else -> InstallOutcome.Failed(
                event.message ?: "the installation failed (status ${event.status})",
                blocked = false,
            )
        }
    }

    /**
     * The `PendingIntent` MUST be mutable: the system fills
     * `EXTRA_STATUS`/`EXTRA_STATUS_MESSAGE` into it, and with `targetSdk` 35+ an
     * immutable one makes `commit()` throw. Lint suggests immutable, which is wrong
     * here. The Intent is explicit (`setPackage` plus class), so only the system
     * can address it.
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
        // Distinct codes per phase, or the second PendingIntent would replace the first.
        sessionId * 2 + if (phase == PHASE_PREAPPROVAL) 0 else 1

    companion object {
        /**
         * Suffix of the update FileProvider authority. Must match
         * `android:authorities` in the :data manifest, or `getUriForFile` throws
         * and the system-installer fallback silently disappears.
         */
        private const val AUTHORITY_SUFFIX = ".atualizacao"

        const val PHASE_PREAPPROVAL = "preaprovacao"
        const val PHASE_COMMIT = "instalacao"
        private const val APK_ENTRY_NAME = "base.apk"
        private const val COPY_BUFFER_BYTES = 64 * 1024
    }
}
