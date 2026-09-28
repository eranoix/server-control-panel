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
        Log.w(TAG, "Telecom refused the incoming connection, call did not ring")
    }

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
