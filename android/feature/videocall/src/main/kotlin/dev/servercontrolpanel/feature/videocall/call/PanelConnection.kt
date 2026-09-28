package dev.servercontrolpanel.feature.videocall.call

import android.content.Context
import android.content.Intent
import android.telecom.Connection
import android.telecom.DisconnectCause
import android.util.Log
import dev.servercontrolpanel.data.videocall.ActiveCallRegistry
import dev.servercontrolpanel.data.videocall.RingingCallHandle
import dev.servercontrolpanel.feature.videocall.AndroidCallPermissionChecker
import dev.servercontrolpanel.feature.videocall.CallPermissionChecker

private const val TAG = "PanelConnection"

private fun launchHostActivity(context: Context, roomId: String) {
    val intent = context.packageManager.getLaunchIntentForPackage(context.packageName) ?: return
    intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
    intent.putExtra(EXTRA_ROOM_ID, roomId)
    context.startActivity(intent)
}

class PanelConnection(
    private val context: Context,
    private val callId: String,
    private val roomId: String,
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
    }

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
