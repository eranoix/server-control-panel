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
 * Launches this app's own launcher activity with [EXTRA_ROOM_ID] set, so the in-app UI (not
 * Telecom, which only ever renders the system call screen) can pick the call up once
 * foregrounded. `:feature-videocall` cannot import `:app`'s `MainActivity` directly (feature
 * modules are dependency-free siblings of `:app`, never the reverse), so this goes through the
 * package's own launch intent instead of a concrete Activity class reference — the same
 * seam every other cross-module "open the app" call site in this codebase would need.
 *
 * `MainActivity.consumeDeepLink` reads [EXTRA_ROOM_ID] off this exact Intent and resolves it to
 * `AppNavHost`'s real `chamada/{roomId}` destination (`CallScreen`), through the same
 * single-consumption `pendingDeepLinkRoute` path a tapped notification's route uses —
 * answering a call from the lock screen now lands the user on the actual call screen, not a
 * placeholder.
 */
private fun launchHostActivity(context: Context, roomId: String) {
    val intent = context.packageManager.getLaunchIntentForPackage(context.packageName) ?: return
    intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
    intent.putExtra(EXTRA_ROOM_ID, roomId)
    context.startActivity(intent)
}

/**
 * One self-managed Telecom call (`PROPERTY_SELF_MANAGED`) — Telecom itself renders the
 * lock-screen/ringtone/DND-bypass UI for it (CallStyle is applied automatically to self-managed
 * connections on API 31+), so [onShowIncomingCallUi] is intentionally a no-op: building a second,
 * competing UI here would fight the OS's own rendering instead of relying on it.
 *
 * [onAnswer] deliberately does NOT re-run WebRTC join/negotiation itself —
 * [com.vpsmanager.feature.videocall.CallViewModel.joinRoom] remains the one join use-case,
 * converged rather than duplicated (this plan's own key_links requirement). Instead it: confirms
 * CAMERA+RECORD_AUDIO are already granted (required before [CallForegroundService] can start
 * with the camera/microphone FGS types — `startForeground()` throws `SecurityException`
 * otherwise), starts [CallForegroundService], marks the Telecom call `setActive()`, and launches
 * this app's own UI via [launchHostActivity] so it can complete the join once foregrounded.
 *
 * Implements [RingingCallHandle] so [ActiveCallRegistry] can end this exact call when a
 * `call-ended` FCM push arrives while it is still ringing or active on this device.
 */
class VpsmConnection(
    private val context: Context,
    private val callId: String,
    private val roomId: String,
    // Overridable seam (mirrors CallViewModel's own constructor pattern) so
    // LockScreenAnswerDeclineTest can drive both the granted and denied branch deterministically
    // instead of depending on real runtime grant state / adb shell pm revoke from inside a test.
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
            // The lobby's proactive permission request is meant to prevent this, but a user
            // can always revoke CAMERA/RECORD_AUDIO afterward under system Settings — a
            // ring-time denial here is a real, reachable state, not a theoretical one.
            Log.w(TAG, "resposta recusada, permissao ausente: $missing")
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
        // Intentional no-op — see class doc: Telecom already renders the call UI itself.
    }

    /** [ActiveCallRegistry]'s callback for a `call-ended` push arriving before/during this call. */
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
