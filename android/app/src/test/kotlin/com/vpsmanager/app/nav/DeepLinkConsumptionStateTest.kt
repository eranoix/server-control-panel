package com.vpsmanager.app.nav

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DeepLinkConsumptionStateTest {

    @Test
    fun `first consumeOnce returns the route and marks the state consumed`() {
        val state = DeepLinkConsumptionState()

        assertEquals("admin/scheduler.jobs", state.consumeOnce("admin/scheduler.jobs"))
        assertTrue(state.consumed)
    }

    @Test
    fun `a second consumeOnce without reset returns null`() {
        val state = DeepLinkConsumptionState()
        state.consumeOnce("admin/scheduler.jobs")

        assertNull(state.consumeOnce("admin/scheduler.jobs"))
    }

    @Test
    fun `reset allows a fresh route to be consumed again — the onNewIntent case`() {
        val state = DeepLinkConsumptionState()
        state.consumeOnce("admin/scheduler.jobs")

        state.reset()

        assertEquals("admin/scheduler.jobs", state.consumeOnce("admin/scheduler.jobs"))
    }

    @Test
    fun `a state restored already-consumed never re-navigates — the rotation case`() {
        // Models MainActivity restoring `consumed = true` from onSaveInstanceState after a
        // configuration change recreates the activity with the same launch Intent.
        val state = DeepLinkConsumptionState(consumed = true)

        assertNull(state.consumeOnce("admin/scheduler.jobs"))
    }

    @Test
    fun `consumeOnce still marks the state consumed even when the resolved route is null`() {
        val state = DeepLinkConsumptionState()

        assertNull(state.consumeOnce(null))
        assertTrue(state.consumed)
        assertNull(state.consumeOnce("admin/scheduler.jobs"))
    }
}
