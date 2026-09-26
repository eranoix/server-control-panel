package com.vpsmanager.core.sdui

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonContentPolymorphicSerializer
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * The single configured [Json] instance every SDUI decode in this module
 * goes through.
 *
 * - `ignoreUnknownKeys = true` is the direct Kotlin equivalent of the
 *   researched OpenAPI `additionalProperties: true` pattern, and is what
 *   makes rule 4 of research/ARCHITECTURE.md's versioning section true: a
 *   server adding a new optional field to a known component must never break
 *   a client that predates that field.
 * - `explicitNulls = false` means an explicit JSON `null` for an optional
 *   field falls back to its Kotlin default instead of failing to decode —
 *   tolerating a null the Go marshaller may emit for an omitted pointer
 *   field, same spirit as `ignoreUnknownKeys`.
 * - `isLenient` stays `false`: tolerating unknown *vocabulary* (component
 *   types, fields) is the whole point of this module; tolerating malformed
 *   JSON syntax is a different concern and is NOT part of that contract.
 */
val SduiJson: Json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    isLenient = false
}

/**
 * Picks the concrete [SduiComponent] serializer from the wire `type` string.
 * Any `type` this build does not recognize falls through to
 * [SduiComponent.Unknown.serializer] instead of throwing — this is the
 * mechanical reason an old client can never crash on a new server's payload.
 */
object SduiComponentSerializer : JsonContentPolymorphicSerializer<SduiComponent>(SduiComponent::class) {
    override fun selectDeserializer(element: JsonElement) = when (element.jsonObject["type"]?.jsonPrimitive?.content) {
        "form" -> SduiComponent.Form.serializer()
        "table" -> SduiComponent.Table.serializer()
        "list" -> SduiComponent.ListComponent.serializer()
        "detail" -> SduiComponent.Detail.serializer()
        "action" -> SduiComponent.Action.serializer()
        "chart" -> SduiComponent.Chart.serializer()
        "confirm_destructive" -> SduiComponent.ConfirmDestructive.serializer()
        else -> SduiComponent.Unknown.serializer()
    }
}

/**
 * Observes an [SduiComponent.Unknown] the parser skipped, so a skip is never
 * silent (PITFALLS.md: "skip the node, log it, never throw" — a skip nobody
 * can observe is how a blank screen goes undiagnosed). No-op by default;
 * `:sdui` supplies a real implementation that surfaces to app diagnostics.
 *
 * Deliberately not an Android API: `:core` has zero Android dependency, so
 * this cannot be `android.util.Log`.
 */
interface SduiLogger {
    fun skippedUnknown(type: String, id: String)

    object None : SduiLogger {
        override fun skippedUnknown(type: String, id: String) = Unit
    }
}

/**
 * Decodes a raw SDUI JSON payload, applying the three forward-compatibility
 * rules this parser exists to prove:
 *
 * 1. An unrecognized `type` never throws — it decodes into
 *    [SduiComponent.Unknown] (proven by `ForwardCompatTest`'s
 *    `unknown-noncritical.json` / `unknown-critical.json` cases).
 * 2. An [SduiComponent.Unknown] with `critical == true` is distinguishable
 *    from one with `critical == false` — the renderer (plan 07-05) decides
 *    placeholder-vs-skip on that flag; only non-critical skips are logged
 *    here; critical ones are reported through the renderer's placeholder
 *    path instead (proven by `ForwardCompatTest`'s critical/non-critical
 *    pair and its logging test).
 * 3. An unrecognized field inside a recognized component is ignored, never
 *    fatal — a *missing required* field is still a hard parse error, since
 *    tolerance applies to additions, never to a contract violation (proven
 *    by `ForwardCompatTest`'s `unknown-extra-fields.json` and
 *    missing-required-field cases).
 */
fun parseScreen(json: String, logger: SduiLogger = SduiLogger.None): SduiEnvelope {
    val envelope = SduiJson.decodeFromString(SduiEnvelope.serializer(), json)
    envelope.screen.components.forEach { component ->
        if (component is SduiComponent.Unknown && !component.critical) {
            logger.skippedUnknown(component.type, component.id)
        }
    }
    return envelope
}
