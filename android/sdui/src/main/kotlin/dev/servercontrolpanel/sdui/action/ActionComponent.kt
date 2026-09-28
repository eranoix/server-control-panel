package dev.servercontrolpanel.sdui.action

import androidx.compose.material3.Button
import androidx.compose.material3.ButtonColors
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import dev.servercontrolpanel.core.sdui.SduiComponent
import dev.servercontrolpanel.sdui.actionrunner.ActionInvocation
import dev.servercontrolpanel.sdui.actionrunner.ActionOutcome
import dev.servercontrolpanel.sdui.actionrunner.ActionRunner
import dev.servercontrolpanel.sdui.actionrunner.Confirmation
import dev.servercontrolpanel.sdui.confirm.ConfirmDestructiveComponent
import kotlinx.coroutines.launch

@Composable
fun ActionComponent(
    component: SduiComponent.Action,
    actionRunner: ActionRunner?,
    confirmations: Map<String, SduiComponent.ConfirmDestructive> = emptyMap(),
    onOutcome: (ActionOutcome) -> Unit = {},
) {
    var inFlight by remember(component.id) { mutableStateOf(false) }
    var pendingConfirmation by remember(component.id) { mutableStateOf<SduiComponent.ConfirmDestructive?>(null) }
    val scope = rememberCoroutineScope()
    val declaration = confirmations[component.actionId]

    fun dispatch(confirmation: Confirmation?) {
        val runner = actionRunner ?: return
        inFlight = true
        scope.launch {
            val outcome = runner.run(
                ActionInvocation(
                    actionId = component.actionId,
                    confirmation = confirmation,
                    destructive = declaration != null,
                ),
            )
            inFlight = false
            onOutcome(outcome)
        }
    }

    Button(
        onClick = {
            if (declaration != null) {
                pendingConfirmation = declaration
            } else {
                dispatch(null)
            }
        },
        enabled = !inFlight,
        colors = colorsFor(component.style),
    ) {
        Text(component.label)
    }

    pendingConfirmation?.let { confirmDeclaration ->
        ConfirmDestructiveComponent(
            descriptor = confirmDeclaration,
            onConfirm = { confirmation ->
                pendingConfirmation = null
                dispatch(confirmation)
            },
            onDismiss = { pendingConfirmation = null },
        )
    }
}

@Composable
private fun colorsFor(style: String?): ButtonColors = when (style) {
    "danger" -> ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)
    "secondary" -> ButtonDefaults.buttonColors(
        containerColor = MaterialTheme.colorScheme.secondaryContainer,
        contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
    )
    else -> ButtonDefaults.buttonColors()
}
