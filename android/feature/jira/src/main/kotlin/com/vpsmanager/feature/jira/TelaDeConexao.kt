package com.vpsmanager.feature.jira

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp

/** Test tag of the connection screen. */
internal const val TAG_CONEXAO = "jira-conexao"

/**
 * Connecting the Jira account.
 *
 * ## Why this is not an error screen
 *
 * Never having connected is everybody's initial state, not a failure. The server
 * returns `connected:false` with HTTP 200 precisely so the application can show
 * THIS form instead of "the board could not be loaded" with a try-again button
 * that will never work.
 *
 * ## Where the token ends up
 *
 * Straight into the server's per-user vault — the SAME one the web panel uses.
 * Connecting here connects there, and the token never comes back in any
 * response, nor is it kept on the device.
 */
@Composable
internal fun TelaDeConexao(
    ocupado: Boolean,
    aoConectar: (String, String, String, String?) -> Unit,
) {
    var site by remember { mutableStateOf("") }
    var email by remember { mutableStateOf("") }
    var token by remember { mutableStateOf("") }
    var projeto by remember { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp)
            .testTag(TAG_CONEXAO),
    ) {
        Text("Connect to Jira", style = MaterialTheme.typography.headlineSmall)
        Spacer(Modifier.height(8.dp))
        Text(
            text = "The credentials stay in this server's vault, under your account — the same ones the web panel uses.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )

        Spacer(Modifier.height(20.dp))
        OutlinedTextField(
            value = site,
            onValueChange = { site = it },
            label = { Text("Site") },
            placeholder = { Text("yourcompany") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
            modifier = Modifier.fillMaxWidth(),
        )

        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = email,
            onValueChange = { email = it },
            label = { Text("Atlassian account email") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next),
            modifier = Modifier.fillMaxWidth(),
        )

        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = token,
            onValueChange = { token = it },
            label = { Text("API token") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Next),
            modifier = Modifier.fillMaxWidth(),
        )

        Spacer(Modifier.height(12.dp))
        OutlinedTextField(
            value = projeto,
            onValueChange = { projeto = it },
            label = { Text("Default project (optional)") },
            placeholder = { Text("VPSM") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
            modifier = Modifier.fillMaxWidth(),
        )

        Spacer(Modifier.height(20.dp))
        Button(
            onClick = { aoConectar(site.trim(), email.trim(), token.trim(), projeto.trim().takeIf { it.isNotBlank() }) },
            enabled = !ocupado && site.isNotBlank() && email.isNotBlank() && token.isNotBlank(),
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(if (ocupado) "Connecting…" else "Connect")
        }
    }
}
