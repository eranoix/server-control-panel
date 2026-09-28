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

@Composable
fun ConfirmDestructiveComponent(
    descriptor: SduiComponent.ConfirmDestructive,
    onConfirm: (Confirmation) -> Unit,
    onDismiss: () -> Unit,
) {
    val requiredTyped = descriptor.requireTypedConfirmation
    var typed by remember(descriptor.id) { mutableStateOf("") }
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
