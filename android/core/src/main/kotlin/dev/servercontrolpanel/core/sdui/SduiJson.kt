package dev.servercontrolpanel.core.sdui

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonContentPolymorphicSerializer
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

val SduiJson: Json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    isLenient = false
}

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

interface SduiLogger {
    fun skippedUnknown(type: String, id: String)

    object None : SduiLogger {
        override fun skippedUnknown(type: String, id: String) = Unit
    }
}

fun parseScreen(json: String, logger: SduiLogger = SduiLogger.None): SduiEnvelope {
    val envelope = SduiJson.decodeFromString(SduiEnvelope.serializer(), json)
    envelope.screen.components.forEach { component ->
        if (component is SduiComponent.Unknown && !component.critical) {
            logger.skippedUnknown(component.type, component.id)
        }
    }
    return envelope
}
