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
 * First-boot diagnostic screen.
 *
 * This app was built with no device available and the operator has only
 * the phone — no `adb`, no logcat. When something breaks during startup,
 * this screen is the only channel through which the information leaves
 * the device. That is why it shows the whole stack trace, selectable,
 * instead of "an error occurred".
 *
 * [falhasDeAtualizacao] brings the same channel to the other moment when
 * the app fails with no way to explain itself: installing its own update.
 * `PackageInstaller`'s `EXTRA_STATUS_MESSAGE` ("signature does not match",
 * "version downgrade", "blocked by device policy") is the only sentence
 * that tells those cases apart, and it does not fit in the banner at the
 * top — which is why the banner sends you here. Unlike the other two
 * sections, this one is reachable WITH the app working (route
 * `diagnostico`), because the owner needs it precisely when the app is up
 * and only the update failed.
 */
@Composable
fun DiagnosticoScreen(
    falhasDeInit: List<String>,
    ultimoCrash: String?,
    onLimpar: () -> Unit,
    falhasDeAtualizacao: String? = null,
    // Reached through the `diagnostico` route the app is ALREADY open, and
    // "try opening the app" there would be a meaningless sentence.
    rotuloLimpar: String = "Clear and try opening the app",
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

            ProcedenciaDaInstalacao()

            if (falhasDeInit.isNotEmpty()) {
                Text("Failed steps", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Card {
                    Column(Modifier.padding(12.dp)) {
                        falhasDeInit.forEach { falha ->
                            Text(
                                "• $falha",
                                style = MaterialTheme.typography.bodySmall,
                                fontFamily = FontFamily.Monospace,
                            )
                            Spacer(Modifier.height(6.dp))
                        }
                    }
                }
                Spacer(Modifier.height(16.dp))
            }

            if (falhasDeAtualizacao != null) {
                Text("App update", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Card {
                    Text(
                        falhasDeAtualizacao,
                        modifier = Modifier.padding(12.dp),
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                Spacer(Modifier.height(16.dp))
            }

            if (ultimoCrash != null) {
                Text("Previous process crash", style = MaterialTheme.typography.titleMedium)
                Spacer(Modifier.height(8.dp))
                Card {
                    Text(
                        ultimoCrash,
                        modifier = Modifier.padding(12.dp),
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                Spacer(Modifier.height(16.dp))
            }

            if (falhasDeInit.isEmpty() && ultimoCrash == null && falhasDeAtualizacao == null) {
                Text(
                    "Nothing recorded — no step failed, the previous process " +
                        "exited normally and the update reported no error.",
                    style = MaterialTheme.typography.bodyMedium,
                )
                Spacer(Modifier.height(16.dp))
            }

            Button(onClick = onLimpar) {
                Text(rotuloLimpar)
            }
        }
    }
}

/**
 * Who Android thinks installed this app.
 *
 * ## Why this became a screen
 *
 * Updating without the system dialog — the only path Samsung's Auto
 * Blocker does not interrupt — requires Android to KNOW who installed the
 * app. An app whose provenance is null is refused with
 * `Self update is blocked by unknown source package`, and the code falls
 * back to the dialog. That is where Auto Blocker cuts in.
 *
 * That state decided the behaviour and showed up nowhere: I had been
 * inferring it from the symptom, and got it wrong twice in a row because
 * of that. Now it is readable on the device, by whoever is holding it.
 */
@Composable
private fun ProcedenciaDaInstalacao() {
    val contexto = LocalContext.current
    val instalador = remember(contexto) {
        runCatching {
            contexto.packageManager
                .getInstallSourceInfo(contexto.packageName)
                .installingPackageName
        }.getOrNull()
    }
    val ehOProprioApp = instalador == contexto.packageName

    Text("Install source", style = MaterialTheme.typography.titleMedium)
    Spacer(Modifier.height(8.dp))
    Card {
        Column(Modifier.padding(12.dp)) {
            Text(
                text = instalador ?: "(none — installed from a manually opened APK)",
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                text = when {
                    ehOProprioApp ->
                        "Updates can happen without the system dialog — which is " +
                            "where Samsung's Auto Blocker interrupts them."
                    instalador != null ->
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
