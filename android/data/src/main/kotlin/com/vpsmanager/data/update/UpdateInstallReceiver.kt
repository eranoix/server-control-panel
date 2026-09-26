package com.vpsmanager.data.update

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.util.Log

/**
 * Receives the `PackageInstaller` results.
 *
 * ### Why it is declared in the manifest and not registered at runtime
 * Installing our own APK kills this process. A receiver registered at runtime
 * would die with it and the failure message — the only thing that explains an
 * "app not installed" to someone without `adb` — would be lost. Declared in
 * the manifest, the system recreates the process just to deliver it, and
 * [UpdateDiagnostics] writes it to disk before any screen has to exist.
 *
 * It is not exported: only the system, answering the explicit `PendingIntent`
 * we created ourselves, gets here.
 */
class UpdateInstallReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_INSTALL_STATUS) return

        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, Int.MIN_VALUE)
        val sessionId = intent.getIntExtra(PackageInstaller.EXTRA_SESSION_ID, -1)
        val phase = intent.getStringExtra(EXTRA_PHASE) ?: ApkInstaller.PHASE_COMMIT
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)

        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            // The system wants to show the confirmation dialog and handed us
            // the Intent that opens it. It is NOT an outcome: the real result
            // arrives in a second broadcast, so nothing goes on the bus.
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
            Log.w(TAG, "instalacao falhou: fase=$phase status=$status msg=$message")
        }

        InstallStatusBus.publish(
            InstallStatusEvent(sessionId = sessionId, phase = phase, status = status, message = message),
        )
    }

    @Suppress("DEPRECATION")
    private fun confirmationIntent(intent: Intent): Intent? =
        // getParcelableExtra(String, Class) only exists from API 33 on and the
        // minSdk is 34, but the extra arrives typed as an Intent from the
        // system; the typed form is used and the @Suppress covers the old
        // signature that lint still sees in some compile SDKs.
        intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)

    companion object {
        const val ACTION_INSTALL_STATUS = "com.vpsmanager.data.update.INSTALL_STATUS"
        const val EXTRA_PHASE = "com.vpsmanager.data.update.EXTRA_PHASE"
        private const val TAG = "VpsmUpdate"
    }
}
