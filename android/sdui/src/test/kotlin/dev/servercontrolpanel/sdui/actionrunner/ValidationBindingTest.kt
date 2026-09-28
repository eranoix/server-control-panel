package dev.servercontrolpanel.sdui.actionrunner

import dev.servercontrolpanel.core.sdui.SduiActionRef
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.core.sdui.SduiDataSource
import dev.servercontrolpanel.core.sdui.SduiFormField
import dev.servercontrolpanel.core.sdui.SduiJson
import dev.servercontrolpanel.core.sdui.SduiScreen
import dev.servercontrolpanel.core.sdui.SduiValidationError
import dev.servercontrolpanel.sdui.form.bindErrors
import dev.servercontrolpanel.sdui.form.FormErrorState
import dev.servercontrolpanel.sdui.registry.SduiFixtures
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ValidationBindingTest {

    private fun jobForm() = SduiComponent.Form(
        id = "job-form",
        fields = listOf(
            SduiFormField(key = "name", label = "Name", kind = "text", required = true),
            SduiFormField(key = "schedule", label = "Schedule", kind = "text", required = true),
            SduiFormField(key = "kind", label = "Type", kind = "select"),
        ),
        submitAction = SduiActionRef(actionId = "scheduler.jobs.create"),
    )

    @Test
    fun `each 422 field message lands under its own field key, and an untouched field has no error`() {
        val fixture = SduiFixtures.read("validation-error.json")
        val decoded = SduiJson.decodeFromString(SduiValidationError.serializer(), fixture)

        val bound = bindErrors(jobForm(), decoded.fields)

        assertEquals(listOf("required"), bound.fieldErrors["name"])
        assertTrue(bound.fieldErrors.getValue("schedule").isNotEmpty())
        assertTrue(bound.fieldErrors["kind"] == null)
        assertTrue(bound.formLevel.isEmpty())
        assertTrue(bound.confirmation.isEmpty())
    }

    @Test
    fun `an error keyed _confirmation is routed to the confirmation slot, never to an input`() {
        val bound = bindErrors(jobForm(), mapOf(ConfirmationFieldKey to listOf("confirmation required")))

        assertEquals(listOf("confirmation required"), bound.confirmation)
        assertTrue(bound.fieldErrors.isEmpty())
        assertTrue(bound.formLevel.isEmpty())
    }

    @Test
    fun `an error keyed to a field the form does not declare is kept, not dropped`() {
        val bound = bindErrors(jobForm(), mapOf("foo" to listOf("unknown field")))

        assertEquals(listOf("unknown field"), bound.formLevel)
        assertTrue(bound.fieldErrors.isEmpty())
        assertTrue(bound.confirmation.isEmpty())
    }

    @Test
    fun `submitting clears previously bound errors before dispatching`() {
        val state = FormErrorState()
        state.bind(jobForm(), mapOf("name" to listOf("required")))
        assertTrue(state.bound.fieldErrors.isNotEmpty())

        state.clearOnSubmit()

        assertTrue(state.bound.fieldErrors.isEmpty())
        assertTrue(state.bound.formLevel.isEmpty())
        assertTrue(state.bound.confirmation.isEmpty())
    }

    @Test
    fun `a row action and a standalone action resolve the same confirmation by action id`() {
        val confirmDeclaration = SduiComponent.ConfirmDestructive(
            id = "confirm-delete",
            actionId = "x.delete",
            message = "Are you sure?",
        )
        val table = SduiComponent.Table(
            id = "jobs-table",
            columns = emptyList(),
            rowsSource = SduiDataSource(endpoint = "/api/mobile/v1/scheduler/jobs"),
            rowActions = listOf(SduiActionRef(actionId = "x.delete")),
        )
        val screen = SduiScreen(id = "s1", title = "S", components = listOf(table, confirmDeclaration))

        assertEquals(confirmDeclaration, confirmationFor(screen, "x.delete"))
        assertNull(confirmationFor(screen, "x.other"))
    }
}
