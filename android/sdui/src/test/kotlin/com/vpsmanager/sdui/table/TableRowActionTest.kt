package com.vpsmanager.sdui.table

import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.sdui.actionrunner.Confirmation
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * `rowActionInvocation` is the decision `TableComponent`'s row-action menu
 * dispatches through -- this is what proves a table row action reaches
 * `ActionRunner` shaped the way the server expects, without needing any
 * Compose test infrastructure (this module has none -- see `ScreenState`,
 * `ActionRunner`, `bindErrors` for the same separation).
 */
class TableRowActionTest {

    private fun confirmDestructive(actionId: String) = SduiComponent.ConfirmDestructive(
        id = "confirm-$actionId",
        actionId = actionId,
        message = "Tem certeza?",
    )

    @Test
    fun `a row action carries the row's own id under params, matching what scheduler_actions_go reads`() {
        val row = JsonObject(mapOf("id" to JsonPrimitive("job-1"), "status" to JsonPrimitive("idle")))

        val invocation = rowActionInvocation(
            actionId = "scheduler.job.run_now",
            row = row,
            confirmation = null,
            confirmations = emptyMap(),
        )

        assertEquals("job-1", invocation?.params?.get("id"))
        assertEquals("scheduler.job.run_now", invocation?.actionId)
    }

    @Test
    fun `an action declared in confirmations is marked destructive, one that is not is marked safe`() {
        val row = JsonObject(mapOf("id" to JsonPrimitive("job-1")))
        val confirmations = mapOf("scheduler.job.delete" to confirmDestructive("scheduler.job.delete"))

        val destructive = rowActionInvocation("scheduler.job.delete", row, Confirmation(confirmed = true), confirmations)
        val safe = rowActionInvocation("scheduler.job.run_now", row, null, confirmations)

        assertTrue(destructive?.destructive == true)
        assertEquals(true, destructive?.confirmation?.confirmed)
        assertTrue(safe?.destructive == false)
        assertNull(safe?.confirmation)
    }

    @Test
    fun `a row with no id builds no invocation rather than guessing`() {
        val row = JsonObject(mapOf("status" to JsonPrimitive("idle")))

        val invocation = rowActionInvocation("scheduler.job.run_now", row, null, emptyMap())

        assertNull(invocation)
    }
}
