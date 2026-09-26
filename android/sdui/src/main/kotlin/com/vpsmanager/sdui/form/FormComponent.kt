package com.vpsmanager.sdui.form

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.core.sdui.SduiDataSource
import com.vpsmanager.core.sdui.SduiFormField
import com.vpsmanager.sdui.actionrunner.ActionInvocation
import com.vpsmanager.sdui.actionrunner.ActionOutcome
import com.vpsmanager.sdui.actionrunner.ActionRunner
import com.vpsmanager.sdui.actionrunner.Confirmation
import com.vpsmanager.sdui.actionrunner.ConfirmationFieldKey
import com.vpsmanager.sdui.confirm.ConfirmDestructiveComponent
import com.vpsmanager.sdui.data.ComponentDataState
import com.vpsmanager.sdui.data.rememberComponentDataState
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * Every message a 422 keyed to a form's fields splits into
 * (nothing a server said is ever dropped):
 *
 * - [fieldErrors]: keyed by [SduiFormField.key] — shown as that input's own
 *   supporting text.
 * - [confirmation]: keyed [ConfirmationFieldKey] — the server refused an
 *   unconfirmed destructive submit; shown next to the submit control, never
 *   attached to any input.
 * - [formLevel]: keyed to something the form does not declare — shown above
 *   the form instead of being silently discarded.
 */
data class BoundErrors(
    val fieldErrors: Map<String, List<String>> = emptyMap(),
    val formLevel: List<String> = emptyList(),
    val confirmation: List<String> = emptyList(),
)

/**
 * Pure classification of a 422 `fields` map against [form]'s own field keys.
 * JVM-testable without any Compose infrastructure — this is what
 * `ValidationBindingTest` exercises directly; the composable below is only
 * this function's presentation.
 */
fun bindErrors(form: SduiComponent.Form, fields: Map<String, List<String>>): BoundErrors {
    val knownKeys = form.fields.mapTo(mutableSetOf()) { it.key }
    val fieldErrors = mutableMapOf<String, List<String>>()
    val formLevel = mutableListOf<String>()
    val confirmation = mutableListOf<String>()
    fields.forEach { (key, messages) ->
        when {
            key == ConfirmationFieldKey -> confirmation += messages
            key in knownKeys -> fieldErrors[key] = messages
            else -> formLevel += messages
        }
    }
    return BoundErrors(fieldErrors = fieldErrors, formLevel = formLevel, confirmation = confirmation)
}

/**
 * The error state one form's UI holds, kept independent of Compose so
 * "submitting clears previously bound errors before dispatching" (Task 2
 * Test 4) can run as a plain JUnit test against this class rather than
 * needing a Compose test rule.
 */
class FormErrorState {
    var bound: BoundErrors = BoundErrors()
        private set

    fun bind(form: SduiComponent.Form, fields: Map<String, List<String>>) {
        bound = bindErrors(form, fields)
    }

    /** Called the moment a new dispatch begins — a message from a previous
     *  attempt must never survive past the start of the next one. */
    fun clearOnSubmit() {
        bound = BoundErrors()
    }
}

/**
 * [field]'s current text value coerced to the JSON shape its [SduiFormField.kind]
 * implies, so a `number`/`bool` field round-trips as a JSON number/boolean
 * rather than always as a JSON string. A kind this build does not recognize
 * falls back to a string — the server, not this renderer, owns the field
 * vocabulary.
 */
private fun coerceValue(field: SduiFormField, raw: String): JsonElement = when (field.kind) {
    "number" -> raw.toDoubleOrNull()?.let { JsonPrimitive(it) } ?: JsonPrimitive(raw)
    "bool" -> JsonPrimitive(raw.toBooleanStrictOrNull() ?: false)
    else -> JsonPrimitive(raw)
}

/**
 * Renders a [SduiComponent.Form]: one input per [SduiFormField],
 * a submit button that dispatches `submit_action` through [actionRunner], and
 * per-field error slots bound from a 422 by [bindErrors]. If [confirmations]
 * declares a `confirm_destructive` for `submit_action.action_id`, submitting
 * opens that confirmation dialog first — the same index a table's row actions
 * and [com.vpsmanager.sdui.action.ActionComponent] consult, never a
 * form-specific heuristic.
 */
