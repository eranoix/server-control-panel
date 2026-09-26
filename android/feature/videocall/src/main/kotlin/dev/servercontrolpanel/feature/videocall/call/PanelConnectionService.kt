package dev.servercontrolpanel.feature.videocall.call

import android.net.Uri
import android.os.Bundle
import android.telecom.Connection
import android.telecom.ConnectionRequest
import android.telecom.ConnectionService
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager
import android.util.Log

private const val TAG = "PanelConnectionService"
private const val ROOM_URI_SCHEME = "panel-room"

/**
 * Self-managed `ConnectionService` (`MANAGE_OWN_CALLS`). Registered via [PhoneAccountRegistrar], it
 * makes Telecom treat our calls as real calls (lock-screen UI, ringtone, DND bypass, Bluetooth).
 * Exporting it is safe because `BIND_TELECOM_CONNECTION_SERVICE` is enforced by the system.
 */
class PanelConnectionService : ConnectionService() {

    override fun onCreateIncomingConnection(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest,
    ): Connection {
        val extras: Bundle = request.extras ?: Bundle()
        val roomId = extras.getString(EXTRA_ROOM_ID).orEmpty()
        val roomName = extras.getString(EXTRA_ROOM_NAME).orEmpty()
        val callerName = extras.getString(EXTRA_CALLER_NAME).orEmpty()
        val callId = extras.getString(EXTRA_CALL_ID).orEmpty()

        return PanelConnection(context = applicationContext, callId = callId, roomId = roomId).apply {
            setCallerDisplayName(callerName, TelecomManager.PRESENTATION_ALLOWED)
            setAddress(Uri.fromParts(ROOM_URI_SCHEME, roomId, roomName), TelecomManager.PRESENTATION_ALLOWED)
            setRinging()
        }
    }

    override fun onCreateIncomingConnectionFailed(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest?,
    ) {
        // Telecom refused to ring (e.g. a cellular call is active on some OEMs, or the account was
        // revoked after registration). Nothing to tear down; this device just misses the ring.
        Log.w(TAG, "Telecom refused the incoming connection, call did not ring")
    }

    /**
     * Only for `ConnectionService` completeness: the app never calls `placeCall`, since outgoing
     * calls join directly through `CallViewModel.joinRoom`.
     */
    override fun onCreateOutgoingConnection(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest,
    ): Connection {
        val roomId = request.address?.schemeSpecificPart.orEmpty()
        return PanelConnection(context = applicationContext, callId = roomId, roomId = roomId).apply {
            setDialing()
            setActive()
        }
    }

    override fun onCreateOutgoingConnectionFailed(
        connectionManagerPhoneAccount: PhoneAccountHandle?,
        request: ConnectionRequest?,
    ) {
        Log.w(TAG, "Telecom refused the outgoing connection")
    }
}
