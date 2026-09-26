package dev.servercontrolpanel.feature.videocall.call

import android.content.Context
import android.os.Bundle
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager
import android.util.Log
import dev.servercontrolpanel.data.videocall.ActiveCallRegistry
import dev.servercontrolpanel.data.videocall.IncomingCallHandler

private const val TAG = "TelecomIncomingCall"

/** Extras keys set here and read by [PanelConnectionService.onCreateIncomingConnection]. */
const val EXTRA_ROOM_ID = "dev.servercontrolpanel.feature.videocall.EXTRA_ROOM_ID"
const val EXTRA_ROOM_NAME = "dev.servercontrolpanel.feature.videocall.EXTRA_ROOM_NAME"
const val EXTRA_CALLER_NAME = "dev.servercontrolpanel.feature.videocall.EXTRA_CALLER_NAME"
const val EXTRA_CALL_ID = "dev.servercontrolpanel.feature.videocall.EXTRA_CALL_ID"

/** The one `TelecomManager` call [TelecomIncomingCallHandler] needs, abstracted for tests. */
interface TelecomCallPort {
    fun addNewIncomingCall(phoneAccountHandle: PhoneAccountHandle, extras: Bundle)
}

private class RealTelecomCallPort(private val telecomManager: TelecomManager) : TelecomCallPort {
    override fun addNewIncomingCall(phoneAccountHandle: PhoneAccountHandle, extras: Bundle) {
        telecomManager.addNewIncomingCall(phoneAccountHandle, extras)
    }
}

/**
 * Production [IncomingCallHandler], registered at app start. Turns an FCM ring into a Telecom call
 * (the OS renders the lock-screen UI) and a `call-ended` push into ending the call via
 * [ActiveCallRegistry].
 */
class TelecomIncomingCallHandler(
    private val registrar: PhoneAccountRegistrar,
    private val port: TelecomCallPort,
) : IncomingCallHandler {

    constructor(context: Context, registrar: PhoneAccountRegistrar) : this(
        registrar = registrar,
        port = RealTelecomCallPort(context.getSystemService(TelecomManager::class.java)),
    )

    override fun onIncomingCall(roomId: String, roomName: String, from: String, callId: String) {
        // Re-checked on every ring because a registered account can be revoked later.
        registrar.ensureRegistered()
        val extras = Bundle().apply {
            putString(EXTRA_ROOM_ID, roomId)
            putString(EXTRA_ROOM_NAME, roomName)
            putString(EXTRA_CALLER_NAME, from)
            putString(EXTRA_CALL_ID, callId)
        }
        try {
            port.addNewIncomingCall(registrar.phoneAccountHandle, extras)
        } catch (e: SecurityException) {
            // Telecom can still reject a call (cellular call active, account just disabled);
            // miss the ring rather than crash the FCM path.
            Log.w(TAG, "addNewIncomingCall rejected by Telecom for call=$callId", e)
        }
    }

    override fun onCallEnded(callId: String) {
        ActiveCallRegistry.endCall(callId)
    }
}
