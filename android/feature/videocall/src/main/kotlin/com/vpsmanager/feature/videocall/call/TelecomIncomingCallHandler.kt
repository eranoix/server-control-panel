package com.vpsmanager.feature.videocall.call

import android.content.Context
import android.os.Bundle
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager
import android.util.Log
import com.vpsmanager.data.videocall.ActiveCallRegistry
import com.vpsmanager.data.videocall.IncomingCallHandler

private const val TAG = "TelecomIncomingCall"

/** Extras keys [VpsmConnectionService.onCreateIncomingConnection] reads off the request Telecom
 * hands back — set here, consumed there, never guessed at a distance. */
const val EXTRA_ROOM_ID = "com.vpsmanager.feature.videocall.EXTRA_ROOM_ID"
const val EXTRA_ROOM_NAME = "com.vpsmanager.feature.videocall.EXTRA_ROOM_NAME"
const val EXTRA_CALLER_NAME = "com.vpsmanager.feature.videocall.EXTRA_CALLER_NAME"
const val EXTRA_CALL_ID = "com.vpsmanager.feature.videocall.EXTRA_CALL_ID"

/**
 * Narrow seam over the one `TelecomManager` member [TelecomIncomingCallHandler] needs —
 * matches [TelecomAccountPort]'s pattern so this class stays testable on the plain JVM under
 * Robolectric without a real Telecom binder.
 */
interface TelecomCallPort {
    fun addNewIncomingCall(phoneAccountHandle: PhoneAccountHandle, extras: Bundle)
}

private class RealTelecomCallPort(private val telecomManager: TelecomManager) : TelecomCallPort {
    override fun addNewIncomingCall(phoneAccountHandle: PhoneAccountHandle, extras: Bundle) {
        telecomManager.addNewIncomingCall(phoneAccountHandle, extras)
    }
}

/**
 * The one production [IncomingCallHandler], registered against
 * [com.vpsmanager.data.videocall.IncomingCallDispatcher] in `VpsManagerApplication.onCreate()`.
 * Turns an FCM ring event into a real Telecom call (so the OS renders its own lock-screen UI —
 * see [VpsmConnectionService]) and a `call-ended` push into a lookup against
 * [ActiveCallRegistry] so a still-ringing/active call on this device is torn down without
 * waiting out the ring message's own TTL.
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
        // Re-checked on every ring, not just at app startup — see PhoneAccountRegistrar's own
        // doc for why a previously-registered account can be silently revoked later.
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
            // The account Telecom now reports as "registered" can still reject a specific call
            // (e.g. a real cellular call already occupying the self-managed slot on some OEMs,
            // or the account having just been disabled between ensureRegistered() and this call)
            // — this device silently misses the ring rather than crashing the FCM-delivery path.
            Log.w(TAG, "addNewIncomingCall recusado pelo Telecom para call=$callId", e)
        }
    }

    override fun onCallEnded(callId: String) {
        ActiveCallRegistry.endCall(callId)
    }
}
