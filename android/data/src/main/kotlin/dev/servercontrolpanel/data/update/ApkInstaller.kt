package dev.servercontrolpanel.data.update

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

data class InstallStatusEvent(
    val sessionId: Int,
    val phase: String,
    val status: Int,
    val message: String?,
)

object InstallStatusBus {
    private val _events = MutableSharedFlow<InstallStatusEvent>(replay = 8, extraBufferCapacity = 16)
    val events: SharedFlow<InstallStatusEvent> = _events

    fun publish(event: InstallStatusEvent) {
        _events.tryEmit(event)
    }
}

sealed interface PreapprovalOutcome {
    data object Approved : PreapprovalOutcome

    data object Declined : PreapprovalOutcome

    data class Unsupported(val detail: String) : PreapprovalOutcome
}

sealed interface InstallOutcome {
    data object Committed : InstallOutcome

    data class Failed(val message: String, val blocked: Boolean) : InstallOutcome
}

class ApkInstaller(context: Context) : ApkInstallerPort {

    private val appContext = context.applicationContext
    private val installer: PackageInstaller
        get() = appContext.packageManager.packageInstaller

    override fun canInstallFromUnknownSources(): Boolean = appContext.packageManager.canRequestPackageInstalls()

    override fun installSourceKnown(): Boolean = try {
        appContext.packageManager
            .getInstallSourceInfo(appContext.packageName)
            .installingPackageName != null
    } catch (e: PackageManager.NameNotFoundException) {
        false
    }

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

    fun unknownSourcesSettingsIntent(): Intent =
        Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${appContext.packageName}"))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    override fun createSession(apkSizeBytes: Long, declarePackage: Boolean): Int? = try {
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            if (declarePackage) {
                setAppPackageName(appContext.packageName)
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
        }
    }

    override suspend fun requestPreapproval(sessionId: Int, label: CharSequence): PreapprovalOutcome {
        val details = try {
            PackageInstaller.PreapprovalDetails.Builder()
                .setPackageName(appContext.packageName)
                .setLabel(label)
                .setLocale(ULocale.US)
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
            return InstallOutcome.Failed("The install session was no longer valid: ${e.message}", blocked = false)
        } catch (e: IllegalArgumentException) {
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
        sessionId * 2 + if (phase == PHASE_PREAPPROVAL) 0 else 1

    companion object {
        private const val AUTHORITY_SUFFIX = ".update"

        const val PHASE_PREAPPROVAL = "preapproval"
        const val PHASE_COMMIT = "install"
        private const val APK_ENTRY_NAME = "base.apk"
        private const val COPY_BUFFER_BYTES = 64 * 1024
    }
}
