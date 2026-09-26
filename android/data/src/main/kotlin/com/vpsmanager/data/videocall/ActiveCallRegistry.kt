package com.vpsmanager.data.videocall

/**
 * What a registered ringing/active call can be told to do — implemented by
 * `:feature-videocall`'s `VpsmConnection` so [ActiveCallRegistry] itself never needs to know
 * about `android.telecom.Connection`.
 */
interface RingingCallHandle {
    /** The other side ended the call (or it must be torn down for any other reason): disconnect
     * Telecom's `Connection` and release the local WebRTC/foreground-service resources. */
    fun endCall()
}

/**
 * Maps a call's `call_id` (`internal/videocall/ring.go`'s `announceJoin`/`broadcastCallEnded`
 * both carry it — see [IncomingCallHandler]) to the live [RingingCallHandle] currently
 * representing it on this device, so a `call-ended` FCM push (sent when the caller hangs up
 * before this device answers, or the other side leaves mid-call) can find and disconnect the
 * exact matching Telecom connection — never "the most recently registered call", which would
 * race two overlapping ring events on a multi-line-capable device.
 */
object ActiveCallRegistry {
    private val calls = mutableMapOf<String, RingingCallHandle>()

    @Synchronized
    fun register(callId: String, handle: RingingCallHandle) {
        calls[callId] = handle
    }

    @Synchronized
    fun unregister(callId: String) {
        calls.remove(callId)
    }

    /**
     * True if [callId] had a registered handle (now removed and told to end). False if it had
     * already ended/been answered/was never registered on this device — a safe no-op either way,
     * never an error: the FCM message this drives can race a device that already tore the call
     * down through its own local path.
     */
    @Synchronized
    fun endCall(callId: String): Boolean {
        val handle = calls.remove(callId) ?: return false
        handle.endCall()
        return true
    }
}
