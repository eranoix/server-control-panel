package com.vpsmanager.feature.videocall.call

import android.content.ComponentName
import android.os.Bundle
import android.telecom.PhoneAccountHandle
import com.vpsmanager.data.videocall.ActiveCallRegistry
import com.vpsmanager.data.videocall.RingingCallHandle
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

private class FakeTelecomCallPort : TelecomCallPort {
    var calls = mutableListOf<Pair<PhoneAccountHandle, Bundle>>()
        private set
    var throwOnNextCall: SecurityException? = null

    override fun addNewIncomingCall(phoneAccountHandle: PhoneAccountHandle, extras: Bundle) {
        throwOnNextCall?.let {
            throwOnNextCall = null
            throw it
        }
        calls += phoneAccountHandle to extras
    }
}

private class RecordingAccountPort : TelecomAccountPort {
    var registerCallCount = 0
        private set

    override fun isRegistered(handle: PhoneAccountHandle): Boolean = registerCallCount > 0
    override fun register(account: android.telecom.PhoneAccount) {
        registerCallCount++
    }
}

@RunWith(RobolectricTestRunner::class)
class TelecomIncomingCallHandlerTest {

    private val componentName = ComponentName("com.vpsmanager.app", "com.vpsmanager.feature.videocall.call.VpsmConnectionService")
    private val accountPort = RecordingAccountPort()
    private val registrar = PhoneAccountRegistrar(componentName, accountPort)
    private val callPort = FakeTelecomCallPort()
    private val handler = TelecomIncomingCallHandler(registrar, callPort)

    @After
    fun tearDown() {
        ActiveCallRegistry.unregister("c1")
    }

    @Test
    fun `onIncomingCall registers the phone account then adds exactly one Telecom call with all extras`() {
        handler.onIncomingCall(roomId = "r1", roomName = "Sala", from = "alice", callId = "c1")

        assertEquals(1, accountPort.registerCallCount)
        assertEquals(1, callPort.calls.size)
        val (handle, extras) = callPort.calls[0]
        assertEquals(registrar.phoneAccountHandle, handle)
        assertEquals("r1", extras.getString(EXTRA_ROOM_ID))
        assertEquals("Sala", extras.getString(EXTRA_ROOM_NAME))
        assertEquals("alice", extras.getString(EXTRA_CALLER_NAME))
        assertEquals("c1", extras.getString(EXTRA_CALL_ID))
    }

    @Test
    fun `onIncomingCall does not crash when Telecom rejects the call`() {
        callPort.throwOnNextCall = SecurityException("conta desabilitada")

        // Must not throw — a rejected call is dropped silently (logged), same posture as a
        // missing/incomplete FCM payload upstream in VpsFirebaseMessagingService.
        handler.onIncomingCall(roomId = "r1", roomName = "Sala", from = "alice", callId = "c1")
    }

    @Test
    fun `onCallEnded looks up ActiveCallRegistry and ends the matching call`() {
        var ended = false
        ActiveCallRegistry.register(
            "c1",
            object : RingingCallHandle {
                override fun endCall() {
                    ended = true
                }
            },
        )

        handler.onCallEnded("c1")

        assertTrue(ended)
    }
}
