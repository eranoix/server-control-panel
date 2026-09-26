package com.vpsmanager.data.update

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.util.Log

/**
 * Receives `PackageInstaller` results. Declared in the manifest because
 * installing our own APK kills this process: a runtime receiver would die with it
 * and lose the failure message, while the system recreates the process to
 * deliver a manifest receiver's broadcast and [UpdateDiagnostics] writes it to
 * disk. Not exported: only the system, answering our explicit `PendingIntent`, gets here.
 */
class UpdateInstallReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_INSTALL_STATUS) return

        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, Int.MIN_VALUE)
        val sessionId = intent.getIntExtra(PackageInstaller.EXTRA_SESSION_ID, -1)
        val phase = intent.getStringExtra(EXTRA_PHASE) ?: ApkInstaller.PHASE_COMMIT
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)

        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            // The system wants to show its confirmation dialog. Not an outcome:
            // the real result comes in a second broadcast, so nothing goes on the bus.
            val confirmation = confirmationIntent(intent)
            if (confirmation == null) {
                UpdateDiagnostics.record(
                    context,
                    "Update: the system asked for confirmation but did not send the screen to open.",
                )
                return
            }
            try {
                context.startActivity(confirmation.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (e: android.content.ActivityNotFoundException) {
                UpdateDiagnostics.record(context, "Update: could not open the confirmation: ${e.message}")
            }
            return
        }

        if (status != PackageInstaller.STATUS_SUCCESS) {
            UpdateDiagnostics.record(
                context,
                buildString {
                    append("Update — ")
                    append(if (phase == ApkInstaller.PHASE_PREAPPROVAL) "pre-approval" else "installation")
                    append(" failed (status $status)")
                    if (!message.isNullOrBlank()) {
                        append('\n')
                        append(message)
                    }
                },
            )
            Log.w(TAG, "install failed: phase=$phase status=$status msg=$message")
        }

        InstallStatusBus.publish(
            InstallStatusEvent(sessionId = sessionId, phase = phase, status = status, message = message),
        )
    }

    @Suppress("DEPRECATION")
    private fun confirmationIntent(intent: Intent): Intent? =
        // The @Suppress covers the deprecated signature some compile SDKs still
        // flag; minSdk 34 has the typed overload.
        intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)

    companion object {
        const val ACTION_INSTALL_STATUS = "com.vpsmanager.data.update.INSTALL_STATUS"
        const val EXTRA_PHASE = "com.vpsmanager.data.update.EXTRA_PHASE"
        private const val TAG = "VpsmUpdate"
    }
}
