package com.vpsmanager.sdui.action

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
import com.vpsmanager.core.sdui.SduiComponent
import com.vpsmanager.sdui.actionrunner.ActionInvocation
import com.vpsmanager.sdui.actionrunner.ActionOutcome
import com.vpsmanager.sdui.actionrunner.ActionRunner
import com.vpsmanager.sdui.actionrunner.Confirmation
import com.vpsmanager.sdui.confirm.ConfirmDestructiveComponent
import kotlinx.coroutines.launch

/**
 * Renders a standalone [SduiComponent.Action] as a button dispatching
 * `action_id` with no input. [style] maps to a
 * `:design-system`/Material3 button tone -- it is presentation only, never a
 * signal of what the action does.
 *
 * Whether the button routes through confirmation first is decided purely by
 * [confirmations] -- the index of every `confirm_destructive` the server
 * declared for the current screen, keyed by `action_id`. There is no
 * inspection of [SduiComponent.Action.label] or `action_id` text anywhere in
 * this file: an action this build has never seen before is handled exactly
 * like one it has, and a missing confirmation declaration means this
 * component dispatches directly -- the server still refuses an unconfirmed
 * action that actually is destructive (plan 07-04), so a missing declaration
 * surfaces as a visible `_confirmation` error rather than as a silent bypass.
 */
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

/** [style] is a presentation-only tone ("primary"/"secondary"/"danger") --
 *  never read as a hint about what the action does. Any other value, or
 *  none, falls back to the default button tone. */
@Composable
private fun colorsFor(style: String?): ButtonColors = when (style) {
    "danger" -> ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)
    "secondary" -> ButtonDefaults.buttonColors(
        containerColor = MaterialTheme.colorScheme.secondaryContainer,
        contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
    )
    else -> ButtonDefaults.buttonColors()
}
