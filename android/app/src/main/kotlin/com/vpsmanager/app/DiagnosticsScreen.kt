package com.vpsmanager.app

import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp

/**
 * Diagnostic screen. Without adb or logcat it is the only way failure details leave the
 * device, so it shows full, selectable stack traces.
 *
 * [updateFailures] carries `PackageInstaller`'s status message, the only text that tells update
 * failures apart; it does not fit in the banner, so the banner links here (a route reachable
 * while the app is running).
 */
@Composable
fun DiagnosticsScreen(
    initFailures: List<String>,
    lastCrash: String?,
    onClear: () -> Unit,
    updateFailures: String? = null,
    // Overridden when reached from inside the running app, where "try opening" makes no sense.
    clearLabel: String = "Clear and try opening the app",
) {
    Scaffold { insets ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(insets)
                .padding(16.dp)
                .verticalScroll(rememberScrollState()),
        ) {
            Text(
                "Startup diagnostics",
                style = MaterialTheme.typography.headlineSmall,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                "The app started, but something failed. Send this text to whoever " +
                    "is building the app — it says exactly what broke.",
                style = MaterialTheme.typography.bodyMedium,
            )
            Spacer(Modifier.height(16.dp))

            InstallProvenance()

            if (initFailures.isNotEmpty()) {
                Text("Failed steps", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Card {
                    Column(Modifier.padding(12.dp)) {
                        initFailures.forEach { failure ->
                            Text(
                                "• $failure",
                                style = MaterialTheme.typography.bodySmall,
                                fontFamily = FontFamily.Monospace,
                            )
                            Spacer(Modifier.height(6.dp))
                        }
                    }
                }
                Spacer(Modifier.height(16.dp))
            }

            if (updateFailures != null) {
                Text("App update", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Card {
                    Text(
                        updateFailures,
                        modifier = Modifier.padding(12.dp),
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                Spacer(Modifier.height(16.dp))
            }

            if (lastCrash != null) {
                Text("Previous process crash", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Card {
                    Text(
                        lastCrash,
                        modifier = Modifier.padding(12.dp),
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                Spacer(Modifier.height(16.dp))
            }

            if (initFailures.isEmpty() && lastCrash == null && updateFailures == null) {
                Text(
                    "Nothing recorded — no step failed, the previous process " +
                        "exited normally and the update reported no error.",
                    style = MaterialTheme.typography.bodyMedium,
                )
                Spacer(Modifier.height(16.dp))
            }

            Button(onClick = onClear) {
                Text(clearLabel)
            }
        }
    }
}

/**
 * Who Android thinks installed this app. Silent updates (the only path Samsung's Auto Blocker
 * does not interrupt) require a known installer; otherwise they fail with
 * `Self update is blocked by unknown source package` and fall back to the dialog. Shown here
 * because that state decides update behavior and is otherwise invisible.
 */
@Composable
private fun InstallProvenance() {
    val context = LocalContext.current
    val installer = remember(context) {
        runCatching {
            context.packageManager
                .getInstallSourceInfo(context.packageName)
                .installingPackageName
        }.getOrNull()
    }
    val isOwnApp = installer == context.packageName

    Text("Install source", style = MaterialTheme.typography.titleMedium)
    Spacer(Modifier.height(8.dp))
    Card {
        Column(Modifier.padding(12.dp)) {
            Text(
                text = installer ?: "(none — installed from a manually opened APK)",
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = when {
                    isOwnApp ->
                        "Updates can happen without the system dialog — which is " +
                            "where Samsung's Auto Blocker interrupts them."
                    installer != null ->
                        "Another app installed this one. Updates go through the " +
                            "system dialog."
                    else ->
                        "With no known source, Android REFUSES the silent update " +
                            "and it goes through the dialog — which Auto Blocker " +
                            "interrupts. To get out of this state, let ONE " +
                            "update finish through the in-app button: from then " +
                            "on, the app itself is listed as the source."
                },
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
    Spacer(Modifier.height(16.dp))
}
