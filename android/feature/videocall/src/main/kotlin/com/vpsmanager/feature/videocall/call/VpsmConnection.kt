package com.vpsmanager.feature.videocall.call

import android.content.Context
import android.content.Intent
import android.telecom.Connection
import android.telecom.DisconnectCause
import android.util.Log
import com.vpsmanager.data.videocall.ActiveCallRegistry
import com.vpsmanager.data.videocall.RingingCallHandle
import com.vpsmanager.feature.videocall.AndroidCallPermissionChecker
import com.vpsmanager.feature.videocall.CallPermissionChecker

private const val TAG = "VpsmConnection"

/**
 * Opens the app's launch activity with [EXTRA_ROOM_ID] so the in-app call screen can take over.
 * Uses the package launch intent because feature modules cannot depend on `:app`'s MainActivity;
 * `MainActivity.consumeDeepLink` routes the extra to the call screen.
 */
private fun launchHostActivity(context: Context, roomId: String) {
    val intent = context.packageManager.getLaunchIntentForPackage(context.packageName) ?: return
    intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
    intent.putExtra(EXTRA_ROOM_ID, roomId)
    context.startActivity(intent)
}

/**
 * One self-managed Telecom call. Telecom renders the ringing UI itself, so
 * [onShowIncomingCallUi] is a no-op to avoid a competing UI.
 *
 * [onAnswer] does not join WebRTC itself ([com.vpsmanager.feature.videocall.CallViewModel.joinRoom]
 * is the only join path). It checks CAMERA and RECORD_AUDIO (otherwise `startForeground()` throws
 * `SecurityException`), starts [CallForegroundService], marks the call active and opens the app.
 *
 * As a [RingingCallHandle], [ActiveCallRegistry] can end it when a `call-ended` push arrives.
 */
class VpsmConnection(
    private val context: Context,
    private val callId: String,
    private val roomId: String,
    // Injectable so tests can drive the granted and denied branches deterministically.
    private val permissionChecker: CallPermissionChecker = AndroidCallPermissionChecker(context),
    private val launchHostActivity: (Context, String) -> Unit = ::launchHostActivity,
) : Connection(), RingingCallHandle {

    init {
        connectionProperties = PROPERTY_SELF_MANAGED
        connectionCapabilities = CAPABILITY_MUTE or CAPABILITY_SUPPORT_HOLD
        ActiveCallRegistry.register(callId, this)
    }

    override fun onAnswer() {
        val missing = permissionChecker.missingPermissions()
        if (missing.isNotEmpty()) {
            // The user can revoke permissions in Settings after the lobby granted them.
            Log.w(TAG, "answer refused, missing permission: $missing")
            teardown(DisconnectCause(DisconnectCause.ERROR, "missing_permission"))
            return
        }
        CallForegroundService.start(context)
        setActive()
        launchHostActivity(context, roomId)
    }

    override fun onReject() {
        teardown(DisconnectCause(DisconnectCause.REJECTED))
    }

    override fun onDisconnect() {
        teardown(DisconnectCause(DisconnectCause.LOCAL))
    }

    override fun onShowIncomingCallUi() {
        // No-op: Telecom renders the call UI.
    }

    /** Called by [ActiveCallRegistry] when a `call-ended` push arrives. */
    override fun endCall() {
        teardown(DisconnectCause(DisconnectCause.REMOTE))
    }

    private fun teardown(cause: DisconnectCause) {
        CallForegroundService.stop(context)
        setDisconnected(cause)
        destroy()
        ActiveCallRegistry.unregister(callId)
    }
}
