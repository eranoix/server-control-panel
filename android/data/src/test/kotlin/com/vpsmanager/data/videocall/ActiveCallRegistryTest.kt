package com.vpsmanager.data.videocall

import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

private class FakeRingingCallHandle : RingingCallHandle {
    var endCallCallCount = 0
        private set

    override fun endCall() {
        endCallCallCount++
    }
}

class ActiveCallRegistryTest {

    @After
    fun tearDown() {
        // ActiveCallRegistry is a process-wide singleton — leaking a registration across tests
        // would make a later test's "unknown callId" assertion depend on execution order.
        ActiveCallRegistry.unregister("call-1")
        ActiveCallRegistry.unregister("call-2")
    }

    @Test
    fun `endCall disconnects the registered handle for that call id`() {
        val handle = FakeRingingCallHandle()
        ActiveCallRegistry.register("call-1", handle)

        val handled = ActiveCallRegistry.endCall("call-1")

        assertTrue(handled)
        assertTrue(handle.endCallCallCount == 1)
    }

    @Test
    fun `endCall on an unknown call id is a no-op, not an error`() {
        val handled = ActiveCallRegistry.endCall("never-registered")

        assertFalse(handled)
    }

    @Test
    fun `endCall only ends the matching call, not every registered call`() {
        val ringing = FakeRingingCallHandle()
        val other = FakeRingingCallHandle()
        ActiveCallRegistry.register("call-1", ringing)
        ActiveCallRegistry.register("call-2", other)

        ActiveCallRegistry.endCall("call-1")

        assertTrue(ringing.endCallCallCount == 1)
        assertTrue(other.endCallCallCount == 0)
    }

    @Test
    fun `endCall is idempotent — a second call for the same id is a safe no-op`() {
        val handle = FakeRingingCallHandle()
        ActiveCallRegistry.register("call-1", handle)

        ActiveCallRegistry.endCall("call-1")
        val secondCallHandled = ActiveCallRegistry.endCall("call-1")

        assertFalse(secondCallHandled)
        assertTrue(handle.endCallCallCount == 1)
    }
}
