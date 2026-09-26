package dev.servercontrolpanel.feature.auth

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.data.config.ConfigureServerResult
import dev.servercontrolpanel.data.config.ServerConfigRepository

/**
 * First-run gate: nothing else in the app can reach a real server until a
 * base URL is configured here. This is now the FALLBACK path — the primary
 * first-run flow is [PasskeyRegisterFlow], whose scanned QR already carries
 * the server address (`PairingPayload.serverUrl`), so pairing normally never
 * requires typing a hostname. This screen stays reachable via
 * `onManualSetupRequested` for a device that cannot scan (no camera, no
 * admin physically present with the panel) or a manual override for local
 * development.
 */
@Composable
fun ServerSetupScreen(
    serverConfigRepository: ServerConfigRepository,
    modifier: Modifier = Modifier,
    onConfigured: () -> Unit,
) {
    var rawUrl by remember { mutableStateOf("") }
    var allowInsecureHttp by remember { mutableStateOf(false) }
    var errorMessage by remember { mutableStateOf<String?>(null) }

    Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(
            modifier = Modifier.padding(24.dp).widthIn(max = 480.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text(text = "Set up server", style = MaterialTheme.typography.titleLarge)
            Text(text = "Enter the address of your Server Control Panel to continue.")
            OutlinedTextField(
                value = rawUrl,
                onValueChange = {
                    rawUrl = it
                    errorMessage = null
                },
                label = { Text("https://your-server.example.com") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = allowInsecureHttp, onCheckedChange = { allowInsecureHttp = it })
                Text(text = "Allow http:// (local development only)")
            }
            errorMessage?.let { message ->
                Text(text = message, color = MaterialTheme.colorScheme.error)
            }
            Button(
                onClick = {
                    when (
                        val result = serverConfigRepository.configure(
                            rawUrl = rawUrl,
                            allowInsecureHttp = allowInsecureHttp,
                            allowRepoint = false,
                        )
                    ) {
                        is ConfigureServerResult.Applied -> onConfigured()
                        is ConfigureServerResult.Rejected -> errorMessage = result.reason
                        is ConfigureServerResult.RepointBlocked -> errorMessage =
                            "This app is already set up for ${result.currentBaseUrl}. " +
                                "Remove the current setup before switching servers."
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(text = "Continue")
            }
        }
    }
}
