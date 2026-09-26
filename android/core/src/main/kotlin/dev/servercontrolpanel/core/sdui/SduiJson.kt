package dev.servercontrolpanel.core.sdui

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonContentPolymorphicSerializer
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * The [Json] instance used for every SDUI decode.
 *
 * - `ignoreUnknownKeys`: a new optional server field must never break an older client.
 * - `explicitNulls = false`: an explicit `null` (as Go may emit) falls back to the Kotlin default.
 * - `isLenient = false`: unknown vocabulary is tolerated, malformed JSON is not.
 */
val SduiJson: Json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    isLenient = false
}

/**
 * Picks the concrete [SduiComponent] serializer from the wire `type` string.
 * Unrecognized types decode as [SduiComponent.Unknown] instead of throwing, so an
 * old client never crashes on a newer server's payload.
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
 * Observes each [SduiComponent.Unknown] the parser skips, so skips are never
 * silent. No-op by default; `:sdui` provides the real one. Not `android.util.Log`
 * because `:core` has no Android dependency.
 */
interface SduiLogger {
    fun skippedUnknown(type: String, id: String)

    object None : SduiLogger {
        override fun skippedUnknown(type: String, id: String) = Unit
    }
}

/**
 * Decodes an SDUI payload with forward-compatibility rules (see `ForwardCompatTest`):
 *
 * 1. An unknown `type` decodes to [SduiComponent.Unknown] instead of throwing.
 * 2. The renderer uses `critical` to choose placeholder vs skip; only
 *    non-critical skips are logged here.
 * 3. Unknown fields are ignored, but a missing required field is still a parse error.
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
