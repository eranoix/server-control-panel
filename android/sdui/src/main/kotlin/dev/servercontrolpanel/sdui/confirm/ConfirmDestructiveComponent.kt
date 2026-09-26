package dev.servercontrolpanel.sdui.confirm

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.sdui.actionrunner.Confirmation

/**
 * The destructive-confirmation client side: a [SduiComponent.ConfirmDestructive]
 * declared on a screen is never rendered as a standalone piece of UI (see
 * `ComponentRegistry.RenderComponent`, which dispatches it to nothing). It
 * exists only to be looked up by `action_id` -- by
 * `dev.servercontrolpanel.sdui.actionrunner.ScreenState.confirmations` -- and to
 * supply the dialog this composable renders when
 * `dev.servercontrolpanel.sdui.action.ActionComponent` or
 * `dev.servercontrolpanel.sdui.form.FormComponent` is about to dispatch that exact
 * `action_id`. A row action resolves through the same index, so a row's
 * action is confirmable without the component vocabulary ever needing a
 * nested "confirm" child.
 *
 * This dialog is a courtesy, not the enforcement point: plan 07-04's server
 * refuses an unconfirmed destructive action regardless of whether this
 * dialog ever appears. Nothing in this file -- or anywhere else in this
 * module -- infers whether an action is destructive from its name; the only
 * signal is whether the server declared a [SduiComponent.ConfirmDestructive]
 * for that `action_id`.
 */
@Composable
fun ConfirmDestructiveComponent(
    descriptor: SduiComponent.ConfirmDestructive,
    onConfirm: (Confirmation) -> Unit,
    onDismiss: () -> Unit,
) {
    val requiredTyped = descriptor.requireTypedConfirmation
    var typed by remember(descriptor.id) { mutableStateOf("") }
    // Exact match, no trim/case-fold: the server compares `typed` the same
    // way (plan 07-04), so the two sides can never disagree about what
    // counts as confirmed.
    val canConfirm = requiredTyped == null || typed == requiredTyped

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Confirmation required") },
        text = {
            Column {
                Text(descriptor.message)
                if (requiredTyped != null) {
                    OutlinedTextField(
                        value = typed,
                        onValueChange = { typed = it },
                        label = { Text("Type \"$requiredTyped\" to confirm") },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = canConfirm,
                onClick = {
                    onConfirm(Confirmation(confirmed = true, typed = typed.ifEmpty { null }))
                },
            ) {
                Text("Confirm")
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("Cancel")
            }
        },
    )
}
