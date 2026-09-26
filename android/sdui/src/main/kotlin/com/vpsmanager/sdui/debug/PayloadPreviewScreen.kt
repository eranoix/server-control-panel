package com.vpsmanager.sdui.debug

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
import com.vpsmanager.core.sdui.SduiEnvelope
import com.vpsmanager.core.sdui.SduiJson
import com.vpsmanager.core.sdui.parseScreen
import com.vpsmanager.sdui.BuildConfig
import com.vpsmanager.sdui.SduiScreen
import com.vpsmanager.sdui.actionrunner.ActionInvoker
import com.vpsmanager.sdui.actionrunner.ActionRunner
import com.vpsmanager.sdui.actionrunner.ComponentDataFetcher
import com.vpsmanager.sdui.actionrunner.ScreenRefetcher
import com.vpsmanager.sdui.actionrunner.ScreenState
import com.vpsmanager.sdui.registry.LocalActionRunner
import com.vpsmanager.sdui.registry.LocalScreenState
import com.vpsmanager.sdui.registry.renderPolicyFor
import com.vpsmanager.data.sdui.SduiActionHttpResult
import com.vpsmanager.data.sdui.SduiDataResult
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.JsonObject

/**
 * Renders an arbitrary pasted SDUI payload with the exact same
 * [SduiScreen] renderer the real app uses, plus a per-component diagnostics
 * strip (type + the [renderPolicyFor] it resolved to) — the preview tooling
 * PITFALLS.md names as missing in teams that struggled with SDUI.
 *
 * **Self-gated on `BuildConfig.DEBUG`, not just on how it gets called.** A
 * payload pasted here names its own endpoints, and this module's build type
 * is the one signal this function trusts to decide whether that is safe: the
 * first statement below is `if (!BuildConfig.DEBUG) return`, before any
 * state, before the envelope is ever parsed. A release build of `:sdui`
 * therefore renders nothing here regardless of whether — or how carelessly —
 * a future navigation graph ever calls this composable; the guard does not
 * depend on the caller remembering to add one of its own.
 *
 * Both directions this screen can reach the network are inert, not just the
 * mutation one: [actionRunner] (built internally, never injected from
 * outside) is wired to a stub [ActionInvoker] that records the request it
 * *would* have sent instead of calling the network, and every read component
 * (`table`/`list`/`detail`/`chart`) reached under [screenState] resolves its
 * `rows_source`/`data_source`/`series_source` through that same [screenState]'s
 * [ComponentDataFetcher] — `com.vpsmanager.sdui.data.rememberComponentDataState`
 * prefers a [com.vpsmanager.sdui.registry.LocalScreenState]'s fetcher over the
 * real `SduiDataRepository` when one is provided, which this screen always
 * does. A pasted payload naming a real endpoint therefore never issues a real
 * request, on either the read or the write path.
 *
 * [sampleFixtures] is a name-to-raw-JSON map for the fixture picker
 * ("bundled sample payloads"). This composable does not read Android assets
 * itself — no asset-bundling convention exists yet in `:sdui` — so the
 * caller supplies whatever fixtures it wants offered (e.g. loaded from the
 * app's own assets in plan 07-09).
 */
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
