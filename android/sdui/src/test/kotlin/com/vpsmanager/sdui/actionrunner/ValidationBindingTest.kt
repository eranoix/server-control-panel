package com.vpsmanager.sdui.actionrunner

import com.vpsmanager.core.sdui.SduiActionRef
import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.core.sdui.SduiDataSource
import com.vpsmanager.core.sdui.SduiFormField
import com.vpsmanager.core.sdui.SduiJson
import com.vpsmanager.core.sdui.SduiScreen
import com.vpsmanager.core.sdui.SduiValidationError
import com.vpsmanager.sdui.form.bindErrors
import com.vpsmanager.sdui.form.FormErrorState
import com.vpsmanager.sdui.registry.SduiFixtures
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * `bindErrors` is the form-error requirement --
 * JVM-testable without any Compose test infrastructure. `confirmationFor` is
 * Task 1's pure index, re-verified here against a screen shaped like Task
 * 2's Test 5: a standalone `confirm_destructive` plus a table whose
 * `row_actions` names the same `action_id`.
 */
class ValidationBindingTest {

    private fun jobForm() = SduiComponent.Form(
        id = "job-form",
        fields = listOf(
            SduiFormField(key = "name", label = "Nome", kind = "text", required = true),
            SduiFormField(key = "schedule", label = "Agenda", kind = "text", required = true),
            SduiFormField(key = "kind", label = "Tipo", kind = "select"),
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
        val bound = bindErrors(jobForm(), mapOf(ConfirmationFieldKey to listOf("confirmação obrigatória")))

        assertEquals(listOf("confirmação obrigatória"), bound.confirmation)
        assertTrue(bound.fieldErrors.isEmpty())
        assertTrue(bound.formLevel.isEmpty())
    }

    @Test
    fun `an error keyed to a field the form does not declare is kept, not dropped`() {
        val bound = bindErrors(jobForm(), mapOf("foo" to listOf("campo desconhecido")))

        assertEquals(listOf("campo desconhecido"), bound.formLevel)
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
            message = "Tem certeza?",
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
