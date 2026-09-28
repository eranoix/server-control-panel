package dev.servercontrolpanel.data.videocall

interface RingingCallHandle {
    fun endCall()
}

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

    @Synchronized
    fun endCall(callId: String): Boolean {
        val handle = calls.remove(callId) ?: return false
        handle.endCall()
        return true
    }
}
