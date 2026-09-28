package dev.servercontrolpanel.data.videocall

interface IncomingCallHandler {
    fun onIncomingCall(roomId: String, roomName: String, from: String, callId: String)

    fun onCallEnded(callId: String)
}

object IncomingCallDispatcher {
    @Volatile
    var handler: IncomingCallHandler? = null
}