@Composable
fun FormComponent(
    form: SduiComponent.Form,
    actionRunner: ActionRunner?,
    confirmations: Map<String, SduiComponent.ConfirmDestructive> = emptyMap(),
    onOutcome: (ActionOutcome) -> Unit = {},
) {
    val values = remember(form.id) {
        mutableStateMapOf<String, String>().apply {
            form.fields.forEach { field -> field.value?.let { put(field.key, it) } }
        }
    }
    val errorState = remember(form.id) { FormErrorState() }
    var boundErrors by remember(form.id) { mutableStateOf(errorState.bound) }
    var inFlight by remember(form.id) { mutableStateOf(false) }
    var pendingConfirmation by remember(form.id) { mutableStateOf<SduiComponent.ConfirmDestructive?>(null) }
    val scope = rememberCoroutineScope()

    fun buildInput(): JsonObject = buildJsonObject {
        form.fields.forEach { field -> put(field.key, coerceValue(field, values[field.key].orEmpty())) }
    }

    fun dispatch(confirmation: Confirmation?) {
        val runner = actionRunner ?: return
        errorState.clearOnSubmit()
        boundErrors = errorState.bound
        inFlight = true
        scope.launch {
            val outcome = runner.run(
                ActionInvocation(
                    actionId = form.submitAction.actionId,
                    input = buildInput(),
                    confirmation = confirmation,
                    destructive = confirmations.containsKey(form.submitAction.actionId),
                ),
            )
            inFlight = false
            if (outcome is ActionOutcome.ValidationFailed) {
                errorState.bind(form, outcome.fields)
                boundErrors = errorState.bound
            }
            onOutcome(outcome)
        }
    }

    fun onSubmitClick() {
        val declaration = confirmations[form.submitAction.actionId]
        if (declaration != null) {
            pendingConfirmation = declaration
        } else {
            dispatch(null)
        }
    }

    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        boundErrors.formLevel.forEach { message ->
            Text(text = message, color = MaterialTheme.colorScheme.error)
        }
        form.fields.forEach { field ->
            FormField(
                field = field,
                value = values[field.key].orEmpty(),
                onValueChange = { values[field.key] = it },
                errors = boundErrors.fieldErrors[field.key].orEmpty(),
            )
        }
        boundErrors.confirmation.forEach { message ->
            Text(text = message, color = MaterialTheme.colorScheme.error)
        }
        Button(onClick = ::onSubmitClick, enabled = !inFlight) {
            Text(form.submitAction.label ?: "Submit")
        }
    }

    pendingConfirmation?.let { declaration ->
        ConfirmDestructiveComponent(
            descriptor = declaration,
            onConfirm = { confirmation ->
                pendingConfirmation = null
                dispatch(confirmation)
            },
            onDismiss = { pendingConfirmation = null },
        )
    }
}

@Composable
private fun FormField(
    field: SduiFormField,
    value: String,
    onValueChange: (String) -> Unit,
    errors: List<String>,
) {
    val isError = errors.isNotEmpty()
    val supportingText: (@Composable () -> Unit)? = if (isError) {
        { Text(errors.joinToString(" ")) }
    } else {
        null
    }

    when (field.kind) {
        "bool" -> Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(field.label)
            Switch(
                checked = value.toBooleanStrictOrNull() ?: false,
                onCheckedChange = { onValueChange(it.toString()) },
            )
        }
        "select" -> SelectField(
            field = field,
            value = value,
            onValueChange = onValueChange,
            isError = isError,
            supportingText = supportingText,
        )
        "textarea" -> OutlinedTextField(
            value = value,
            onValueChange = onValueChange,
            label = { Text(field.label) },
            placeholder = field.placeholder?.let { { Text(it) } },
            isError = isError,
            supportingText = supportingText,
            minLines = 3,
            modifier = Modifier.fillMaxWidth(),
        )
        // "text", "number" and any kind this build does not specifically
        // know all get a single-line input -- the value is always sent as
        // whatever coerceValue() maps that kind to.
        else -> OutlinedTextField(
            value = value,
            onValueChange = onValueChange,
            label = { Text(field.label) },
            placeholder = field.placeholder?.let { { Text(it) } },
            isError = isError,
            supportingText = supportingText,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun rememberRemoteOptionRows(source: SduiDataSource): List<JsonObject> {
    val state by rememberComponentDataState(source)
    return (state as? ComponentDataState.Data)?.rows.orEmpty()
}

/** One dropdown entry: the value sent on selection, and the optional group
 *  header it is shown under -- present only when the server's option rows
 *  carry a `group` key of their own. */
private data class OptionEntry(val value: String, val group: String? = null)

private fun optionEntriesFor(field: SduiFormField, remoteRows: List<JsonObject>): List<OptionEntry> =
    field.options?.map { OptionEntry(it) }
        ?: remoteRows.map { row ->
            val value = (row["value"] as? JsonPrimitive)?.content
                ?: (row["label"] as? JsonPrimitive)?.content.orEmpty()
            val group = (row["group"] as? JsonPrimitive)?.content
            OptionEntry(value = value, group = group)
        }

@Composable
private fun SelectField(
    field: SduiFormField,
    value: String,
    onValueChange: (String) -> Unit,
    isError: Boolean,
    supportingText: (@Composable () -> Unit)?,
) {
    var expanded by remember { mutableStateOf(false) }
    val remoteRows = field.optionsSource?.let { rememberRemoteOptionRows(it) }.orEmpty()
    val options = optionEntriesFor(field, remoteRows)

    Box {
        OutlinedTextField(
            value = value,
            onValueChange = {},
            readOnly = true,
            label = { Text(field.label) },
            placeholder = field.placeholder?.let { { Text(it) } },
            isError = isError,
            supportingText = supportingText,
            modifier = Modifier
                .fillMaxWidth()
                .clickable { expanded = true },
        )
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            options.groupBy { it.group }.forEach { (group, entries) ->
                if (group != null) {
                    DropdownMenuItem(
                        enabled = false,
                        text = { Text(text = group, style = MaterialTheme.typography.labelSmall) },
                        onClick = {},
                    )
                }
                entries.forEach { entry ->
                    DropdownMenuItem(
                        text = { Text(entry.value) },
                        onClick = {
                            onValueChange(entry.value)
                            expanded = false
                        },
                        modifier = Modifier.padding(start = if (group != null) 8.dp else 0.dp),
                    )
                }
            }
        }
    }
}
