package dev.servercontrolpanel.sdui.debug

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AssistChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.servercontrolpanel.core.sdui.SduiEnvelope
import dev.servercontrolpanel.core.sdui.SduiJson
import dev.servercontrolpanel.core.sdui.parseScreen
import dev.servercontrolpanel.sdui.BuildConfig
import dev.servercontrolpanel.sdui.SduiScreen
import dev.servercontrolpanel.sdui.actionrunner.ActionInvoker
import dev.servercontrolpanel.sdui.actionrunner.ActionRunner
import dev.servercontrolpanel.sdui.actionrunner.ComponentDataFetcher
import dev.servercontrolpanel.sdui.actionrunner.ScreenRefetcher
import dev.servercontrolpanel.sdui.actionrunner.ScreenState
import dev.servercontrolpanel.sdui.registry.LocalActionRunner
import dev.servercontrolpanel.sdui.registry.LocalScreenState
import dev.servercontrolpanel.sdui.registry.renderPolicyFor
import dev.servercontrolpanel.data.sdui.SduiActionHttpResult
import dev.servercontrolpanel.data.sdui.SduiDataResult
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonObject

@Composable
fun PayloadPreviewScreen(sampleFixtures: Map<String, String> = emptyMap()) {
    if (!BuildConfig.DEBUG) return

    var rawPayload by remember { mutableStateOf("") }
    var parseError by remember { mutableStateOf<String?>(null) }
    var envelope by remember { mutableStateOf<SduiEnvelope?>(null) }
    var lastInvocation by remember { mutableStateOf<Pair<String, JsonObject>?>(null) }

    fun parse(json: String) {
        rawPayload = json
        try {
            envelope = parseScreen(json)
            parseError = null
        } catch (e: SerializationException) {
            envelope = null
            parseError = e.message ?: "Failed to parse the payload."
        }
    }

    val screenState = remember(envelope) {
        val current = envelope
        if (current == null) {
            null
        } else {
            ScreenState(
                initialEnvelope = current,
                componentDataFetcher = ComponentDataFetcher { SduiDataResult.Empty },
                screenRefetcher = ScreenRefetcher { current },
            )
        }
    }
    val actionRunner = remember(screenState) {
        screenState?.let { state ->
            ActionRunner(
                invoker = ActionInvoker { actionId, requestBody ->
                    lastInvocation = actionId to requestBody
                    SduiActionHttpResult.Success(JsonObject(emptyMap()))
                },
                screenState = state,
            )
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            text = "SDUI payload preview (debug only)",
            style = MaterialTheme.typography.titleMedium,
        )

        if (sampleFixtures.isNotEmpty()) {
            LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                items(items = sampleFixtures.keys.toList()) { name ->
                    AssistChip(onClick = { sampleFixtures[name]?.let(::parse) }, label = { Text(name) })
                }
            }
        }

        OutlinedTextField(
            value = rawPayload,
            onValueChange = ::parse,
            label = { Text("Paste the JSON payload here") },
            minLines = 6,
            modifier = Modifier.fillMaxWidth(),
        )

        parseError?.let { message ->
            Text(text = message, color = MaterialTheme.colorScheme.error)
        }

        envelope?.let { env ->
            HorizontalDivider()
            Text(text = "Per-component diagnostics", style = MaterialTheme.typography.titleSmall)
            env.screen.components.forEach { component ->
                Text(
                    text = "${component.id} (${component::class.simpleName}) -> ${renderPolicyFor(component)}",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            HorizontalDivider()
            CompositionLocalProvider(
                LocalActionRunner provides actionRunner,
                LocalScreenState provides screenState,
            ) {
                SduiScreen(env)
            }
        }

        lastInvocation?.let { (actionId, body) ->
            HorizontalDivider()
            Text(text = "Last action (inert -- nothing was sent)", style = MaterialTheme.typography.titleSmall)
            Text(text = actionId, style = MaterialTheme.typography.bodySmall)
            Text(
                text = SduiJson.encodeToString(JsonObject.serializer(), body),
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
}
