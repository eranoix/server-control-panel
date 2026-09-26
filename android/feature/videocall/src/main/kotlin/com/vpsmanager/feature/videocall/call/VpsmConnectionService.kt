package com.vpsmanager.feature.videocall.call

import android.net.Uri
import android.os.Bundle
import android.telecom.Connection
import android.telecom.ConnectionRequest
import android.telecom.ConnectionService
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager
import android.util.Log

private const val TAG = "VpsmConnectionService"
private const val ROOM_URI_SCHEME = "vpsm-room"

/**
 * Self-managed `android.telecom.ConnectionService` (`MANAGE_OWN_CALLS`) — registering this via
 * [PhoneAccountRegistrar] is what makes Telecom treat calls from this app as real calls (native
 * lock-screen UI, ringtone, DND bypass, Bluetooth routing), not just call-shaped notifications.
 * `android:exported="true"` with `BIND_TELECOM_CONNECTION_SERVICE` is required and safe — that
 * permission is enforced by the system, not this app (reviewed and accepted).
 */
class VpsmConnectionService : ConnectionService() {

    override fun onCreateIncomingConnection(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest,
    ): Connection {
        val extras: Bundle = request.extras ?: Bundle()
        val roomId = extras.getString(EXTRA_ROOM_ID).orEmpty()
        val roomName = extras.getString(EXTRA_ROOM_NAME).orEmpty()
        val callerName = extras.getString(EXTRA_CALLER_NAME).orEmpty()
        val callId = extras.getString(EXTRA_CALL_ID).orEmpty()

        return VpsmConnection(context = applicationContext, callId = callId, roomId = roomId).apply {
            setCallerDisplayName(callerName, TelecomManager.PRESENTATION_ALLOWED)
            setAddress(Uri.fromParts(ROOM_URI_SCHEME, roomId, roomName), TelecomManager.PRESENTATION_ALLOWED)
            setRinging()
        }
    }

    override fun onCreateIncomingConnectionFailed(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest?,
    ) {
        // Telecom refused to let this call ring at all — e.g. a real cellular call already
        // occupies the self-managed slot on some OEMs, or MANAGE_OWN_CALLS/the registered
        // PhoneAccount was revoked between PhoneAccountRegistrar.ensureRegistered() and this
        // callback (T-11 risk register). Nothing to tear down beyond what Telecom itself already
        // discards; this device simply misses the ring, same as a rejected addNewIncomingCall.
        Log.w(TAG, "Telecom recusou a conexao de entrada, chamada nao tocou")
    }

    /**
     * No current call site in this app places an outgoing call through Telecom —
     * `CallViewModel.joinRoom` joins directly over the existing signaling/WebRTC path without
     * `TelecomManager.placeCall`, since an outgoing call is always initiated from inside the
     * app's own UI (the user is already looking at the phone, unlike an incoming ring). This
     * override exists for `ConnectionService` contract completeness — `MANAGE_OWN_CALLS` self-
     * managed apps that never call `placeCall` never have it invoked — not to serve a real path
     * today.
     */
    override fun onCreateOutgoingConnection(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest,
    ): Connection {
        val roomId = request.address?.schemeSpecificPart.orEmpty()
        return VpsmConnection(context = applicationContext, callId = roomId, roomId = roomId).apply {
            setDialing()
            setActive()
        }
    }

    override fun onCreateOutgoingConnectionFailed(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest?,
    ) {
        Log.w(TAG, "Telecom recusou a conexao de saida")
    }
}
