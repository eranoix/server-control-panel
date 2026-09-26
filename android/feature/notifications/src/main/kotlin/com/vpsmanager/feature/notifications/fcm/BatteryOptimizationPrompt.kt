package com.vpsmanager.feature.notifications.fcm

import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import com.vpsmanager.data.push.PushOnboardingState

/**
 * Shown once, in the same post-login onboarding moment as the `POST_NOTIFICATIONS` request,
 * explaining that exempting the app from battery optimization materially improves push delivery
 * reliability on OEMs known for aggressive throttling (Samsung/Xiaomi/OnePlus). Explanatory and
 * consent-seeking, never a forced gate — declining dismisses it for good (see
 * [PushOnboardingState]), it is never re-shown on a later launch.
 */
@Composable
fun BatteryOptimizationPrompt(onDismissed: () -> Unit) {
    val context = LocalContext.current
    val onboardingState = remember { PushOnboardingState(context) }

    AlertDialog(
        onDismissRequest = {
            onboardingState.markBatteryOptimizationPromptSeen()
            onDismissed()
        },
        title = { Text("More reliable notifications") },
        text = {
            Text(
                "Some manufacturers (Samsung, Xiaomi, OnePlus) restrict background apps " +
                    "in a way that delays or blocks deploy and alert notifications. " +
                    "Allowing \"Unrestricted\" battery use for Server Control Panel prevents this.",
            )
        },
        confirmButton = {
            TextButton(onClick = {
                onboardingState.markBatteryOptimizationPromptSeen()
                context.startActivity(
                    Intent(
                        Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                        Uri.parse("package:${context.packageName}"),
                    ),
                )
                onDismissed()
            }) { Text("Allow") }
        },
        dismissButton = {
            TextButton(onClick = {
                onboardingState.markBatteryOptimizationPromptSeen()
                onDismissed()
            }) { Text("Not now") }
        },
    )
}

/** Whether [BatteryOptimizationPrompt] still needs to be shown to this install. */
@Composable
fun rememberShouldShowBatteryOptimizationPrompt(): Boolean {
    val context = LocalContext.current
    return remember { !PushOnboardingState(context).hasSeenBatteryOptimizationPrompt() }
}
